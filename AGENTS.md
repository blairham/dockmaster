# AGENTS.md — dockmaster

Guidance for AI coding agents (Claude Code, Cursor, Copilot, Codex, OpenCode, …) working in this repository. This is the **cross-tool single source of truth** — `CLAUDE.md` imports it.

## Project Overview

**dockmaster** is a k9s-style terminal UI for Docker: containers, images, volumes, networks, and Compose projects in one navigable frame, with live log tailing, inspect, and the full container lifecycle on the keyboard. On a Colima host it also manages the VMs (profiles) under the daemon.

- Module path: `github.com/blairham/dockmaster`
- Go 1.26 (pinned in `.tool-versions`, kept in sync with `go.mod` by a pre-commit hook)
- Built on [`github.com/blairham/tuikit`](https://github.com/blairham/tuikit) — the shared Bubble Tea chrome — plus the Docker Engine API client (`github.com/moby/moby/client` and `.../api`).

**Hosted at `github.com/blairham/dockmaster`**, Apache-2.0. CI (`.github/workflows/ci.yml`) runs pre-commit, `go test -race -short ./...` and a darwin/linux cross-compile; CodeQL and OpenSSF Scorecard run alongside. A signed `v*` tag releases through GoReleaser (`.github/workflows/release.yml`): darwin/linux archives, a cosign-signed `checksums.txt`, and the `dockmaster` formula (with the `dm` symlink) in `blairham/homebrew-tap`, which needs the `HOMEBREW_TAP_TOKEN` secret. Releases start at `v0.0.0`.

⚠️ **tuikit's module path is `github.com/blairham/tuikit`, and it is public** — it needs no `GOPRIVATE` entry.

## Quick Reference

```bash
make help                 # list targets
make build                # ./dist/dockmaster
make install              # build + copy to ~/.local/bin, plus a dm -> dockmaster symlink
make run ARGS='--readonly'
make screenshots          # re-render docs/images with VHS on a staged demo daemon (DEMO_HOST=...)

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
  config/                  config.yaml (k9s-shaped) — load, validate, defaults; flags over it; `dockmaster config path|init`
  docker/                  Engine API wrapper — no bubbletea in here
    client.go              dial, daemon metadata, error formatting
    context.go             docker CONTEXT STORE resolution (see below)
    containers.go          list + lifecycle (start/stop/restart/kill/pause/rm)
    images.go              list, history, pull, rm, prune
    volumes.go networks.go list, inspect, rm, prune
    volbrowse.go           a volume's files, through a read-only, no-network helper (docs/design/volume-browser.md)
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
- goimports local-prefix is `github.com/blairham/dockmaster`; internal imports get their own group.
- golines max-len 120. misspell locale US.
- **The lint config carries no adoption-relaxation block.** dockmaster started green at the canonical thresholds and should stay there — a genuinely flat dispatch function takes a targeted `//nolint` naming the reason, not a globally disabled linter.
- Apache-2.0, `LICENSE` and `NOTICE` in Blair Hamilton's name. Every `.go` file starts with the two-line SPDX header; contributions need the CLA (`CLA.md`).

## The docker context store — the thing that is easy to get wrong

The Go SDK's `client.FromEnv` reads **only `DOCKER_HOST`**. It does not read docker *contexts*. On any machine using Colima, Rancher Desktop, Podman, or a remote host — where the endpoint lives in the context store and nowhere else — an SDK program therefore dials `/var/run/docker.sock` and reports "is the docker daemon running?" while `docker ps` two lines earlier worked fine.

`internal/docker/context.go` closes that gap, following the CLI's own precedence:

1. `--host`
2. `$DOCKER_HOST`
3. `$DOCKER_CONTEXT`, looked up in the store
4. `currentContext` from `~/.docker/config.json`, looked up in the store
5. the platform default socket

The store lives at `~/.docker/contexts/meta/<hex sha256 of the context name>/meta.json`. `TestContextDigestMatchesDockerCLI` pins that digest scheme against a real value, because if it ever drifts the failure is silent: dockmaster falls back to the default socket and declares a live daemon dead.

## Daemon calls are slow, and the design assumes it

This is not a theoretical concern. On a Colima host measured during development:

| Call | Time |
|---|---|
| `_ping` | instant |
| `docker ps` (7 containers) | ~6 s |
| `docker images` (651 images) | **did not complete — timed out at 300 s, twice** |

Both were as slow through the CLI as through the SDK, so it is the daemon, not the client. Three consequences are baked in and must not be undone:

- **Every list view single-flights its refresh** (`inFlight` guard). The 3-second poll skips entirely while a request is outstanding. Without it, a 6-second `ps` on a 3-second tick stacks requests until the socket refuses connections.
- **The poll is 3 s, not k9s's 2 s**, and `imageListTimeout` is 5 minutes while everything else is 20–60 s. On the host above even that is not enough, and the images view surfaces a timeout with a hint rather than spinning forever — a daemon that cannot list its own images in five minutes is a daemon problem, and dockmaster says so instead of hiding it.
- **Every daemon request takes its deadline from `Client.RequestContext(def)`**, never a bare `context.WithTimeout`: `--request-timeout` / `requestTimeout` overrides `def` everywhere, and a guard test counts the exceptions (CLI calls only).
- **The CPU/MEM sampler is one blocking request per running container** and is togglable with `<t>` for exactly that reason.

The stats sampler asks for the two-sample form — `ContainerStatsOptions{Stream: false, IncludePreviousSample: true}` — **not** one-shot, which is what the zero value sends. One-shot returns a zeroed `PreCPU` block, and the CPU formula against a zero baseline reports numbers in the thousands of percent. `TestLiveStats` asserts a plausible bound to catch a regression back to it.

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
| `ctrl-d` | remove (confirms) | `d` / `y` | inspect, as `o` (k9s's describe / yaml) |
| `H` | health: check, streak, last probes with output | `T` / `D` / `S` | top (processes) / diff (files changed) / full stats |
| `/` | filter (leading `!` negates) | `:` | command palette |
| `ctrl-g` | toggle breadcrumbs (k9s) | `ctrl-e` | toggle the header (k9s) |
| `q` | back out of a drill-in (never quits) | `ctrl-r` | reload, as `r` |
| `[` / `]` | view history back / forward | `-` | toggle to the last view |

Navigation is tuikit's `viewfsm.TranslateNavKey` — `j`/`k`/`h`/`l`, `g`/`G`, `ctrl-f`/`ctrl-b` onto the arrow and page keys — applied **after** the view's own keys, so a view's letter (`l` for logs) wins. In a log view the digits pick a time range (`0` the usual backlog, `1`–`5` the last 1m–1h) rather than switching views — a view claims them by implementing `views.DigitClaimer`. The log keys are k9s's (#3): `s` toggles autoscroll, `t` timestamps, `w` wraps, `f` hides header and crumbs until the log is left, `c` / `ctrl-s` copy / save what is shown (filtered, styling stripped; saves go to `$XDG_STATE_HOME/dockmaster/logs`), `shift-c` clears, `m` marks (a stamped rule appended to the buffer, k9s's mark — a tuikit `tail` marker, so every filter lets it through). Help over a log swaps the CONTAINER column for LOGS. `d` and `y`, k9s's describe and yaml, fall back to `o` in any view that does not bind them (`App.activeViewHandleKey`). Where k9s and Docker mean different things the key stays dockmaster's: `p` pause, `a` show stopped, `ctrl-k` Kubernetes containers (k9s's kill — `K` kills here, after a confirm). The events stream still toggles follow with `f`: that binding is tuikit's `tail.HandleScrollKey`, shared with every app on the chrome. `aliases.yaml` (`config.LoadAliases`, checked by `tui.ValidateAliases` before the UI starts) maps `:` names to command lines; `dispatchCommand` expands them first, and a view command takes a `/filter` argument. `hotkeys.yaml` (`config.LoadHotKeys`, validated by `tui.HotKeys`) binds keys to `:` commands; `handleKey` asks them after the view and table keys, before navigation, so a view's own key wins. `plugins.yaml` (`config.LoadPlugins`, validated by `tui.Plugins`) runs the user's commands on the selected row (`tui/plugins.go`): `$VAR`s in args are expanded by dockmaster from `pluginVars`, an `override` plugin is asked before the view's keys, the rest after. Skins are k9s's (`ui.skin`, `DOCKMASTER_SKIN`, `--invert`; `docs/design/config.md`): ⚠️ every package-level style must be derived in `style.apply` or a view's `applyTheme` (registered with `style.OnBase`), never in its declaration — a declaration runs before the skin loads. `TestNoPackageStyleSkipsTheSkin` enforces it, and tables repaint with `tktable.FixRows`, never `FixSelectedRow`, whose colors are fixed. `enter` on a volume browses it (`o` inspects): the browser walks directories in place and claims `esc`/`q` through `views.Backer` until it is back at the volume's root. Every table view shares three tuikit features through `internal/tui/table.go` and `views/{table_rows,sort,marks}.go`: `ctrl+s` saves the table (`table.PlainText` into `chrome.SaveDump`, under `$XDG_STATE_HOME/dockmaster/dumps`, and `:sd` — `views.DumpsView` — lists both that and `logs/`, reads a save back in the plain viewer, and deletes one after a confirm, refusing any path outside those two directories); `shift+←/→` / `shift+↑/↓` sort by column and direction (`table.Sorter`, off until first pressed so each view keeps its own order, with ages and sizes compared by what they measure); and in the containers, images, volumes and networks views `space` / `ctrl+space` / `ctrl+\` mark rows (`table.Marks`), after which lifecycle and remove keys act on every marked row, one confirm for the destructive ones. ⚠️ A view's `Selected` indexes its `visible` slice by cursor, so `sortRows` reorders `visible` with the rows — sorting the rows alone would act on the wrong container. Marks key on IDs, not the name cell, which is truncated and could match two containers. In the logs and events streams, keys and the mouse wheel go through tuikit's `tail.HandleScrollKey` (`views/scroll.go`): any scroll pauses follow so new lines stop pulling the view down, and `G` resumes it, as in k9s. The help overlay's GENERAL and NAVIGATION columns are tuikit's shared `chrome.GeneralHelp()` / `NavigationHelp()`; `TestNavigationKeysDoWhatHelpSays` drives every advertised navigation key.

In the projects view `l` opens the project's merged log (`docker.StreamProjectLogs`: `MergeLogs` fans in every member's stream, tags each line with `LogLine.Source`, and follows `start` events so containers started later join from that moment; `views.NewProjectLogsView` prefixes and aligns them, and its container-only keys are inert). The other letters run Compose — `u` `compose up -d`, `e` edit the compose files in `$EDITOR` then `up -d` if they changed, `R` restart, `p` pull, `ctrl-d` `compose down` (confirms) — via the `docker compose` CLI against the files the project's labels record, falling back to per-container actions when those files are not on this machine (`docs/design/compose.md`).

In the runtimes view the same letters act on the machine, where its runtime supports the verb (see `docs/design/runtimes.md`): `n` new machine (the form's Provider field picks the runtime) and `e` edit resources (both a form, `views.RuntimeFormView`, which captures every key but esc), `o` inspect, `u` start, `x` stop, `R` restart, `ctrl-d` delete — the last three confirm, since each takes every container in the VM with it — `s` ssh into the VM, `K` manages a kind cluster on the machine's daemon (after a confirm): stops or starts the one there, or creates one — control-plane + worker plus `kind-registry` on `localhost:5001`, the user's own setup — via `internal/kind` and `docker.Clusters`/`EnsureRegistry`/`WireRegistry`. The registry stays its own container (it outlives the cluster) but is treated as part of it: `K` stops and starts it with the nodes — stopping only with the last kind cluster using it — it is marked ⎈ in the containers list, and the node view shows where to push. It never switches a runtime's built-in Kubernetes, and refuses where one is on or the machine is stopped. `enter` connects dockmaster to the profile's daemon. See `docs/design/colima.md`.

`n` on a kind or k3d node container (marked ⎈) opens the node view (`c` is k9s's copy key): the containers inside the node's own containerd (every pod), which `docker ps` never shows. They are read with `crictl` over a docker exec; `enter`/`l` logs, `o` inspect, `s` shell, `ctrl-d` remove (exited only, confirmed), `a` show exited (hidden by default). See `docs/design/kubernetes-nodes.md` — in particular why node log streams run under a watcher.

`u` on an image opens the run form (`views.RunFormView`): name, ports, env, volumes, command, `--rm`, each list space-separated as after `-p`/`-e`/`-v`. It is checked with `docker.RunConfig` — the same code that builds the create request — so a bad port is reported in the form; a daemon failure lands back in the form, and success opens the new container's logs. A container that is created but will not start is removed. The runtime and run forms share their field machinery in `views/form.go`.

`C` on a container is `docker cp` either way (`views.CopyFormView`): the archive handling is moby/go-archive, the docker CLI's own, so directory-versus-contents and missing-destination rules match `docker cp` — checked against the real CLI on the same input. Copying in changes the container and is gated by `--readonly`; copying out is not. A local path takes `~` and is relative to where dockmaster was started.

`e` on a container edits it in place (`views.EditFormView`, `docker.Edit`): CPU and memory limits and restart policy via `docker update`, the name via `docker rename` — no restart. The form opens on the container's current settings. A memory change always carries a swap value: the daemon refuses memory above the existing memory+swap limit ("update the memoryswap at the same time"), so `docker.EditUpdate` keeps the swap headroom the container had (unlimited stays unlimited; no limit before gets docker run's 2×). Checked live.

`K` and every `ctrl-d` are uppercase or modified on purpose: the violent operations should not share a keystroke shape with navigation.

`s` shells out to the `docker` CLI via `tea.ExecProcess` rather than driving the SDK's hijacked-stream exec. Interactive sessions need raw-mode TTY handling, window-resize propagation, and signal forwarding; the CLI already does all three correctly. It passes `--host` so an exec always lands on the daemon dockmaster is showing, not whatever context the user's shell happens to have.

## Configuration

`config.yaml` is shaped after k9s's (`dockmaster:` root, `ui:` and `logger:` blocks) and read from `$DOCKMASTER_CONFIG_DIR`, else `$XDG_CONFIG_HOME/dockmaster`, else `~/.config/dockmaster`. **Only flags set on the command line override it** — `flag.Visit`, never the parsed default — and unknown keys are an error, not ignored. Adding a setting means a key in `config.Config`, its default in `Default()` *and* `Sample` (`TestSampleIsTheDefaults` fails otherwise), and, if it has a flag, a row in `applyFlags`. See `docs/design/config.md`.

## Testing

- `internal/tui/app_test.go` drives the real root model headlessly: feed messages, render, assert. **Assertions strip ANSI first** — lipgloss emits a separate escape pair around *every character* of an underlined span, so a substring check for the help overlay's `RESOURCE` header can never match the raw frame. `render()` strips; `renderStyled()` is for the few assertions that are genuinely about color.
- The views are driven through the app rather than unit-tested in isolation, so the tests cover the wiring (key → action → state → frame) that is where the bugs actually are.
- The colima tests use a fake `Runner` and never touch a VM. `internal/colima/live_test.go` lists the real profiles read-only and is skipped under `-short`. **Never write a test that starts, stops or deletes a real profile** — it is the daemon every other test and the user's own work runs on.
- Fuzz targets guard what untrusted input reaches: `FuzzSanitizeLogText` (container output — no escape but complete color sequences, no C0 or C1 controls), `FuzzExpandPluginVars` (a value is substituted once, never re-expanded), `FuzzParseShortcut` and `FuzzParse` (config). Their seeds run as ordinary tests; fuzz one with `go test -run '^$' -fuzz '^FuzzSanitizeLogText$' -fuzztime 30s ./internal/tui/views`, and commit any input it finds under `testdata/fuzz/`.
- `TestEmptyFilterDoesNotWedgeTheCursor` pins a bubbles quirk: `SetRows` clamps the cursor to `-1` on an empty row set and never restores it, silently killing every row action for the rest of the session. `setTableRows` exists solely to repair that, and any filter matching nothing reaches it — including an ordinary `docker ps` right after the last container stops.

## Documentation

See [`docs/README.md`](docs/README.md) for the docs index; design notes live in [`docs/design/`](docs/design/). Read the relevant doc before changing the area it covers, and update it in the same commit when a change makes it stale.
