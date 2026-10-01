# dockyard

A [k9s](https://k9scli.io)-style terminal UI for Docker — containers, images, volumes, networks and Compose projects in one navigable frame, with live log tailing, inspect, and the whole container lifecycle on the keyboard. On a Colima host it manages the VMs under the daemon too.

Built on [tuikit](https://github.com/blairham/tuikit), a shared Bubble Tea chrome.

```
 Context:  —                                            <1>      Containers  <a>      All         <r>      Refresh
 Endpoint: —                                            <2>      Images      </>      Filter      <ctrl-d> Remove
 Engine:   —                                            <3>      Volumes     <?>      Help        <R>      Restart
 Counts:   2/4 ctr                                      <4>      Networks    <o>      Inspect     <s>      Shell
 Dockyard: v0.1.0                                       <5>      Projects    <enter>  Logs        <u>      Start
                                                                             <:q>     Quit        <x>      Stop
╭──────────────────────────────────────────────── containers(all)[4] ────────────────────────────────────────────────╮
│ NAME              IMAGE              STATE       HEALTH     CPU%     MEM          PORTS                   AGE      │
│ web               nginx:1.27         running     healthy    0.42     24.1MB       8080→80/tcp             3h       │
│ api               ghcr.io/acme/api…  running     —          12.80    311MB                                3h       │
│ migrate           ghcr.io/acme/api…  exited      —                                                        3h       │
│ standalone        redis:7            paused      —                                                        3h       │
│                                                                                                                    │
│                                                                                                                    │
```

## Install

```bash
make install          # builds, copies to ~/.local/bin, and links the d6d alias
```

This repo is local-only — no remote, no published release — so `make install`
is the install.

## Run

`d6d` is the short name — `d` + 6 letters + `d`, the way `k8s` is Kubernetes — and runs the same binary.

```bash
dockyard                        # the active docker context
dockyard --context colima       # a specific context
dockyard --readonly             # refuse every mutating action
dockyard --all                  # start with stopped containers listed
dockyard --no-stats             # skip the CPU/MEM poll
dockyard --logoless             # no header logo (`:logo` toggles it at runtime)
dockyard --splashless           # skip the startup splash
dockyard --headless             # no header at all — the table gets the rows
dockyard --crumbsless           # no breadcrumbs
dockyard -r 5                   # refresh every 5s (default 3)
dockyard --request-timeout 2m   # one limit for every daemon request
dockyard -c runtimes            # open on a view (containers, images, volumes, networks, projects, runtimes)
```

**Settings persist in `config.yaml`**, shaped like k9s's: `dockyard config init` writes a commented one with the defaults to `~/.config/dockyard/config.yaml` (`dockyard config path` shows where it is read from; `$DOCKYARD_CONFIG_DIR` and `$XDG_CONFIG_HOME` move it). Every flag above has a key there, plus `context`, `thresholds` (CPU/MEM colours, k9s's 70/90), and `logger.tail` / `logger.showTime`. Flags given on the command line win. See [`docs/design/config.md`](docs/design/config.md).

**It follows your docker context.** The Go SDK only reads `DOCKER_HOST`, which is empty on Colima, Rancher Desktop, Podman and remote hosts — so an SDK tool dials `/var/run/docker.sock` and claims your daemon is down while `docker ps` works fine. dockyard reads the context store itself, in the CLI's own precedence order. See [`docs/design/docker-context-resolution.md`](docs/design/docker-context-resolution.md).

## Keys

Digits pick the resource; `?` shows everything.

| | | | |
|---|---|---|---|
| `0`–`6` | Containers / Images / Volumes / Networks / Projects / Runtimes / Events | `/` | filter (leading `!` negates) |
| `enter` | drill in — logs, layers, inspect; on a kind/k3d node, the pods' containers inside it | `:` | command palette |
| `o` | inspect (pretty-printed, colorized JSON); `H` health: the check, its streak, and the last probes' output; `T` top, `D` diff (files changed), `S` full stats | `r` | refresh |
| `u` / `x` | start ("up") / stop | `R` | restart |
| `K` | kill (SIGKILL — confirms) | `p` | pause / unpause |
| `s` | shell into the container | `ctrl-d` | remove (confirms) |
| `a` | toggle stopped containers / all images | `P` | prune (confirms) |
| `t` | toggle the CPU/MEM poll | `z` | volume sizes (slow) |
| `f` | toggle log follow (scrolling pauses it, `G` resumes) | `T` | toggle log timestamps |
| `ctrl-g` | toggle breadcrumbs | `ctrl-e` | toggle the header |
| `j` `k` `h` `l` | move (arrows work too) | `g` / `G` | top / bottom |
| `ctrl-f` / `ctrl-b` | page down / up | `ctrl-r` | reload |
| `q` | back out of a drill-in | `[` / `]` / `-` | view history back / forward / last |

Every destructive action asks first, and `--readonly` refuses them outright.

### Commands

`:containers` `:images` `:volumes` `:networks` `:projects` `:colima` · `:pf` (port forwards — `shift-f` on a container starts one, `b` opens a published port in the browser; see [`docs/design/port-forward.md`](docs/design/port-forward.md)) · `:events` (the daemon's live event feed — the last 10 minutes, then as it happens, colored by what each event means; `/` filters, `f` follows) · `:df` (disk usage, like `docker system df`; `P` on a row prunes that kind) · `:logs` / `:inspect` (`:describe`) (the selected row, as `l` / `o`) · `:top` / `:diff` / `:health` (the selected container) · `:ctx` (docker contexts, or `:ctx <name>` to switch directly) · `:pull <ref>` · `:prune` (stopped containers) · `:prune all` (like `docker system prune`: stopped containers, unused networks, dangling images, build cache — volumes kept) · `:prune all volumes` (and every unused volume) · `:prune cache` (build cache) · `:readonly` · `:logo` · `:q`

## Notes

- **Compose projects are labels**, folded from `com.docker.compose.project`. In the projects view `u` is `docker compose up -d`, `R` restart, `p` pull, `ctrl-d` `compose down` (confirms; volumes kept), run against the compose files the labels record. When those files are not on this machine — a remote daemon, a moved checkout — the keys fall back to acting on the containers directly. See [`docs/design/compose.md`](docs/design/compose.md).
- **The daemon can be slow.** `docker images` on a development host here takes over two minutes. Every list view single-flights its refresh, only the volatile views poll, and the expensive ones load on open and on `r`. See [`docs/design/daemon-latency.md`](docs/design/daemon-latency.md).
- **Container runtimes are view `5`** (`:runtimes`; `:colima` and `:podman` work too). dockyard finds what is installed — Colima, Podman machines, Docker Desktop, Rancher Desktop, OrbStack — and lists their VMs or engines together. `u` start, `x` stop, `R` restart and `enter` connect work everywhere; `n` new (pick the runtime in the form — that is how the first Podman machine is made), `e` edit resources (and Podman's rootful / user-mode networking), `o` inspect, `ctrl-d` delete and `s` shell where the runtime has machines to manage (Colima, Podman). `:pods` lists Podman pods across running machines. If the daemon dockyard resolves to belongs to a stopped runtime, it opens on this view instead of exiting. See [`docs/design/runtimes.md`](docs/design/runtimes.md).
- **`s` shells out to the `docker` CLI** (via `tea.ExecProcess`), passing `--host` so it always lands on the daemon you are looking at. Interactive TTY handling is a solved problem and not worth re-solving against a hijacked stream.

## Development

```bash
make check              # fmt + vet + test
go test -short ./...    # skip the live-daemon tests
```

Linting is a pre-commit hook (`pre-commit install`), not a make target — a
failing lint fails the commit, which is the only lint output worth reading.

`AGENTS.md` is the working agreement for this repo — read it before changing anything, especially the note on `fieldalignment` reordering struct fields.
