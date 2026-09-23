# dockyard — a k9s-style TUI for Docker.
BINARY      := dockyard
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG         := github.com/blairham/dockyard/internal/version
LDFLAGS     := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
INSTALL_DIR ?= $(HOME)/.local/bin

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build ./dist/dockyard
	@mkdir -p dist
	go build -ldflags '$(LDFLAGS)' -o dist/$(BINARY) .

.PHONY: install
install: build ## Build and copy to ~/.local/bin
	@mkdir -p $(INSTALL_DIR)
	install -m 0755 dist/$(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "installed $(INSTALL_DIR)/$(BINARY)"

.PHONY: run
run: ## Run against the active docker context
	go run . $(ARGS)

.PHONY: test
test: ## Race tests (live-daemon tests skip when no daemon is reachable)
	go test -race ./...

.PHONY: fmt
fmt: ## Format with gofumpt (gci and golines run in the pre-commit hook)
	go tool gofumpt -w .

.PHONY: vet
vet: ## go vet
	go vet ./...

# There is deliberately no `lint` target: golangci-lint runs as a pre-commit
# hook and nowhere else. A by-hand run tells you nothing the commit will not,
# and `--fix` applies govet's fieldalignment reordering (see AGENTS.md).
.PHONY: check
check: fmt vet test ## Format, vet and test

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: clean
clean: ## Remove build output
	rm -rf dist
