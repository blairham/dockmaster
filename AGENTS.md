# AGENTS.md — dockyard

Guidance for AI coding agents (Claude Code, Cursor, Copilot, Codex, OpenCode, …) working in this repository. This is the **cross-tool single source of truth** — `CLAUDE.md` imports it.

## Project Overview

**dockyard** is a k9s-style terminal UI for Docker: containers, images, volumes, networks, and Compose projects in one navigable frame, with live log tailing, inspect, and the full container lifecycle on the keyboard. On a Colima host it also manages the VMs (profiles) under the daemon.

- Module path: `github.com/blairham/dockyard`
- Go 1.26 (pinned in `.tool-versions`, kept in sync with `go.mod` by a pre-commit hook)
- Built on [`github.com/blairham/tuikit`](https://github.com/blairham/tuikit) — the shared Bubble Tea chrome — plus the Docker Engine API SDK.

**This repo is local-only** — there is no GitHub remote, no CI, and no release pipeline. Do not create one without asking.

⚠️ **tuikit's module path is `github.com/blairham/tuikit`, and it is public** — it needs no `GOPRIVATE` entry.

## Quick Reference

```bash
make help                 # list targets
make build                # ./dist/dockyard
make install              # build + copy to ~/.local/bin, plus a d6d -> dockyard symlink
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
  config/                  config.yaml (k9s-shaped) — load, validate, defaults; flags over it; `dockyard config path|init`
  docker/                  Engine API wrapper — no bubbletea in here
    client.go              dial, daemon metadata, error formatting
    context.go             docker CONTEXT STORE resolution (see below)
    containers.go          list + lifecycle (start/stop/restart/kill/pause/rm)
    images.go              list, history, pull, rm, prune
    volumes.go networks.go list, inspect, rm, prune
    stats.go               the CPU/MEM sampler
    logs.go                follow-mode log streaming with stdcopy demux
    util.go                HumanSize, indentJSON, compose-project folding
  colima/                  colima CLI wrapper — no bubbletea in here either
  engines/                 runtime providers (colima, podman, Docker Desktop, Rancher, OrbStack) — no bubbletea
  tui/
    app.go                 root model, message dispatch, context switching
    keys.go                the key path and the `:` command palette
    actions.go             EVERY mutation funnels through handleAction
    runtimes.go            runtime lifecycle helpers, reconnect after start
    dispatch.go            view stack, sizing, refresh routing
    render.go              Frame assembly, info panel, shortcuts, help
    style/                 palette + the ViewType enum
    views/                 one file per view, all implementing views.View
docs/                      design notes (see Documentation)
```

## Code Conventions

- **`main.go` is the only file in package `main`**, so `go run main.go` builds as well as `go run .`. Helpers go in an `internal/` package.
- **`internal/docker` and `internal/colima` must not import bubbletea.** They return plain values and errors; views wrap them in messages. That boundary is what lets each be tested against the real thing with no TUI in the loop.
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
- **Every daemon request takes its deadline from `Client.RequestContext(def)`**, never a bare `context.WithTimeout`: `--request-timeout` / `requestTimeout` overrides `def` everywhere, and a guard test counts the exceptions (CLI calls only).
- **The CPU/MEM sampler is one blocking request per running container** and is togglable with `<t>` for exactly that reason.

The stats sampler uses `ContainerStats(ctx, id, false)` — the two-sample form — **not** `ContainerStatsOneShot`. One-shot returns a zeroed `PreCPU` block, and the CPU formula against a zero baseline reports numbers in the thousands of percent. `TestLiveStats` asserts a plausible bound to catch a regression back to it.

## Keymap

Digits `0`–`6` switch resources, numbered from 0 as in k9s (`5` is Runtimes: Colima, Podman, Docker Desktop, Rancher Desktop, OrbStack; `6` is Events). Within a view:

| Key | Action | Key | Action |
|---|---|---|---|
| `enter` | drill in (logs / layers / inspect) | `o` | inspect |
| `u` | start ("up") | `x` | stop |
| `R` | restart | `K` | kill (SIGKILL, confirms) |
| `p` | pause / unpause (flips with state) | `s` | shell into the container |
| `a` | toggle stopped/all | `t` | toggle the CPU/MEM poll |
| `z` | volume sizes (slow, off by default) | `P` | prune |
| `ctrl-d` | remove (confirms) | `f` | toggle log follow |
| `H` | health: check, streak, last probes with output | `T` / `D` / `S` | top (processes) / diff (files changed) / full stats |
| `/` | filter (leading `!` negates) | `:` | command palette |
| `ctrl-g` | toggle breadcrumbs (k9s) | `ctrl-e` | toggle the header (k9s) |
| `q` | back out of a drill-in (never quits) | `ctrl-r` | reload, as `r` |
| `[` / `]` | view history back / forward | `-` | toggle to the last view |

Navigation is tuikit's `viewfsm.TranslateNavKey` — `j`/`k`/`h`/`l`, `g`/`G`, `ctrl-f`/`ctrl-b` onto the arrow and page keys — applied **after** the view's own keys, so a view's letter (`l` for logs) wins. In the logs and events streams, keys and the mouse wheel go through tuikit's `tail.HandleScrollKey` (`views/scroll.go`): any scroll pauses follow so new lines stop pulling the view down, and `G` resumes it, as in k9s. The help overlay's GENERAL and NAVIGATION columns are tuikit's shared `chrome.GeneralHelp()` / `NavigationHelp()`; `TestNavigationKeysDoWhatHelpSays` drives every advertised navigation key.

In the projects view the letters run Compose — `u` `compose up -d`, `R` restart, `p` pull, `ctrl-d` `compose down` (confirms) — via the `docker compose` CLI against the files the project's labels record, falling back to per-container actions when those files are not on this machine (`docs/design/compose.md`).

In the runtimes view the same letters act on the machine, where its runtime supports the verb (see `docs/design/runtimes.md`): `n` new machine (the form's Provider field picks the runtime) and `e` edit resources (both a form, `views.RuntimeFormView`, which captures every key but esc), `o` inspect, `u` start, `x` stop, `R` restart, `ctrl-d` delete — the last three confirm, since each takes every container in the VM with it — `s` ssh into the VM, and `enter` connects dockyard to the profile's daemon. See `docs/design/colima.md`.

`c` on a kind or k3d node container (marked ⎈) opens the node view: the containers inside the node's own containerd (every pod), which `docker ps` never shows. They are read with `crictl` over a docker exec; `enter`/`l` logs, `o` inspect, `s` shell, `ctrl-d` remove (exited only, confirmed), `a` show exited (hidden by default). See `docs/design/kubernetes-nodes.md` — in particular why node log streams run under a watcher.

`K` and every `ctrl-d` are uppercase or modified on purpose: the violent operations should not share a keystroke shape with navigation.

`s` shells out to the `docker` CLI via `tea.ExecProcess` rather than driving the SDK's hijacked-stream exec. Interactive sessions need raw-mode TTY handling, window-resize propagation, and signal forwarding; the CLI already does all three correctly. It passes `--host` so an exec always lands on the daemon dockyard is showing, not whatever context the user's shell happens to have.

## Configuration

`config.yaml` is shaped after k9s's (`dockyard:` root, `ui:` and `logger:` blocks) and read from `$DOCKYARD_CONFIG_DIR`, else `$XDG_CONFIG_HOME/dockyard`, else `~/.config/dockyard`. **Only flags set on the command line override it** — `flag.Visit`, never the parsed default — and unknown keys are an error, not ignored. Adding a setting means a key in `config.Config`, its default in `Default()` *and* `Sample` (`TestSampleIsTheDefaults` fails otherwise), and, if it has a flag, a row in `applyFlags`. See `docs/design/config.md`.

## Testing

- `internal/tui/app_test.go` drives the real root model headlessly: feed messages, render, assert. **Assertions strip ANSI first** — lipgloss emits a separate escape pair around *every character* of an underlined span, so a substring check for the help overlay's `RESOURCE` header can never match the raw frame. `render()` strips; `renderStyled()` is for the few assertions that are genuinely about color.
- The views are driven through the app rather than unit-tested in isolation, so the tests cover the wiring (key → action → state → frame) that is where the bugs actually are.
- The colima tests use a fake `Runner` and never touch a VM. `internal/colima/live_test.go` lists the real profiles read-only and is skipped under `-short`. **Never write a test that starts, stops or deletes a real profile** — it is the daemon every other test and the user's own work runs on.
- `TestEmptyFilterDoesNotWedgeTheCursor` pins a bubbles quirk: `SetRows` clamps the cursor to `-1` on an empty row set and never restores it, silently killing every row action for the rest of the session. `setTableRows` exists solely to repair that, and any filter matching nothing reaches it — including an ordinary `docker ps` right after the last container stops.

## Documentation

See [`docs/README.md`](docs/README.md) for the docs index; design notes live in [`docs/design/`](docs/design/). Read the relevant doc before changing the area it covers, and update it in the same commit when a change makes it stale.
