# dockmaster

A [k9s](https://k9scli.io)-style terminal UI for Docker — containers, images, volumes, networks and Compose projects in one navigable frame, with live log tailing, inspect, and the whole container lifecycle on the keyboard. On a Colima host it manages the VMs under the daemon too.

Built on [tuikit](https://github.com/blairham/tuikit), a shared Bubble Tea chrome.

![dockmaster's containers view](docs/images/containers.png)

## Install

```bash
make install          # builds, copies to ~/.local/bin, and links the dm alias
```

This repo is local-only — no remote, no published release — so `make install`
is the install.

## Run

`dm` is the short name and runs the same binary.

```bash
dockmaster                        # the active docker context
dockmaster --context colima       # a specific context
dockmaster --readonly             # refuse every mutating action
dockmaster --all                  # start with stopped containers listed
dockmaster --no-stats             # skip the CPU/MEM poll
dockmaster --logoless             # no header logo (`:logo` toggles it at runtime)
dockmaster --splashless           # skip the startup splash
dockmaster --headless             # no header at all — the table gets the rows
dockmaster --crumbsless           # no breadcrumbs
dockmaster -r 5                   # refresh every 5s (default 3)
dockmaster --request-timeout 2m   # one limit for every daemon request
dockmaster -c runtimes            # open on a view (containers, images, volumes, networks, projects, runtimes)
```

**Settings persist in `config.yaml`**, shaped like k9s's: `dockmaster config init` writes a commented one with the defaults to `~/.config/dockmaster/config.yaml` (`dockmaster config path` shows where it is read from; `$DOCKMASTER_CONFIG_DIR` and `$XDG_CONFIG_HOME` move it). Every flag above has a key there, plus `context`, `thresholds` (CPU/MEM colours, k9s's 70/90), and `logger.tail` / `logger.showTime`. Flags given on the command line win. See [`docs/design/config.md`](docs/design/config.md).

**It follows your docker context.** The Go SDK only reads `DOCKER_HOST`, which is empty on Colima, Rancher Desktop, Podman and remote hosts — so an SDK tool dials `/var/run/docker.sock` and claims your daemon is down while `docker ps` works fine. dockmaster reads the context store itself, in the CLI's own precedence order. See [`docs/design/docker-context-resolution.md`](docs/design/docker-context-resolution.md).

## Keys

Digits pick the resource; `?` shows everything.

| | | | |
|---|---|---|---|
| `0`–`6` | Containers / Images / Volumes / Networks / Projects / Runtimes / Events | `/` | filter (leading `!` negates) |
| `enter` | drill in — logs, layers, inspect | `:` | command palette |
| `c` | on a ⎈ kind/k3d node: the pods' containers inside it | | |
| `o` | inspect (pretty-printed, colorized JSON); `H` health: the check, its streak, and the last probes' output; `T` top, `D` diff (files changed), `S` full stats; `C` copies files in or out (`docker cp`); `e` edits CPU/memory limits, restart policy and name, live (`docker update`, no restart) | `r` | refresh |
| `u` / `x` | start ("up") / stop; on an image, `u` runs it (name, ports, env, volumes, command, `--rm`) | `R` | restart |
| `K` | kill (SIGKILL — confirms) | `p` | pause / unpause |
| `s` | shell into the container | `ctrl-d` | remove (confirms) |
| `a` | toggle stopped containers / all images | `P` | prune (confirms) |
| `t` | toggle the CPU/MEM poll | `z` | volume sizes (slow) |
| `f` | toggle log follow (scrolling pauses it, `G` resumes) | `T` | toggle log timestamps |
| in a log: `0`–`5` | usual backlog / last 1m, 5m, 15m, 30m, 1h | `w` / `F` | wrap / fullscreen |
| in a log: `c` / `ctrl-s` | copy / save what is shown (`~/.local/state/dockmaster/logs`) | `ctrl-k` | clear |
| `ctrl-g` | toggle breadcrumbs | `ctrl-e` | toggle the header |
| `j` `k` `h` `l` | move (arrows work too) | `g` / `G` | top / bottom |
| `ctrl-f` / `ctrl-b` | page down / up | `ctrl-r` | reload |
| `q` | back out of a drill-in | `[` / `]` / `-` | view history back / forward / last |

Every destructive action asks first, and `--readonly` refuses them outright.

### Commands

`:containers` `:images` `:volumes` `:networks` `:projects` `:colima` · `:pf` (port forwards — `shift-f` on a container starts one, `b` opens a published port in the browser; see [`docs/design/port-forward.md`](docs/design/port-forward.md)) · `:events` (the daemon's live event feed — the last 10 minutes, then as it happens, colored by what each event means; `/` filters, `f` follows) · `:df` (disk usage, like `docker system df`; `P` on a row prunes that kind) · `:logs` / `:inspect` (`:describe`) (the selected row, as `l` / `o`) · `:top` / `:diff` / `:health` (the selected container) · `:ctx` (docker contexts, or `:ctx <name>` to switch directly) · `:pull <ref>` · `:prune` (stopped containers) · `:prune all` (like `docker system prune`: stopped containers, unused networks, dangling images, build cache — volumes kept) · `:prune all volumes` (and every unused volume) · `:prune cache` (build cache) · `:readonly` · `:logo` · `:q`

## Screenshots

| | |
|---|---|
| ![Help: every key the view takes](docs/images/help.png) | ![A container's healthcheck, probe by probe](docs/images/health.png) |
| **`?`** — every key the view takes, by section | **`H`** — a container's healthcheck, newest probe first |
| ![Tailing a container's log](docs/images/logs.png) | ![Running a container from an image](docs/images/run.png) |
| **`enter`** — the log, following, with its toggles | **`u`** on an image — `docker run` as a form |

`make screenshots` regenerates all of them — see [Development](#development).

## Notes

- **Compose projects are labels**, folded from `com.docker.compose.project`. In the projects view `u` is `docker compose up -d`, `e` opens the compose file in `$EDITOR` and runs `up -d` when it comes back changed, `R` restart, `p` pull, `ctrl-d` `compose down` (confirms; volumes kept), run against the compose files the labels record. When those files are not on this machine — a remote daemon, a moved checkout — the keys fall back to acting on the containers directly. See [`docs/design/compose.md`](docs/design/compose.md).
- **The daemon can be slow.** `docker images` on a development host here takes over two minutes. Every list view single-flights its refresh, only the volatile views poll, and the expensive ones load on open and on `r`. See [`docs/design/daemon-latency.md`](docs/design/daemon-latency.md).
- **Container runtimes are view `5`** (`:runtimes`; `:colima` and `:podman` work too). dockmaster finds what is installed — Colima, Podman machines, Docker Desktop, Rancher Desktop, OrbStack — and lists their VMs or engines together. `u` start, `x` stop, `R` restart and `enter` connect work everywhere; `n` new (pick the runtime in the form — that is how the first Podman machine is made), `e` edit resources (and Podman's rootful / user-mode networking), `o` inspect, `ctrl-d` delete and `s` shell where the runtime has machines to manage (Colima, Podman). `K` manages a kind cluster on the machine's Docker daemon: stops or starts the one that is there, or creates one where there is none — a control-plane and a worker, with a local registry on `localhost:5001` wired into the nodes, kind's own recipe (kubectl context `kind-k8s`). The K8S column shows the cluster (bright running, dim stopped), and `own` where a runtime's built-in Kubernetes is on instead. `:pods` lists Podman pods across running machines. If the daemon dockmaster resolves to belongs to a stopped runtime, it opens on this view instead of exiting. See [`docs/design/runtimes.md`](docs/design/runtimes.md).
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
