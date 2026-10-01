# Design: Container runtimes

**Status:** Living
**Code:** `internal/engines/`, `internal/tui/views/runtimes.go`, `internal/tui/views/runtimeform.go`, `internal/tui/runtimes.go`, the startup fallback in `main.go`

## Purpose

On a laptop the docker daemon usually runs inside something else: a Colima VM,
a Podman machine, Docker Desktop, Rancher Desktop, OrbStack. The runtimes view
(`5`, `:runtimes`) manages that layer — the one view that keeps working when
the daemon is gone, which is exactly when it is needed.

## One interface, per-runtime abilities

Every runtime is an `engines.Provider` over its own CLI. `Caps` says what it can
do beyond start and stop, and the view refuses the rest by name rather than
running anything.

| Provider | Detected by | Start/stop/restart | Create / edit / delete / shell | Endpoint |
|---|---|---|---|---|
| `colima` | `colima` on PATH | ✓ | ✓ (see `colima.md`) | `~/.colima/<p>/docker.sock` |
| `podman` | `podman` on PATH | ✓ (`podman machine …`) | ✓ (`init --now`, `set`, `rm -f`, `ssh`) | the machine's API socket, from `podman machine inspect` |
| `docker-desktop` | `docker desktop` plugin — on PATH, or run straight from `Docker.app`'s bundle — or the app | ✓ (`docker desktop start/stop`, else `open -a` / quit) | — | context `desktop-linux` |
| `rancher-desktop` | `rdctl`, or the app | ✓ (`rdctl start/shutdown`) | — | context `rancher-desktop` |
| `orbstack` | `orb`, or the app | ✓ (`orb start/stop`) | — | context `orbstack` |

**A runtime counts as installed when its CLI or app is.** A docker context left
behind by an uninstalled app does not count — the machine this was built on has
a `desktop-linux` context and no Docker Desktop, and offering to start it would
be a lie. `TestDetect` pins that case.

**Single-engine runtimes are "running" when their daemon answers a ping** on
their endpoint. Each app has its own notion of status, but whether the daemon
answers is the one that means the same thing for all three, and it is the one
dockyard cares about.

**A runtime's own status wins over the ping** where it has one: `docker
desktop status --format json` and `orb status`. Found live: with Docker
Desktop's Resource Saver pausing the VM, the first ping into it outlasted the
timeout and a running engine was listed as stopped. Resource Saver's "paused"
counts as running — the engine wakes on the next request.

**Podman only grows disks.** `podman machine set --disk-size` rejects a size
that is not larger than the current one, even an equal one, so an edit that
leaves the disk alone omits the flag. Found live; `TestPodmanVerbs` pins it.

**Starting OrbStack or Docker Desktop switches the docker CLI's current
context** to theirs (`~/.docker/config.json`). A shell that sets
`DOCKER_CONTEXT` keeps its own; dockyard follows the same precedence.

**Units.** Forms take GiB everywhere; podman takes memory in MiB, so the Podman
provider converts. `TestPodmanVerbs` pins the argv.

**One runtime failing to list does not hide the others**: its error is shown
above the rows the rest returned.

## Creating, editing, inspecting

`n` opens a form whose **Provider** field (←/→) chooses among every installed
runtime that can create machines, starting on the runtime of the row the cursor
was on. That is how the first Podman machine gets made: with none yet there is
no Podman row to stand on. Switching provider rebuilds only the runtime-specific
fields — a Runtime choice for Colima, Rootful and User net for Podman — and keeps
what was already typed.

Podman's two modes are toggles on create and edit: **Rootful** (containers run
as root in the VM) and **User net** (user-mode networking, for VPNs that break
the default). An edit sends only what changed, and podman's restart uses
`podman machine restart` where podman has it (6.0+), stop-and-start where not.

`o` on a row shows the runtime's own description of the machine: `podman
machine inspect`, `colima status --json` (or the list entry when stopped),
`docker desktop status`.

## Pods

The Docker API, which every other view speaks, has no pods. `:pods` lists them
from the podman CLI across every running Podman machine, through the machine's
connection — `<name>`, or `<name>-root` for a rootful machine, which is where its
containers live. `u`/`x`/`R` start, stop and restart a pod, `ctrl-d` removes it
and its containers (confirms), `o`/`enter` inspects it.

## Daemon down at startup

When the resolved endpoint does not answer, `engines.Owner` asks every detected
runtime whether one of its machines serves it. If one does, dockyard opens on
the runtimes view with a notice instead of exiting — starting that machine is
the fix, and dockyard can do it. Otherwise it fails with the usual one-line
error.

## What carries over from Colima

Busy markers (a start runs for a minute while the runtime still says
"Stopped"), the ten-minute lifecycle timeout, confirming stop/restart/delete
(each takes every container in the VM with it), reconnecting after starting or
restarting the machine dockyard is connected to, and the create/edit form — see
`colima.md`, which is still the reference for those choices.

## Verified live (2026-10-01)

| Runtime | Verified |
|---|---|
| colima | list, start/stop/restart, create/edit, connect (see `colima.md`) |
| podman 6.1.3 | create (`init --now`), Docker API on the machine socket, stop, edit (cpus/memory, rootful), restart, delete; pods: list, stop/start/restart, inspect, rm, and pods behind the `-root` connection after going rootful |
| Docker Desktop 4.x | status (incl. a running engine), stop, start |
| OrbStack 2.2.3 | status, start, stop |
| Rancher Desktop | not yet — it cannot be installed alongside Docker Desktop |

## Listing must stay fast

The runtimes view lists every detected runtime on each refresh, so the
slowest one sets the pace. Two measures keep it under a second:

- **Providers are listed in parallel**, rows kept in provider order
  (`TestRuntimesListInParallel`).
- **A single-engine runtime whose unix socket is off disk is Stopped**
  without asking it. Docker Desktop's `docker desktop status` retries for
  16 seconds before reporting that the app is not running, measured on
  2026-10-01; with that call skipped, a full refresh here took 0.12 s
  instead of over 16 (`TestSingleSocketGoneSkipsStatus`). A socket that
  is present but unanswerable — Resource Saver — still goes to the
  runtime's own status.
