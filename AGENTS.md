# AGENTS.md — dockyard

Guidance for AI coding agents (Claude Code, Cursor, Copilot, Codex, OpenCode, …) working in this repository. This is the **cross-tool single source of truth** — `CLAUDE.md` imports it.

## Project Overview

**dockyard** is a k9s-style terminal UI for Docker: containers, images, volumes, networks, and Compose projects in one navigable frame, with live log tailing, inspect, and the full container lifecycle on the keyboard.

- Module path: `github.com/blairham/dockyard`
- Go 1.26 (pinned in `.tool-versions`, kept in sync with `go.mod` by a pre-commit hook)
- Built on [`github.com/blairham/tuikit`](https://github.com/blairham/tuikit) — the shared Bubble Tea chrome — plus the Docker Engine API SDK.

**This repo is local-only** — there is no GitHub remote, no CI, and no release pipeline. Do not create one without asking.

⚠️ **tuikit's module path is `github.com/blairham/tuikit`, and it is public** — it needs no `GOPRIVATE` entry.

## Quick Reference

```bash
make help                 # list targets
make build                # ./dist/dockyard
make install              # build + copy to ~/.local/bin
make run ARGS='--readonly'

make check                # fmt + vet + test
make test                 # go test -race ./...
go test -short ./...      # skip the live-daemon tests (see below)

go run . --context colima # run against a specific docker context
```

**`go test -short` matters here.** `internal/docker/live_test.go` talks to a real daemon; it self-skips when none is reachable, but on a slow VM-backed daemon the image listing alone can take minutes. `-short` skips those and leaves the whole pure-logic suite, which needs no docker at all.

**Never run `golangci-lint` by hand.** There is no `make lint` target — it runs as a pre-commit hook and nowhere else, so a failing lint fails your commit and you fix it and commit again. That is the only lint output to read.

⚠️ **`fieldalignment -fix` reorders struct fields and does NOT rewrite positional composite literals to match.** It has already silently swapped two fields of a test table here, turning every case into a type error. `govet`'s `fieldalignment` is enabled in `.golangci.yml`, so `golangci-lint run --fix` applies it — one more reason not to run the linter by hand. Write struct literals with **named fields** — the test tables all do, and the comment in `internal/docker/docker_test.go` says why.

## Project Structure

```
main.go                    flag parsing, daemon dial, program start
internal/
  docker/                  Engine API wrapper — no bubbletea in here
    client.go              dial, daemon metadata, error formatting
    context.go             docker CONTEXT STORE resolution (see below)
    containers.go          list + lifecycle (start/stop/restart/kill/pause/rm)
    images.go              list, history, pull, rm, prune
    volumes.go networks.go list, inspect, rm, prune
    stats.go               the CPU/MEM sampler
    logs.go                follow-mode log streaming with stdcopy demux
    util.go                HumanSize, indentJSON, compose-project folding
  tui/
    app.go                 root model, message dispatch, context switching
    keys.go                the key path and the `:` command palette
    actions.go             EVERY mutation funnels through handleAction
    dispatch.go            view stack, sizing, refresh routing
    render.go              Frame assembly, info panel, shortcuts, help
    style/                 palette + the ViewType enum
    views/                 one file per view, all implementing views.View
docs/                      design notes (see Documentation)
```

## Code Conventions

- **`internal/docker` must not import bubbletea.** It returns plain values and errors; views wrap them in messages. That boundary is what lets the docker layer be tested against a real daemon with no TUI in the loop.
- **Views never mutate.** `View.HandleKey` returns an `(action, param)` pair; `App.handleAction` carries it out. This is not ceremony — it is why `--readonly` is a single map lookup in one function rather than a flag threaded through nine views. Adding a mutation to a view directly defeats it.
- **Every destructive action goes through `chrome.Confirm`.** No exceptions, including ones that "obviously" cannot lose data.
- goimports local-prefix is `github.com/blairham/dockyard`; internal imports get their own group.
- golines max-len 120. misspell locale US.
- **The lint config carries no adoption-relaxation block.** dockyard started green at the canonical thresholds and should stay there — a genuinely flat dispatch function takes a targeted `//nolint` naming the reason, not a globally disabled linter.
- MIT, `LICENSE` in Blair Hamilton's name.

## The docker context store — the thing that is easy to get wrong

The Go SDK's `client.FromEnv` reads **only `DOCKER_HOST`**. It does not read docker *contexts*. On any machine using Colima, Rancher Desktop, Podman, or a remote host — where the endpoint lives in the context store and nowhere else — an SDK program therefore dials `/var/run/docker.sock` and reports "is the docker daemon running?" while `docker ps` two lines earlier worked fine.

`internal/docker/context.go` closes that gap, following the CLI's own precedence:

1. `--host`
2. `$DOCKER_HOST`
3. `$DOCKER_CONTEXT`, looked up in the store
4. `currentContext` from `~/.docker/config.json`, looked up in the store
5. the platform default socket

The store lives at `~/.docker/contexts/meta/<hex sha256 of the context name>/meta.json`. `TestContextDigestMatchesDockerCLI` pins that digest scheme against a real value, because if it ever drifts the failure is silent: dockyard falls back to the default socket and declares a live daemon dead.

## Daemon calls are slow, and the design assumes it

This is not a theoretical concern. On a Colima host measured during development:

| Call | Time |
|---|---|
| `_ping` | instant |
| `docker ps` (7 containers) | ~6 s |
| `docker images` (651 images) | **did not complete — timed out at 300 s, twice** |

Both were as slow through the CLI as through the SDK, so it is the daemon, not the client. Three consequences are baked in and must not be undone:

- **Every list view single-flights its refresh** (`inFlight` guard). The 3-second poll skips entirely while a request is outstanding. Without it, a 6-second `ps` on a 3-second tick stacks requests until the socket refuses connections.
- **The poll is 3 s, not k9s's 2 s**, and `imageListTimeout` is 5 minutes while everything else is 20–60 s. On the host above even that is not enough, and the images view surfaces a timeout with a hint rather than spinning forever — a daemon that cannot list its own images in five minutes is a daemon problem, and dockyard says so instead of hiding it.
- **The CPU/MEM sampler is one blocking request per running container** and is togglable with `<t>` for exactly that reason.

The stats sampler uses `ContainerStats(ctx, id, false)` — the two-sample form — **not** `ContainerStatsOneShot`. One-shot returns a zeroed `PreCPU` block, and the CPU formula against a zero baseline reports numbers in the thousands of percent. `TestLiveStats` asserts a plausible bound to catch a regression back to it.

## Keymap

Digits `1`–`5` switch resources. Within a view:

| Key | Action | Key | Action |
|---|---|---|---|
| `enter` | drill in (logs / layers / inspect) | `o` | inspect |
| `s` | start | `x` | stop |
| `R` | restart | `K` | kill (SIGKILL, confirms) |
| `p` | pause / unpause (flips with state) | `e` | shell into the container |
| `a` | toggle stopped/all | `t` | toggle the CPU/MEM poll |
| `z` | volume sizes (slow, off by default) | `P` | prune |
| `ctrl-d` | remove (confirms) | `f` | toggle log follow |
| `/` | filter (leading `!` negates) | `:` | command palette |

`K` and every `ctrl-d` are uppercase or modified on purpose: the violent operations should not share a keystroke shape with navigation.

`e` shells out to the `docker` CLI via `tea.ExecProcess` rather than driving the SDK's hijacked-stream exec. Interactive sessions need raw-mode TTY handling, window-resize propagation, and signal forwarding; the CLI already does all three correctly. It passes `--host` so an exec always lands on the daemon dockyard is showing, not whatever context the user's shell happens to have.

## Testing

- `internal/tui/app_test.go` drives the real root model headlessly: feed messages, render, assert. **Assertions strip ANSI first** — lipgloss emits a separate escape pair around *every character* of an underlined span, so a substring check for the help overlay's `RESOURCE` header can never match the raw frame. `render()` strips; `renderStyled()` is for the few assertions that are genuinely about color.
- The views are driven through the app rather than unit-tested in isolation, so the tests cover the wiring (key → action → state → frame) that is where the bugs actually are.
- `TestEmptyFilterDoesNotWedgeTheCursor` pins a bubbles quirk: `SetRows` clamps the cursor to `-1` on an empty row set and never restores it, silently killing every row action for the rest of the session. `setTableRows` exists solely to repair that, and any filter matching nothing reaches it — including an ordinary `docker ps` right after the last container stops.

## Documentation

See [`docs/README.md`](docs/README.md) for the docs index; design notes live in [`docs/design/`](docs/design/). Read the relevant doc before changing the area it covers, and update it in the same commit when a change makes it stale.
