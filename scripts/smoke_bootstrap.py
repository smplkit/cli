#!/usr/bin/env python3
"""Provision an ephemeral smplkit account for the release-smoke job.

Steps (stdlib-only):

  1. POST /api/v1/auth/register with a random email/password.
  2. Decode the JWT to recover user_id and account_id.
  3. Mint the ADR-036 HMAC verification token over `{user_id}:{exp}`
     using APP_AUTH_SECRET, and POST it to /api/v1/auth/verify-email.
  4. POST /api/v1/api_keys (with the now-verified JWT) to mint a
     fresh customer key.
  5. Write SMPLKIT_API_KEY, ACCOUNT_ID, USER_ID, ACCOUNT_TOKEN, and
     EMAIL into $GITHUB_OUTPUT (or stdout) so the calling workflow can
     pass them to Terraform and the teardown step.

The script is deliberately fail-loud: any HTTP error bubbles up and the
workflow's defensive-teardown step destroys whatever resources may have
been left behind plus the account itself.
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import hmac
import json
import os
import secrets
import sys
import time
import urllib.error
import urllib.request
from typing import Any


def _b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def _b64url_decode_padded(s: str) -> bytes:
    padding = (-len(s)) % 4
    return base64.urlsafe_b64decode(s + "=" * padding)


def post_json(url: str, body: dict[str, Any], headers: dict[str, str]) -> dict[str, Any]:
    payload = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=payload, method="POST")
    req.add_header("Content-Type", "application/vnd.api+json")
    req.add_header("Accept", "application/vnd.api+json")
    for k, v in headers.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            data = resp.read().decode("utf-8")
    except urllib.error.HTTPError as exc:  # surface server error body
        body = exc.read().decode("utf-8", errors="replace")
        raise SystemExit(f"POST {url} failed: {exc.code} {body}") from exc
    if not data:
        return {}
    return json.loads(data)


def decode_jwt_claims(token: str) -> dict[str, Any]:
    # JWTs are header.payload.signature — we only need the payload.
    parts = token.split(".")
    if len(parts) != 3:
        raise SystemExit("malformed JWT in register response")
    return json.loads(_b64url_decode_padded(parts[1]).decode("utf-8"))


def mint_verification_token(user_id: str, secret: str, ttl_seconds: int = 86400) -> str:
    exp = int(time.time() + ttl_seconds)
    payload = f"{user_id}:{exp}".encode("utf-8")
    sig = hmac.new(secret.encode("utf-8"), payload, hashlib.sha256).digest()
    return f"{_b64url(payload)}.{_b64url(sig)}"


def emit_output(key: str, value: str, sensitive: bool = False) -> None:
    target = os.environ.get("GITHUB_OUTPUT")
    if target:
        with open(target, "a", encoding="utf-8") as fh:
            fh.write(f"{key}={value}\n")
    if sensitive:
        # Mask via GH Actions logger so the value doesn't leak in logs.
        print(f"::add-mask::{value}")
    else:
        print(f"{key}={value}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--base-url",
        default=os.environ.get("SMOKE_BASE_URL", "https://app.smplkit.com"),
        help="App-service base URL (no trailing slash). Defaults to production.",
    )
    parser.add_argument(
        "--email-suffix",
        default=os.environ.get("SMOKE_EMAIL_SUFFIX", "smplkit-test.com"),
        help="Domain to use for the random throwaway email. Cannot be an "
        "RFC 2606 reserved TLD (.invalid / .test / .localhost / .example) "
        "because Pydantic EmailStr / email-validator rejects those. The "
        "convention across smplkit's e2e suite is smplkit-test.com.",
    )
    args = parser.parse_args()

    secret = os.environ.get("APP_AUTH_SECRET", "")
    if not secret:
        raise SystemExit("APP_AUTH_SECRET environment variable is required")

    # When SMPLKIT_RATE_LIMIT_BYPASS_TOKEN is set the script sends it as
    # the X-Rate-Limit-Bypass header on every request. This is meant
    # for runs against the local platform where /auth/register's
    # 5/hour limit can interfere with tight CI re-runs. Against
    # production we leave it unset and accept the 5/hour budget — the
    # smoke job only fires on releases, so it's plenty.
    bypass_token = os.environ.get("SMPLKIT_RATE_LIMIT_BYPASS_TOKEN", "")
    extra_headers: dict[str, str] = {}
    if bypass_token:
        extra_headers["X-Rate-Limit-Bypass"] = bypass_token

    nonce = secrets.token_hex(6)
    email = f"smoke-{int(time.time())}-{nonce}@{args.email_suffix}"
    password = secrets.token_urlsafe(20)

    # 1. Register
    reg = post_json(
        f"{args.base_url}/api/v1/auth/register",
        {"email": email, "password": password},
        extra_headers,
    )
    token = reg.get("token") or reg.get("data", {}).get("attributes", {}).get("token")
    if not token:
        raise SystemExit(f"register response missing token: {reg!r}")

    claims = decode_jwt_claims(token)
    user_id = claims.get("user_id") or claims.get("sub_user") or claims.get("uid")
    account_id = claims.get("account_id") or claims.get("account") or claims.get("acc")
    if not user_id or not account_id:
        raise SystemExit(f"could not extract user_id/account_id from JWT claims: {claims!r}")

    # 2. Mint verification token + verify
    verification = mint_verification_token(user_id, secret)
    verify_resp = post_json(
        f"{args.base_url}/api/v1/auth/verify-email",
        {"token": verification},
        extra_headers,
    )
    new_token = verify_resp.get("token") or token  # endpoint optionally returns a fresh token

    # 3. Create API key
    key_headers = dict(extra_headers)
    key_headers["Authorization"] = f"Bearer {new_token}"
    key_resp = post_json(
        f"{args.base_url}/api/v1/api_keys",
        {
            "data": {
                "type": "api_key",
                "attributes": {
                    "name": f"terraform-smoke-{nonce}",
                    "scopes": {},
                },
            }
        },
        key_headers,
    )
    attrs = key_resp.get("data", {}).get("attributes", {})
    api_key = attrs.get("key") or attrs.get("value") or attrs.get("token")
    if not api_key:
        raise SystemExit(f"api_keys response missing key: {key_resp!r}")

    emit_output("SMPLKIT_API_KEY", api_key, sensitive=True)
    emit_output("ACCOUNT_ID", account_id)
    emit_output("USER_ID", user_id)
    emit_output("ACCOUNT_TOKEN", new_token, sensitive=True)
    emit_output("EMAIL", email)
    return 0


if __name__ == "__main__":
    sys.exit(main())
