# CLAUDE.md

@AGENTS.md

<!-- AGENTS.md is the source of truth for this repo; it is shared with every other
     AI coding tool. Durable project context belongs there, not here. -->

## Claude Code-specific notes

- `/check` runs build, vet and the short test suite.
- The live-daemon tests in `internal/docker` need a reachable docker daemon and self-skip without one. Prefer `go test -short ./...` while iterating — on a slow VM-backed daemon the image listing alone takes minutes.
- ⚠️ Never run `golangci-lint` by hand — there is no `make lint`, it is a pre-commit hook. Besides the reasons in the tree-level agreement, `--fix` here applies `govet`'s `fieldalignment`, which reorders struct fields without rewriting positional composite literals. Use named fields everywhere; see the note in `AGENTS.md`.
