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

# Where `make sandbox` puts the state, configuration, and sockets of a second
# Feat. Keep an override short: the daemon socket resolves below this path, and
# macOS allows a socket path 103 bytes long.
SANDBOX ?= /tmp/feat-sandbox-$(shell id -u)

# XDG_CONFIG_HOME and XDG_DATA_HOME each get a `feat` component appended.
# FEAT_RUNTIME_DIR is taken as given, and moves the daemon socket, the ownership
# lock, the endpoint record, and the tmux socket together — an override of the
# socket alone would separate it from the lock that proves who owns it (ADR-027).
SANDBOX_ENV = XDG_CONFIG_HOME=$(SANDBOX)/config \
              XDG_DATA_HOME=$(SANDBOX)/state \
              FEAT_RUNTIME_DIR=$(SANDBOX)/run

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

.PHONY: sandbox
sandbox: build ## Run the built binary on throwaway state (ARGS="doctor")
# A second Feat on this host, owning nothing the Feat you use every day owns.
#
# It runs on the host rather than in a container because that is what Feat is:
# worktrees on the host filesystem, its own tmux server, and Compose against the
# host Docker daemon. A control plane inside a container would write host paths
# it cannot see into the generated override, and every task mount would resolve
# somewhere else.
#
# Nothing here creates the three directories. Feat creates each on first use, so
# a fresh sandbox exercises the same first-run path a new install takes, and
# removing it returns to that state.
#
#   make sandbox                     # the dashboard, on sandbox state
#   make sandbox ARGS="doctor"
#   make sandbox ARGS="project init"
#
# The daemon the dashboard starts inherits this environment, so its projects,
# its tasks, and its terminals are its own.
	@$(SANDBOX_ENV) $(BIN_DIR)/$(BINARY) $(ARGS)

.PHONY: sandbox-clean
sandbox-clean: build ## Stop the sandbox daemon and remove its directory
# The guard is here because SANDBOX is an override and the next line is an rm.
	@case "$(SANDBOX)" in \
		/*/*) ;; \
		*) echo "SANDBOX must be absolute and below a top-level directory, but is '$(SANDBOX)'"; exit 1 ;; \
	esac
# The daemon stops first: removing the directory under a running one leaves it
# writing to files that no longer have a name.
	@$(SANDBOX_ENV) $(BIN_DIR)/$(BINARY) daemon stop >/dev/null 2>&1 || true
	@rm -rf $(SANDBOX)
	@echo "removed $(SANDBOX)"
# What this does not remove is what a sandbox task created outside that
# directory: its worktrees, its containers, its volumes, and its tmux sessions.
# Those are `feat task cleanup`'s to resolve, and it resolves the resources a
# task owns rather than matching on a name. Run it before this, not after — the
# record of what a task owns lived in the directory now gone.
	@echo "task worktrees, containers, volumes, and tmux sessions are not removed:"
	@echo "  run 'make sandbox ARGS=\"task cleanup <task>\"' before this target"

.PHONY: test
test: ## Run unit tests with the race detector
	go test -race ./...

# The tools `make check` demands of the machine it runs on. A demanded tool that
# is missing or unanswering fails the run instead of skipping it, because a
# skipped package still prints "ok" and the gate then reports green having
# proved nothing (G6-05).
#
# claude is demandable and is deliberately not demanded: those tests need an
# authenticated installation and spend a model call, so a run that wants them
# asks for them.
#
# It is an override so that a machine short of a tool can say so out loud —
# `make check INTEGRATION_TOOLS=git,tmux` on a laptop with Docker stopped — and
# so that what a run proved is a value somebody typed rather than a property of
# whatever happened to be installed.
INTEGRATION_TOOLS ?= git,docker,tmux

.PHONY: test-real
test-real: ## Run opt-in integration tests against installed real tools
# -count=1 because these tests assert about tools outside the process. Go can
# only invalidate its cache on inputs it can see, so an uninstalled tmux, a
# stopped Docker, or a notification the platform swallowed would all replay a
# previous PASS unchanged (ADR-035 evidence 13).
	FEAT_INTEGRATION=1 FEAT_INTEGRATION_REQUIRE=$(INTEGRATION_TOOLS) \
		go test -race -count=1 -run 'TestBinary|TestReal' ./...

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
	go test ./internal/runtime/compose -run 'TestTheGenerated' -update
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
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
