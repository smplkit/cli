#!/usr/bin/env bash
# wait_for_platform.sh — block until every smplkit service exposes
# /api/liveness via the Caddy reverse proxy at *.localhost.
#
# Used by the acceptance-test workflow after `docker compose up -d`
# brings up the platform: compose returns as soon as the containers
# are running, but the apps inside take a few seconds to bind their
# port and the migration step that ran before this needs every
# service's schema to be live. We poll the liveness endpoint instead
# of `docker compose ps` health because the latter is image-dependent
# and the four product images don't ship healthchecks today.

set -euo pipefail

SERVICES=(app config flags logging audit)
TIMEOUT_SECONDS=${TIMEOUT_SECONDS:-180}
SLEEP_SECONDS=${SLEEP_SECONDS:-2}

deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))

for svc in "${SERVICES[@]}"; do
  url="http://${svc}.localhost/api/liveness"
  echo "Waiting for ${svc} (${url}) ..."
  while true; do
    if curl --silent --show-error --fail --max-time 3 "${url}" >/dev/null 2>&1; then
      echo "  ${svc} is live"
      break
    fi
    if (( $(date +%s) >= deadline )); then
      echo "::error::timed out waiting for ${svc} at ${url}"
      echo "Last response:"
      curl -v --max-time 3 "${url}" || true
      echo "---"
      echo "docker compose ps:"
      docker compose ls
      exit 1
    fi
    sleep "${SLEEP_SECONDS}"
  done
done

echo "All smplkit services are live."
