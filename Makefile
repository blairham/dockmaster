# dockmaster — a k9s-style TUI for Docker.
BINARY      := dockmaster
# ALIAS is dockmaster's short name. It is installed as a symlink to the
# binary, not a second copy.
ALIAS       := dm
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG         := github.com/blairham/dockmaster/internal/version
LDFLAGS     := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
INSTALL_DIR ?= $(HOME)/.local/bin

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build ./dist/dockmaster
	@mkdir -p dist
	go build -ldflags '$(LDFLAGS)' -o dist/$(BINARY) .

.PHONY: install
install: build ## Build, copy to ~/.local/bin, and link the dm alias
	@mkdir -p $(INSTALL_DIR)
	install -m 0755 dist/$(BINARY) $(INSTALL_DIR)/$(BINARY)
	@if [ -e $(INSTALL_DIR)/$(ALIAS) ] && [ ! -L $(INSTALL_DIR)/$(ALIAS) ]; then \
		echo "refusing to replace $(INSTALL_DIR)/$(ALIAS): it exists and is not dockmaster's symlink" >&2; exit 1; \
	fi
	ln -sfn $(BINARY) $(INSTALL_DIR)/$(ALIAS)
	@echo "installed $(INSTALL_DIR)/$(BINARY) and $(INSTALL_DIR)/$(ALIAS) -> $(BINARY)"

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

# DEMO_HOST is the daemon the screenshots are staged on — an empty one, so
# nothing of the developer's own appears. OrbStack's by default.
DEMO_HOST ?= unix://$(HOME)/.orbstack/run/docker.sock
DEMO_SOCK := /tmp/dockmaster-demo/docker.sock

.PHONY: screenshots
screenshots: build ## Render docs/images with VHS on a staged demo daemon
	@command -v vhs >/dev/null || { echo "needs vhs: brew install vhs" >&2; exit 1; }
	@mkdir -p $(dir $(DEMO_SOCK)) && ln -sfn $(patsubst unix://%,%,$(DEMO_HOST)) $(DEMO_SOCK)
	DOCKER_HOST=$(DEMO_HOST) docker compose -f docs/demo/compose.yaml up -d --wait
	DOCKMASTER_DEMO_HOST=unix://$(DEMO_SOCK) DOCKMASTER_CONFIG_DIR=$$(mktemp -d) vhs docs/demo/screenshots.tape; \
		status=$$?; DOCKER_HOST=$(DEMO_HOST) docker compose -f docs/demo/compose.yaml down; exit $$status
