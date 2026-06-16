# smplkit CLI

See `~/.claude/CLAUDE.md` for universal rules (git workflow, testing, code quality, SDK conventions, etc.).

## What this repo is

A Cobra/Viper CLI that layers on the Go SDK's management client. Every
command maps onto `Manage().<Ns>().<Verb>` and nothing else — there is
**no hand-rolled HTTP**, **no runtime `Client`**, and **no use of the
SDK's `internal/generated` packages**. The CLI's ceiling is the SDK's
management surface (ADR-053 §1, §2.4).

## Repository structure

- `main.go` — entrypoint; defers to `cmd/`.
- `cmd/` — one file per noun (`flag.go`, `config.go`, `logger.go`,
  `log_group.go`, `env.go`, `service.go`, `audit.go`) plus `root.go`
  with the global flags and shared helpers.
- `internal/cliconfig/` — global-flag struct and just-in-time
  environment resolution (mirrors the SDK's INI precedence).
- `internal/clientfactory/` — single helper that constructs a
  `smplkit.ManagementClient` from the CLI's globals.
- `internal/output/` — table / JSON / YAML renderers per resource.
- `internal/paginate/` — generic page-walker over the SDK's
  `WithPageNumber` / `WithPageSize` options.
- `internal/values/` — typed value parsing (`--item-type`, `--default`,
  `@file` references).
- `acceptance/` — CLI-binary acceptance tests gated by `ACC=1`, run
  locally via `make accept` against a running ADR-042 platform. In CI
  they run from the `smplkit/e2e` repo against the production platform,
  not in this repo.

## Build / test

```
make build     # produces ./smplkit
make test      # unit tests (no live platform)
make check     # build + vet + lint + tests — the CI gate
make accept    # ACC=1 go test ./acceptance/... against the local platform
```

`make accept` requires the local platform (ADR-042) running and a
`SMPLKIT_API_KEY` valid against it.

## Conventions

- Don't introduce a second SDK dependency. The CLI talks to the
  platform exclusively through `github.com/smplkit/go-sdk/v3`.
- Don't reimplement credential resolution. Pass only explicit flag
  values into `ManagementConfig`; leave everything else empty so the
  SDK's resolver runs.
- Don't reach for the runtime `AuditClient` to add event reads — those
  side effects (env/service registration, buffer thread, websocket)
  must not happen in a CLI. The blocker is tracked in
  `smplkit/audit`; this repo's tracker has the CLI follow-up.
- Every `create` and `set` command accepts `-f file.json` plus scalar
  flags. Scalar flags override file values where both are supplied.
- `set` is read-modify-write end to end. No PATCH semantics anywhere.

## CI

- `ci.yml` — `make check` (build + vet + golangci-lint + unit tests)
  on every push and PR.
- `release.yml` — semantic-release (fix-only convention) decides the
  next version and pushes a tag; GoReleaser then builds signed
  cross-platform archives + checksums and publishes a GitHub release
  and a multi-arch GHCR image. Reuses the existing org-level GPG
  signing secret; introduces no new secrets.

Acceptance does **not** run in this repo's CI. The ephemeral-platform
`acceptance.yml` workflow (and its `ci/` docker-compose + Caddyfile)
was retired in commit 3a59ca8 — CLI acceptance now runs from the
`smplkit/e2e` repo against the production platform. `make accept`
still works locally against a running ADR-042 platform (see Build /
test).

## Pre-launch commit convention

This repo uses the org-wide `fix:`-only convention (see Universal
Rules → SDK commit message lockdown). New work lands on `main` via
`fix:` commits so semantic-release issues patch bumps until Mike
explicitly switches the lock off.
