# Development commands for Feat.
#
# `make check` runs everything CI runs. CLAUDE.md requires formatting, lint,
# tests, and build to pass before a slice is declared complete.

BINARY := feat
MODULE := github.com/ma8el/feat
CMD    := ./cmd/feat

BIN_DIR      := bin
GOLANGCI     := $(BIN_DIR)/golangci-lint
LINT_VERSION := $(shell cat .golangci-version)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X '$(MODULE)/internal/version.version=$(VERSION)' \
           -X '$(MODULE)/internal/version.commit=$(COMMIT)' \
           -X '$(MODULE)/internal/version.date=$(DATE)'

.DEFAULT_GOAL := help

.PHONY: check
check: tidy-check fmt-check lint test test-real build ## Run everything CI runs

.PHONY: build
build: ## Build the feat binary into bin/
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

.PHONY: run
run: build ## Build and open the dashboard
	@$(BIN_DIR)/$(BINARY)

.PHONY: test
test: ## Run unit tests with the race detector
	go test -race ./...

.PHONY: test-real
test-real: ## Run opt-in integration tests against installed real tools
# -count=1 because these tests assert about tools outside the process. Go can
# only invalidate its cache on inputs it can see, so an uninstalled tmux, a
# stopped Docker, or a notification the platform swallowed would all replay a
# previous PASS unchanged (ADR-035 evidence 13).
	FEAT_INTEGRATION=1 go test -race -count=1 -run 'TestBinary|TestReal' ./...

.PHONY: lint
lint: $(GOLANGCI) ## Run golangci-lint, including the architectural depguard rules
	$(GOLANGCI) run ./...

.PHONY: fmt
fmt: $(GOLANGCI) ## Format the tree
	$(GOLANGCI) fmt

.PHONY: fmt-check
fmt-check: $(GOLANGCI) ## Report formatting differences without writing
	$(GOLANGCI) fmt --diff

.PHONY: tidy
tidy: ## Tidy the module requirements
	go mod tidy

.PHONY: tidy-check
tidy-check: ## Fail when go.mod or go.sum is untidy
	go mod tidy -diff

.PHONY: golden
golden: ## Rewrite golden test files
	go test ./internal/cli -run TestCommandSurface -update
	go test ./internal/store/fs -run TestStoredFormat -update
	go test ./internal/execution/compose -run 'TestTheGeneratedOverride' -update
	go test ./internal/api -update

.PHONY: tools
tools: $(GOLANGCI) ## Install the pinned developer tools into bin/

# The binary is rebuilt whenever the pinned version changes. Installing with an
# explicit @version keeps the linter's dependencies out of the module graph, so
# `go install github.com/ma8el/feat/cmd/feat@latest` stays clean for users.
$(GOLANGCI): .golangci-version
	@mkdir -p $(BIN_DIR)
	GOBIN=$(CURDIR)/$(BIN_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)

.PHONY: clean
clean: ## Remove build output and installed tools
	rm -rf $(BIN_DIR)

.PHONY: help
help: ## List the available commands
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
