SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

BINARY := smplkit
COVERPROFILE := coverage.out

##@ Build

.PHONY: build
build: ## Build the smplkit binary.
	go build -o $(BINARY) .

.PHONY: install
install: ## Install the smplkit binary into $$GOBIN.
	go install .

##@ Test

.PHONY: test
test: ## Run unit tests (no live platform).
	go test ./... -count=1

.PHONY: cover
cover: ## Run unit tests with coverage.
	go test ./... -coverprofile=$(COVERPROFILE) -covermode=atomic
	go tool cover -func=$(COVERPROFILE) | tail -1

.PHONY: accept
accept: ## Run acceptance tests (ADR-042). DESTRUCTIVE — uses the isolated [local-acceptance] account.
	@prof="$$(awk '/^\[local-acceptance\]/{p=1;next}/^\[/{p=0}p&&/^[[:space:]]*api_key/{sub(/^[^=]*=[[:space:]]*/,"");print;exit}' $$HOME/.smplkit 2>/dev/null || true)"; \
	base="$${SMPLKIT_BASE_DOMAIN:-localhost}"; \
	if [ -n "$$prof" ]; then \
		key="$$prof"; \
	elif echo "$$base" | grep -qvE 'localhost|127\.0\.0\.1|0\.0\.0\.0'; then \
		key="$${SMPLKIT_API_KEY:-}"; \
	else \
		key=""; \
	fi; \
	if [ -z "$$key" ]; then \
		echo "ERROR: the acceptance suite is DESTRUCTIVE — it deletes the authenticating account's" >&2; \
		echo "  'development' environment to free a managed slot. It does NOT create a throwaway" >&2; \
		echo "  account, so run it ONLY as a dedicated, isolated account — never your dev/preview" >&2; \
		echo "  account. Provision the local one once:" >&2; \
		echo "    python3 ~/projects/.github/platform/seed-acceptance-account.py" >&2; \
		echo "  (writes the [local-acceptance] profile; see ~/projects/.github/docs/local-testing.md)." >&2; \
		echo "  (An ambient SMPLKIT_API_KEY is honored only when SMPLKIT_BASE_DOMAIN targets a remote" >&2; \
		echo "  endpoint — the e2e path — never against the local platform.)" >&2; \
		exit 1; \
	fi; \
	ACC=1 SMPLKIT_ACC_DESTRUCTIVE=1 SMPLKIT_API_KEY="$$key" go test ./acceptance/... -v -timeout 20m -count=1

##@ Lint

.PHONY: vet
vet: ## go vet.
	go vet ./...

.PHONY: lint
lint: ## golangci-lint.
	golangci-lint run ./...

##@ Check

.PHONY: check
check: build vet lint test ## build + vet + lint + tests (the CI gate).

##@ Help

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n"} \
	/^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } \
	/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)
