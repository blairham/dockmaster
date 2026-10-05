# dockmaster

[![CI](https://github.com/blairham/dockmaster/actions/workflows/ci.yml/badge.svg)](https://github.com/blairham/dockmaster/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/blairham/dockmaster?sort=semver)](https://github.com/blairham/dockmaster/releases/latest)
[![CodeQL](https://github.com/blairham/dockmaster/actions/workflows/codeql.yml/badge.svg)](https://github.com/blairham/dockmaster/actions/workflows/codeql.yml)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/blairham/dockmaster/badge)](https://scorecard.dev/viewer/?uri=github.com/blairham/dockmaster)
[![Go version](https://img.shields.io/github/go-mod/go-version/blairham/dockmaster)](go.mod)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

A [k9s](https://k9scli.io)-style terminal UI for Docker — containers, images, volumes, networks and Compose projects in one navigable frame, with live log tailing, inspect, and the whole container lifecycle on the keyboard. On a Colima host it manages the VMs under the daemon too.

Built on [tuikit](https://github.com/blairham/tuikit), a shared Bubble Tea chrome.

![dockmaster's containers view](docs/images/containers.png)

## Install

```bash
brew install blairham/tap/dockmaster   # installs dockmaster and the dm short name
```

Or download an archive from [Releases](https://github.com/blairham/dockmaster/releases)
— `checksums.txt` is signed with cosign and each archive carries SLSA build
provenance; [SECURITY.md](SECURITY.md#verifying-a-release) shows how to verify both. From source:

```bash
go install github.com/blairham/dockmaster@latest
# or, from a clone, with the version stamped and the dm alias linked:
make install          # builds, copies to ~/.local/bin, and links dm
```

## Run

`dm` is the short name and runs the same binary.

```bash
dockmaster                        # the active docker context
dockmaster --context colima       # a specific context
dockmaster --readonly             # refuse every mutating action
dockmaster --all                  # start with stopped containers listed
dockmaster --no-stats             # skip the CPU/MEM poll
dockmaster --logoless             # no header logo (`:logo` toggles it at runtime)
dockmaster --invert               # the skin (or the default look) dark to light
DOCKMASTER_SKIN=dracula dockmaster  # a k9s skin from ~/.config/dockmaster/skins/
dockmaster --splashless           # skip the startup splash
dockmaster --headless             # no header at all — the table gets the rows
dockmaster --crumbsless           # no breadcrumbs
dockmaster -r 5                   # refresh every 5s (default 3)
dockmaster --request-timeout 2m   # one limit for every daemon request
dockmaster -c runtimes            # open on a view, or any : command or alias (-c xray, -c pg, -c "images @prod")
dockmaster --log-level info       # log more than warnings and errors (debug, info, warn, error)
dockmaster --log-file ./dm.log    # log somewhere other than the state dir
dockmaster info                   # every path dockmaster uses, and the endpoint it would dial
```

**dockmaster keeps a log** at `~/.local/state/dockmaster/dockmaster.log` (`$XDG_STATE_HOME` moves it, `--log-file` replaces it): every error it flashes, and at `--log-level info` plugin and hotkey runs and context switches. It never records environment values, a plugin's arguments or output, or what was typed into a plugin's inputs form. `dockmaster info` shows where it is, with the config, skins, dumps and history paths and the docker context and endpoint dockmaster would use — it does not dial the daemon, so it works when there is none.

**Settings persist in `config.yaml`**, shaped like k9s's: `dockmaster config init` writes a commented one with the defaults to `~/.config/dockmaster/config.yaml` (`dockmaster config path` shows where it is read from; `$DOCKMASTER_CONFIG_DIR` and `$XDG_CONFIG_HOME` move it). Every flag above has a key there, plus `context`, `thresholds` (CPU/MEM colours, k9s's 70/90), `portForwardAddress`, and `logger.tail` / `logger.showTime`. Flags given on the command line win. See [`docs/design/config.md`](docs/design/config.md).

**It follows your docker context.** The Go SDK reads only the environment — `DOCKER_HOST`, which is empty on Colima, Rancher Desktop, Podman and remote hosts — so an SDK tool dials `/var/run/docker.sock` and claims your daemon is down while `docker ps` works fine. dockmaster reads the context store itself, in the CLI's own precedence order. See [`docs/design/docker-context-resolution.md`](docs/design/docker-context-resolution.md).

## Keys

Digits pick the resource; `?` shows every key, and `ctrl-a` every `:` command.

| | | | |
|---|---|---|---|
| `0`–`6` | Containers / Images / Volumes / Networks / Projects / Runtimes / Events | `/` | filter: a regex (leading `!` negates), `-f term` fuzzy, `-l k=v,k!=v,k,!k` by label |
| `enter` | drill in — logs, layers, inspect | `:` | command palette |
| `n` | on a ⎈ kind/k3d node: the pods' containers inside it | `i` / `d` / `y` | inspect, as `o` (`d` / `y` are k9s's describe / yaml) |
| `o` | inspect (pretty-printed, colorized JSON); `H` health: the check, its streak, and the last probes' output; `T` top, `D` diff (files changed), `S` full stats; `C` copies files in or out (`docker cp`); `e` edits CPU/memory limits, restart policy and name, live (`docker update`, no restart) | `r` | refresh |
| `u` / `x` | start ("up") / stop; on an image, `u` runs it (name, ports, env, volumes, command, `--rm`) | `R` | restart |
| `K` | kill (SIGKILL — confirms) | `p` | pause / unpause |
| `s` | shell into the container; `A` attaches to its main process (signals not forwarded, so ctrl-c never stops it) | `ctrl-d` | remove (confirms) |
| `a` | toggle stopped containers / all images | `P` | prune (confirms) |
| `ctrl-k` | show the containers a runtime's built-in Kubernetes runs pods in (hidden by default) | `ctrl-z` | faults only: unhealthy, restarting, dead, or exited non-zero (stopped ones included) |
| `ctrl-w` | wide columns: ID, command, networks and IP | | |
| `t` | toggle the CPU/MEM poll | `z` | volume sizes (slow) |
| on a volume: `enter` | browse its files (`enter` opens, `esc` goes up) | `o` | inspect |
| in a log: `s` | autoscroll on/off (scrolling pauses it, `G` resumes) | `t` | timestamps |
| in a log: `0`–`5` | usual backlog / last 1m, 5m, 15m, 30m, 1h | `w` / `f` | wrap / fullscreen |
| in a log: `c` / `ctrl-s` | copy / save what is shown (`~/.local/state/dockmaster/logs`) | `shift-c` | clear |
| in a log: `m` | mark: a stamped rule at the end, so later lines are easy to find | | |
| in inspect: `/` | search: matches highlighted, nothing hidden, the title counts them; `n` / `N` next / previous | `c` / `ctrl-s` | copy / save the document (saves list in `:sd`) |
| in inspect: `f` | fullscreen | `a` | refresh on the poll |
| on a project: `l` | every container's logs in one stream | `u` / `x` / `R` | compose up / stop / restart |
| on a project: `p` / `e` | pull / edit the compose files, then up | `ctrl-d` | compose down (confirms) |
| on a project: `s` | scale a service: pick it, set its container count (fewer confirms) | | |
| `ctrl-s` | save the table as text (`~/.local/state/dockmaster/dumps`) | `shift-←/→` / `shift-↑/↓` | sort column / direction |
| `c` | copy the row's name to the clipboard | `I` | copy its full ID |
| in `:` or `/`: `↑` / `↓` | earlier commands / filters, kept across runs (`~/.local/state/dockmaster/history.json`) | `ctrl-n` / `ctrl-p` | cycle `:` suggestions (`tab` or `→` accepts) |
| `U` | on an image, volume or network: the containers using it, stopped ones too (`esc` back) | `J` | on a container: jump to its compose project |
| `shift-f` | port-forward to the container (a label can prefill or confirm it) | `f` | on a container: its port forwards (`esc` back) |
| `space` / `ctrl-space` | mark a row / a range — lifecycle and remove keys then act on every marked row | `ctrl-\` | clear marks |
| `ctrl-g` | toggle breadcrumbs | `ctrl-e` | toggle the header |
| `j` `k` `h` `l` | move (arrows work too) | `g` / `G` | top / bottom |
| `ctrl-f` / `ctrl-b` | page down / up | `ctrl-r` | reload |
| `q` | back out of a drill-in | `[` / `]` / `-` | view history back / forward / last |

The log keys are k9s's. Where k9s and Docker mean different things the key stays dockmaster's: `p` pauses (k9s's previous-container logs have no Docker equivalent — an exited container's logs are kept), `a` shows stopped containers, and `ctrl-k` shows Kubernetes containers rather than killing one (`K` kills, after a confirm).

Every destructive action asks first, and `--readonly` refuses them outright.

### Commands

Every `:` command, its other spellings, and each view's keys are in **[docs/commands.md](docs/commands.md)** — and in the app, `ctrl-a` lists every command and your aliases, with `enter` running one. The ones you will reach for first:

- Views: `:containers` `:images` `:volumes` `:networks` `:projects` `:runtimes` `:events`, plus `:pf` (port forwards), `:df` (disk usage), `:pods` and `:pulses` (a dashboard for the whole daemon). Add a filter or another docker context: `:containers @prod /postgres`.
- Tools: `:xray` (projects → services → containers → what they use, as a tree), `:lint` (each container's risky settings), `:dir` (bring a project up from its compose file, or `ctrl-d` take it down), `:sd` (what `ctrl-s` saved), `:ctx` (switch docker context; `ctrl-d` removes one).
- `:pull <ref>`, `:prune` / `:prune all` / `:prune cache`, `:readonly`, `:q`.
- `:hostshell` opens a **root** shell on the daemon's host — the VM under Docker Desktop, OrbStack or Rancher Desktop, or a remote context's server — through a privileged `nsenter` helper (`hostShell.image`, default `alpine:3`, pulled if missing). It always confirms and `--readonly` refuses it: see [`docs/design/host-shell.md`](docs/design/host-shell.md).
- `v` on an image scans it for vulnerabilities with `grype` or `trivy`. The result is cached by image ID, and with `imageScans.enable` a `VULN` column in images and containers summarizes each image's last scan (`C2 H5`); `imageScans.background` scans the rest, one at a time, while the images view is open.

Your own `:` names go in `aliases.yaml`, your own keys in `hotkeys.yaml`, your own commands on the selected row in `plugins.yaml` (with `inputs` and `pipes`), and each view's columns in `views.yaml` — including columns of your own read from a label or a field, as in `SVC:.Labels.com\.docker\.compose\.service` — all in k9s's formats: see [`docs/design/config.md`](docs/design/config.md). `-c` starts on any command or alias, and `dockmaster info` shows where everything lives.

## Screenshots

| | |
|---|---|
| ![Help: every key the view takes](docs/images/help.png) | ![A container's healthcheck, probe by probe](docs/images/health.png) |
| **`?`** — every key the view takes, by section | **`H`** — a container's healthcheck, newest probe first |
| ![Tailing a container's log](docs/images/logs.png) | ![Running a container from an image](docs/images/run.png) |
| **`enter`** — the log, following, with a mark (`m`) | **`u`** on an image — `docker run` as a form |
| ![Two containers marked for a bulk action](docs/images/marks.png) | ![Browsing a volume's files](docs/images/volume.png) |
| **`space`** — rows marked; `x`, `R` or `ctrl-d` act on all of them | **`enter`** on a volume — its files, read through a read-only helper |

`make screenshots` regenerates all of them — see [Development](#development).

## Notes

- **Compose projects are labels**, folded from `com.docker.compose.project`. In the projects view `l` follows every container's logs in one stream, each line prefixed with its container as `docker compose logs -f` prints it (containers that start later join), `u` is `docker compose up -d`, `e` opens the compose file in `$EDITOR` and runs `up -d` when it comes back changed, `R` restart, `p` pull, `s` scales a service (`up -d --scale svc=N --no-recreate svc`; fewer containers confirms), `ctrl-d` `compose down` (confirms; volumes kept), run against the compose files the labels record. When those files are not on this machine — a remote daemon, a moved checkout — the keys fall back to acting on the containers directly. See [`docs/design/compose.md`](docs/design/compose.md).
- **Port forwards reach ports a container never published.** `shift-f` asks `local:container` (several, comma-separated) and starts a small `alpine/socat` relay on the container's network; `f` lists that container's forwards, `:pf` all of them, and they stop when dockmaster quits. A container can carry `dockmaster.port-forwards=8080:80` to prefill the prompt, or `dockmaster.auto-port-forwards=8080:80` to replace it with a yes/no confirm — k9s's FastForwards annotations, as labels. Forwards listen on `127.0.0.1` unless `portForwardAddress` in `config.yaml` says otherwise, and `shift-f` warns when that address is reachable from the network. See [`docs/design/port-forward.md`](docs/design/port-forward.md).
- **Volumes are browsable.** `enter` on a volume lists its files through a short-lived helper container that mounts it read-only with no network — Docker has no API for a volume's files. See [`docs/design/volume-browser.md`](docs/design/volume-browser.md).
- **Skins are k9s's.** Put a k9s skin in `~/.config/dockmaster/skins/` and name it in `config.yaml` (`ui.skin`) or `DOCKMASTER_SKIN`; `--invert` turns any skin, or the default look, dark to light. See [`docs/design/config.md`](docs/design/config.md#skins).
- **Per-context settings.** A `contexts:` block gives a docker context its own skin, `readOnly` and `defaultView` — `prod: {skin: red, readOnly: true}` — applied whenever dockmaster is on it, and a `:ctx` switch opens the view you last had open there. See [`docs/design/config.md`](docs/design/config.md#per-context-settings).
- **The daemon can be slow.** `docker images` on a development host here takes over two minutes. Every list view single-flights its refresh, only the volatile views poll, and the expensive ones load on open and on `r`. See [`docs/design/daemon-latency.md`](docs/design/daemon-latency.md).
- **Container runtimes are view `5`** (`:runtimes`; `:colima` and `:podman` work too). dockmaster finds what is installed — Colima, Podman machines, Docker Desktop, Rancher Desktop, OrbStack — and lists their VMs or engines together. `u` start, `x` stop, `R` restart and `enter` connect work everywhere; `n` new (pick the runtime in the form — that is how the first Podman machine is made), `e` edit resources (and Podman's rootful / user-mode networking), `o` inspect, `ctrl-d` delete where the runtime has machines to manage (Colima, Podman), and `s` a shell on the machine — ssh for Colima and Podman, a root host shell for Docker Desktop, OrbStack and Rancher Desktop (below). `K` manages a kind cluster on the machine's Docker daemon: stops or starts the one that is there, or creates one where there is none — a control-plane and a worker, with a local registry on `localhost:5001` wired into the nodes, kind's own recipe (kubectl context `kind-k8s`). The K8S column shows the cluster (bright running, dim stopped), and `own` where a runtime's built-in Kubernetes is on instead. `:pods` lists Podman pods across running machines. If the daemon dockmaster resolves to belongs to a stopped runtime, it opens on this view instead of exiting. See [`docs/design/runtimes.md`](docs/design/runtimes.md).
- **`s` shells out to the `docker` CLI** (via `tea.ExecProcess`), passing `--host` so it always lands on the daemon you are looking at. Interactive TTY handling is a solved problem and not worth re-solving against a hijacked stream.

## Development

```bash
make check              # fmt + vet + test
go test -short ./...    # skip the live-daemon tests
```

`make screenshots` re-renders `docs/images/` with [VHS](https://github.com/charmbracelet/vhs)
(`brew install vhs`). It stages the five-container `shop` project from
`docs/demo/compose.yaml` on `DEMO_HOST` (OrbStack's socket by default — pick a
daemon with nothing of yours on it, since the shots show every container),
plays `docs/demo/screenshots.tape`, and takes the project down again.

Linting is a pre-commit hook (`pre-commit install`), not a make target — a
failing lint fails the commit, which is the only lint output worth reading.

`AGENTS.md` is the working agreement for this repo — read it before changing anything, especially the note on `fieldalignment` reordering struct fields.

## Contributing

Issues and pull requests are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).
Report security issues privately, as [SECURITY.md](SECURITY.md) describes.

## License

[Apache-2.0](LICENSE). Copyright 2026 Blair Hamilton.
