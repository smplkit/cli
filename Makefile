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
accept: ## Run acceptance tests against the local platform (ADR-042).
	ACC=1 go test ./acceptance/... -v -timeout 20m -count=1

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
