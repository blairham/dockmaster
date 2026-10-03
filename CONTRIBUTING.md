# Contributing to dockmaster

Thanks for looking. Issues, bug reports and pull requests are all welcome.

## Before you open a PR

```sh
pre-commit install   # once per clone: formatting, golangci-lint, secrets, YAML, license headers
go test -short ./... # the whole pure-logic suite; needs no docker
```

golangci-lint runs as the commit hook and in CI, not as a make target; a
failing hook fails the commit, and you fix it and commit again. `make fmt`
applies gofumpt.

`go test ./...` without `-short` also runs `internal/docker/live_test.go`
against whatever daemon your docker context points at. It only reads, and it
skips when no daemon answers, but on a slow VM-backed daemon listing images
can take minutes.

## The shape of a change

[`AGENTS.md`](AGENTS.md) is the architecture guide. The rules that most often
decide a review:

- **Views never mutate.** A view's `HandleKey` returns an `(action, param)`
  pair and `App.handleAction` carries it out. A new action that changes
  daemon state goes in the `mutating` map, which is all `--readonly` checks.
- **Every destructive action asks first**, through the confirm bar.
- **`internal/docker`, `internal/colima` and `internal/engines` do not import
  Bubble Tea.** They return values and errors, which is what lets them be
  tested without the TUI.
- **Every daemon request takes its deadline from `Client.RequestContext`**,
  so `--request-timeout` applies everywhere.
- **Styles are derived after the skin loads**, never in a package-level
  declaration — `TestNoPackageStyleSkipsTheSkin` enforces it.
- Struct literals use **named fields**; the linter's `fieldalignment` fix
  reorders fields.

Shared UI chrome — the header, crumbs, help, tables, log tail — lives in
[tuikit](https://github.com/blairham/tuikit). A change to how those behave
belongs there, not here.

## Tests

- Tests drive the real app headlessly: send keys and messages, render, assert
  (`internal/tui/app_test.go`). Assertions strip ANSI first.
- Tests must never touch real user state — use `t.TempDir()` and `t.Setenv`
  for the config and state directories.
- **Never write a test that starts, stops or deletes a real container, image,
  volume or VM.** It is the daemon everything else on the machine runs on.

## The Contributor License Agreement

Contributions require a signed CLA; the text is in [`CLA.md`](CLA.md).

**Why.** The project may need to offer different licensing terms in future.
That is only possible if one party can license the whole work, and copyright
in a contribution stays with its author unless licensed onward.

The CLA does **not** take your copyright. You keep it; you grant a license
broad enough to include sublicensing, and you affirm the work is your own —
including that no employer holds rights to it.

## Commits and PRs

- Prefix the subject with the area it touches (`containers:`, `logs:`,
  `config:`, `docs:`, `ci:`).
- Explain the **why** in the commit message. The diff already says what.
- One change per PR, and put `Closes #N` in the PR body.
- Commits must be signed.
- Every `.go` file carries the two-line SPDX header (`Apache-2.0`); the
  pre-commit hook fails without it.

## Releasing

Maintainers only. Tag a signed `vX.Y.Z` on `main`; the release workflow
builds the binaries, signs `checksums.txt` with cosign, publishes the GitHub
release with SLSA build provenance attached, and updates the formula in
[blairham/homebrew-tap](https://github.com/blairham/homebrew-tap).
