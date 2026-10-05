# Design: Host shell

**Status:** Living
**Code:** `internal/docker/hostshell.go`, `internal/tui/hostshell.go`, `runtimeShell` in `internal/tui/runtimes.go`, `hostShell` in `internal/config/config.go`

## Purpose

k9s opens a shell on a Kubernetes node (`featureGates.nodeShell`, with a
`shellPod` image). The Docker counterpart of a node is the machine the
daemon runs on: the VM under Docker Desktop, OrbStack or Rancher Desktop,
or the server behind a remote context. Colima and Podman have `ssh` for
their VMs and the runtimes view's `s` already uses it; everything else had
no way in (#59).

## What it runs

A throwaway helper container on the daemon, which enters PID 1's
namespaces and runs the host's own `sh`:

```
docker <endpoint flags> run --rm -it --privileged --pid=host --net=host \
  --label dockmaster.helper=hostshell <image> \
  nsenter -t 1 -m -u -i -n -p -- sh
```

- `--pid=host` makes PID 1 the host's init rather than the helper's own.
- `--privileged` is what lets `nsenter` `setns` into another mount
  namespace: that needs `CAP_SYS_ADMIN`, and the default seccomp profile
  refuses `setns`.
- `-m -u -i -n -p` enter PID 1's mount, UTS, IPC, network and PID
  namespaces. After `-m` the filesystem is the host's, so `sh` is the
  host's `/bin/sh`; only `nsenter` comes from the image. Entering a PID
  namespace forks, which `nsenter` does by default.
- `--net=host` creates no network for a helper that never uses one of its
  own (`-n` enters the host's regardless).
- `--rm` removes the helper when the shell exits, and the
  `dockmaster.helper=hostshell` label marks it, so
  `docker ps --filter label=dockmaster.helper` finds one left by a crash.

It goes through the docker CLI with `tea.ExecProcess` (`App.inTerminal`),
as `s` and `A` on a container do: raw mode, resizes and signals are the
CLI's job. The endpoint flags come first, so the helper lands on the daemon
meant, never the shell's current context.

**No shell parses anything.** docker gets an argv; the image is one
argument after every flag. An image beginning with `-` would be read by
`docker run` as a flag (`--volume=/:/host`), so it is refused, both when
config.yaml is loaded and again before the confirm.

## The image

`hostShell.image` in config.yaml, default **`alpine:3`**. Alpine's busybox
is built with the `nsenter` applet (`CONFIG_NSENTER=y` in aports'
`main/busybox/busyboxconfig`, checked on `master` and `3.22-stable`), and
busybox's `nsenter` takes every flag above: `-t PID`, `-m`, `-u`, `-i`,
`-n`, `-p` (each with an optional attached file), `--` ending options, and
forking for `-p` unless `-F`. The official `busybox` image (built from
`defconfig`) has it too; any image with an `nsenter` that takes those flags
works, util-linux's included.

**If the daemon does not have the image, `docker run` pulls it first.**
The confirm says so. On a machine that cannot reach the registry, point
`hostShell.image` at one it can.

## Two ways in

- **`:hostshell`** opens it on the daemon dockmaster is showing, with that
  daemon's endpoint flags (`Client.EndpointArgs`: `--context` for a TLS
  context, else `--host`). This is the one that covers a remote context
  and a plain dockerd. `:hostshell @ctx` switches first, as any command
  does.
- **`s` on a Docker Desktop, OrbStack or Rancher Desktop row** in the
  runtimes view, where `s` already means "a shell on this machine". It
  goes to that runtime's own endpoint (`--host` with the row's socket),
  whichever daemon dockmaster is showing, and is refused while the runtime
  is stopped. Colima and Podman rows keep their ssh: it needs no image and
  no privileged container.

## Safety: this is root on the host

- **Readonly refuses it.** `confirm_host_shell` and `runtime_shell` are in
  `mutating`, so `--readonly`, the top-level `readOnly` and a context's
  `readOnly` all refuse it before anything is asked. The confirmed run
  checks again, because a live reload (`ui.reactive`) can turn readonly on
  while the confirm is up.
- **It always confirms**, naming the daemon (context and endpoint, or the
  runtime), the image, that the image is pulled if missing, and that every
  command runs as root on that host:

  > open a ROOT shell on the host of prod (tcp://10.0.0.5:2376)? a
  > privileged alpine:3 helper (pulled if missing) enters the host's
  > namespaces; every command runs as root there

  The image and the endpoint are fixed when the question is asked, so a
  reload cannot make yes run something other than what was named.
- **A rootless daemon is refused with a reason.** Rootless docker and
  rootless podman both report `name=rootless` in `/info`'s security
  options (`Client.Rootless`, read by `Negotiate`). There the helper runs
  in a user namespace, without `CAP_SYS_ADMIN` over the host's own
  namespaces, so `nsenter`'s `setns` into PID 1's cannot succeed — the
  user would get a privileged-looking container and an error. dockmaster
  says so instead, and points a
  Podman machine at `s` in the runtimes view, which is ssh. A rootful
  Podman machine's socket is not refused: its VM's PID 1 is a fine target.

## Tests

`hostshell_test.go` runs the real command against a fake `docker` on PATH
that prints each argument, and checks the whole argv — the endpoint flags,
then the helper, with an image full of shell metacharacters arriving as one
argument. It covers the confirm text, that only yes runs, readonly from
each source, rootless and flag-image refusals, and each runtime kind:
Docker Desktop gets the host shell on its own socket, Podman and Colima
still run their ssh with no confirm, a stopped runtime is refused. No test
starts a privileged container.
