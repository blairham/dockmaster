# dockyard

A [k9s](https://k9scli.io)-style terminal UI for Docker — containers, images, volumes, networks and Compose projects in one navigable frame, with live log tailing, inspect, and the whole container lifecycle on the keyboard.

Built on [tuikit](https://github.com/blairham/tuikit), a shared Bubble Tea chrome.

```
 Context:  —                                            <1>      Containers  <a>      All
 Endpoint: —                                            <2>      Images      </>      Filter
 Engine:   —                                            <3>      Volumes     <?>      Help
 Counts:   2/4 ctr                                      <4>      Networks    <o>      Inspect
 Dockyard: v0.1.0                                       <5>      Projects    <enter>  Logs
                                                                             <:q>     Quit
                                                                             <r>      Refresh
                                                                             <ctrl-d> Remove
                                                                             <R>      Restart
                                                                             <e>      Shell
                                                                             <s>      Start
                                                                             <x>      Stop
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
make install          # builds and copies to ~/.local/bin
```

This repo is local-only — no remote, no published release — so `make install`
is the install.

## Run

```bash
dockyard                        # the active docker context
dockyard --context colima       # a specific context
dockyard --readonly             # refuse every mutating action
dockyard --all                  # start with stopped containers listed
dockyard --no-stats             # skip the CPU/MEM poll
```

**It follows your docker context.** The Go SDK only reads `DOCKER_HOST`, which is empty on Colima, Rancher Desktop, Podman and remote hosts — so an SDK tool dials `/var/run/docker.sock` and claims your daemon is down while `docker ps` works fine. dockyard reads the context store itself, in the CLI's own precedence order. See [`docs/design/docker-context-resolution.md`](docs/design/docker-context-resolution.md).

## Keys

Digits pick the resource; `?` shows everything.

| | | | |
|---|---|---|---|
| `1`–`5` | Containers / Images / Volumes / Networks / Projects | `/` | filter (leading `!` negates) |
| `enter` | drill in — logs, layers, inspect | `:` | command palette |
| `o` | inspect (pretty-printed, colorized JSON) | `r` | refresh |
| `s` / `x` | start / stop | `R` | restart |
| `K` | kill (SIGKILL — confirms) | `p` | pause / unpause |
| `e` | shell into the container | `ctrl-d` | remove (confirms) |
| `a` | toggle stopped containers / all images | `P` | prune (confirms) |
| `t` | toggle the CPU/MEM poll | `z` | volume sizes (slow) |
| `f` | toggle log follow | `T` | toggle log timestamps |

Every destructive action asks first, and `--readonly` refuses them outright.

### Commands

`:containers` `:images` `:volumes` `:networks` `:projects` · `:ctx` (docker contexts, or `:ctx <name>` to switch directly) · `:pull <ref>` · `:prune` · `:readonly` · `:logo` · `:q`

## Notes

- **Compose projects are labels.** There is no Engine-API project endpoint; dockyard folds containers by `com.docker.compose.project`, and drilling into one gives you a filtered containers view.
- **The daemon can be slow.** `docker images` on a development host here takes over two minutes. Every list view single-flights its refresh, only the volatile views poll, and the expensive ones load on open and on `r`. See [`docs/design/daemon-latency.md`](docs/design/daemon-latency.md).
- **`e` shells out to the `docker` CLI** (via `tea.ExecProcess`), passing `--host` so it always lands on the daemon you are looking at. Interactive TTY handling is a solved problem and not worth re-solving against a hijacked stream.

## Development

```bash
make check              # fmt + vet + test
go test -short ./...    # skip the live-daemon tests
```

Linting is a pre-commit hook (`pre-commit install`), not a make target — a
failing lint fails the commit, which is the only lint output worth reading.

`AGENTS.md` is the working agreement for this repo — read it before changing anything, especially the note on `fieldalignment` reordering struct fields.
