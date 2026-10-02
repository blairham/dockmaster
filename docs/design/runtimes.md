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
dockmaster cares about.

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
`DOCKER_CONTEXT` keeps its own; dockmaster follows the same precedence.

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
runtime whether one of its machines serves it. If one does, dockmaster opens on
the runtimes view with a notice instead of exiting — starting that machine is
the fix, and dockmaster can do it. Otherwise it fails with the usual one-line
error.

## What carries over from Colima

Busy markers (a start runs for a minute while the runtime still says
"Stopped"), the ten-minute lifecycle timeout, confirming stop/restart/delete
(each takes every container in the VM with it), reconnecting after starting or
restarting the machine dockmaster is connected to, and the create/edit form — see
`colima.md`, which is still the reference for those choices.

## Verified live (2026-10-01)

| Runtime | Verified |
|---|---|
| colima | list, start/stop/restart, create/edit, connect (see `colima.md`) |
| podman 6.1.3 | create (`init --now`), Docker API on the machine socket, stop, edit (cpus/memory, rootful), restart, delete; pods: list, stop/start/restart, inspect, rm, and pods behind the `-root` connection after going rootful |
| Docker Desktop 4.x | status (incl. a running engine), stop, start |
| OrbStack 2.2.3 | status, start, stop |
| Rancher Desktop 1.24 (moby, Kubernetes on) | 2026-10-02: detection before first launch, status, VM size, built-in Kubernetes (`own`), connect, stop (13 s), start (25 s) |

### Rancher Desktop

- **rdctl before first launch.** Rancher adds `~/.rd/bin` to PATH only
  during its first-run setup, but `rdctl` ships inside the app from the
  start. Detection looks on PATH, then in `~/.rd/bin`, then in the bundle,
  so start/stop go through `rdctl start` / `shutdown` (which quits and
  relaunches the app) and Kubernetes state is read from the beginning.
- **VM size** comes from `rdctl list-settings` (`virtualMachine.numberCPUs`,
  `memoryInGB`, `experimental.virtualMachine.diskSize`), asked only while
  it runs — `rdctl` cannot answer while the app is down.
- **Its Kubernetes runs as docker containers.** With the moby engine,
  cri-dockerd runs every pod as `k8s_<container>_<pod>_<namespace>_…`
  containers, a pause container per pod. The containers view hides them
  by default — Rancher alone starts eleven — and says how many in the
  border title and, on an otherwise empty list, how to show them:
  `ctrl+k` lists them by container and pod, filterable by namespace;
  pause containers never. They are recognised by the
  `io.kubernetes.pod.*` labels (`Container.Kube`), not by name.
- **Their images are named, not IDs.** cri-dockerd creates pod containers
  from an image ID, so docker reports their image as a bare `sha256:`.
  `Containers` asks the daemon what each such ID is called — its tag, else
  `repository@sha256:<12>` from a digest — once per ID for the session
  (an ID names one image for good), never by listing every image, which
  can take minutes on a large host. A failed lookup is asked again on the
  next refresh; an image with no name keeps its ID.

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

## Kubernetes

`K` manages a **kind** cluster on the machine's Docker daemon — the setup a
kind user already has, and one mechanism for every runtime with a daemon:

| On a machine with | `K` (after a confirm) |
|---|---|
| a running kind/k3d cluster | stops its node containers, and its registry with the last kind cluster using it |
| a stopped one | starts them again, the registry first (a node coming up may pull from it) |
| none | creates one: `kind create cluster` against the machine's daemon (`DOCKER_HOST`), control-plane + worker, then `kind-registry` (`registry:2` on `127.0.0.1:5001`, restart unless-stopped) wired into each node's `/etc/containerd/certs.d/localhost:5001/hosts.toml` and joined to the `kind` network |

That is kind's documented local-registry recipe, and what was checked
against a working cluster: the containerd patch is the same setting its
nodes' `config.toml` carries, and the `hosts.toml` dockmaster writes is
byte-identical to theirs. The cluster is `k8s` (kubectl context `kind-k8s`)
unless that context already exists — another machine's — and then
`k8s-<machine>`, so a second cluster never overwrites the first's context.

**The registry is part of the cluster, but outlives it.** It stays a
separate container — kind's recipe, and the only way images survive a
`kind delete cluster`, pushing to `localhost:5001` keeps working from the
host, and containerd (outside Kubernetes networking) can reach it — but
dockmaster treats it as the cluster's: `K` starts and stops it with the
nodes (kind clusters on a daemon share it, so it stops only with the last
one running), the containers list marks it ⎈, and the node view shows where
to push and whether it is up. It is recognised as kind's recipe makes it:
named `kind-registry`, or a `registry` image on the `kind` network.

Clusters are found by label (`io.x-k8s.kind.cluster`, `k3d.cluster`),
running or stopped (`docker.Clusters`), for every running machine on each
refresh. The K8S column shows the tool — bright running, dim stopped — and
`own` where the runtime's built-in Kubernetes is on (read from `colima
status`, `orb config`, `rdctl list-settings`, `docker desktop kubernetes
status`). `K` refuses there, and on a stopped machine.

**Why not the runtimes' own switches.** The first version of `K` turned on
the runtime's built-in cluster. On a Colima VM already running a kind
cluster it started Colima's k3s too — a second cluster in the same VM, which
also took kubectl's current context — and it read only the runtime's own
switch, so it never saw the kind cluster dockmaster itself marked ⎈. The
native toggles were removed; their status readers stay, for the column.

The kind and daemon operations sit behind seams (`dialCluster`, `kindRun`,
`kubeconfigs`) so tests drive every case — stop, start, create in order,
naming around a taken context, and each refusal — without a daemon. No test
creates a real cluster: that would pull node images onto, and collide with,
the developer's own.
