---
description: Build, vet and test dockyard
allowed-tools: Bash(make:*), Bash(go:*)
---

Run the gate and report what failed, if anything:

1. `go build ./...`
2. `go vet ./...`
3. `go test -short -race ./...`

Use `-short` so the live-daemon tests are skipped; they need a running docker
daemon and on a VM-backed one the image listing takes minutes. If the user
explicitly wants them, run `go test -race ./internal/docker/ -run TestLive`
with a generous `-timeout`.

**Do not run `golangci-lint`.** It is a pre-commit hook and runs on commit; a
by-hand run tells you nothing the commit will not, and `--fix` applies govet's
fieldalignment reordering (see `AGENTS.md`).
