# smplkit CLI

`smplkit` is the official command-line interface for the [smplkit
platform](https://smplkit.com). It is a thin imperative shell over the
Go SDK's management client — every command maps onto a
`Manage().<Ns>().<Verb>` call.

## Installation

Pre-built binaries for Linux, macOS, and Windows (amd64 + arm64) are
published to [GitHub Releases](https://github.com/smplkit/cli/releases)
on every push to `main`. A multi-arch Docker image is published to
GHCR:

```bash
docker run --rm -v "$HOME/.smplkit:/root/.smplkit:ro" \
  ghcr.io/smplkit/cli:latest --help
```

Or build from source:

```bash
go install github.com/smplkit/cli@latest
```

## Authentication

The CLI delegates credential and endpoint resolution to the Go SDK's
`resolveConfig` chain — it reads exactly what the SDK reads, in the
same precedence (lowest → highest):

1. Defaults (`scheme=https`, `base_domain=smplkit.com`).
2. `~/.smplkit` — INI: `[common]` overlaid by the selected profile.
3. `SMPLKIT_*` environment variables.
4. The global flags below.

A developer who already has `~/.smplkit` configured for SDK work runs
the CLI with zero additional setup.

## Global flags

```
--api-key                       API key
--profile                       ~/.smplkit profile name (default: default)
-e, --env                       environment (required for env-scoped operations)
-o, --output  table|json|yaml   output format (default: table)
    --quiet                     minimal output (identifiers only)
    --no-color                  disable ANSI color
```

## Resources

```
flag           feature flags          (Manage().Flags())
config         configurations         (Manage().Config())
logger         loggers                (Manage().Loggers())
log-group      log groups             (Manage().LogGroups())
env            environments           (Manage().Environments())
service        services               (Manage().Services())
audit forwarder SIEM forwarders       (Manage().Audit().Forwarders())
```

Five universal verbs:

- `list` — paginate (`--limit`, `--all`).
- `get <key>` — fetch one.
- `create <key>` — `New(id, …)` → optional `-f file.json` + scalar
  flags → `Save(ctx)`.
- `set <key>` — read-modify-write: `Get(ctx, id)` → apply scalar/file
  edits → `Save(ctx)`. There is no PATCH — the platform full-replaces
  on PUT (ADR-014).
- `delete <key>` — confirms unless `--yes` / `-y`.

`set --enabled / --disabled / --value / --rules` (flags), `set
--env-value` (configs), and `set --level` (loggers) are env-scoped and
require `--env`.

## Examples

```bash
# List flags
smplkit flag list

# Get one as JSON, pipe into jq
smplkit flag get checkout_v2 -o json | jq .

# Flip a flag on in production
smplkit flag set checkout_v2 --enabled --env production

# Add an item to a configuration
smplkit config set billing --item retry_count=3 --item-type number

# Override the same item for a single environment
smplkit config set billing --env staging --env-value retry_count=1 --item-type number

# Round-trip a forwarder definition
smplkit audit forwarder get siem -o json > siem.json
$EDITOR siem.json
smplkit audit forwarder set siem -f siem.json
```

## Pagination

`list` accepts `--limit` (page size) and `--all` (auto-paginate to
exhaustion). Both map onto the SDK's `WithPageNumber` / `WithPageSize`
list options.

## Errors

The CLI surfaces JSON:API `errors` arrays verbatim from the server
(via the SDK's typed errors). 401 → "set credentials" guidance via
the SDK message; 402 → the entitlement and upgrade path; 404, 409,
422 → typed errors propagated as-is. Exits non-zero on any failure.

## Scope

The CLI manages product resources and the platform topology. Identity,
security, and billing stay in the console; API key minting in
particular is deliberately console-only.

Audit v1 is forwarder CRUD only — the management client's audit
surface is `forwarders.New/Get/List/Delete + Save`. Event reads and
forwarder operations beyond CRUD live on the runtime `AuditClient`,
whose construction has side effects (env registration, buffer flushing,
websocket) a CLI must never trigger. Side-effect-free event reads are
tracked as
[smplkit/audit#…](https://github.com/smplkit/audit/issues) and CLI
follow-up as
[smplkit/cli#…](https://github.com/smplkit/cli/issues) — see issues
filed by the build.

## Development

```
make build     # build the smplkit binary
make test      # unit tests
make check     # build + vet + lint + tests (the CI gate)
make accept    # acceptance tests against the local platform (ADR-042)
```

The acceptance suite requires the smplkit platform running locally
(see [`ci/docker-compose.yml`](ci/docker-compose.yml) for the same
images CI brings up) and a valid `SMPLKIT_API_KEY` for it.

## See also

- ADR-053 — CLI design rationale.
- [`smplkit/go-sdk`](https://github.com/smplkit/go-sdk) — the SDK the
  CLI layers on.
- [`smplkit/terraform-provider-smplkit`](https://github.com/smplkit/terraform-provider-smplkit) —
  sibling tooling using the same management-client pattern.

## License

MIT. See [LICENSE](LICENSE).
