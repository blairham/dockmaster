# Design: Port forwarding

**Status:** Living
**Code:** `internal/docker/portforward.go`, `internal/tui/portforward.go`, `internal/tui/views/portforwards.go`

## Why a helper container

k9s forwards a port with the Kubernetes API's port-forward stream. Docker has
no equivalent, and cannot publish a new port on a running container. So a
forward is a relay: a small `alpine/socat` container that joins the target's
network, publishes the local port, and relays to the target's address.

```
localhost:15000 ──► helper (socat, 127.0.0.1:15000 published) ──► target 172.18.0.4:5000
```

This reaches ports the target never published, which is the point.

## Choices that matter

- **Loopback by default.** The helper publishes on `127.0.0.1`, so a forward
  is reachable from this machine and nowhere else — the same as
  `kubectl port-forward`'s default. `TestForwardConfig` pins it.
  `portForwardAddress` (k9s's key) moves it; see [The listen
  address](#the-listen-address).
- **Labeled.** Every helper carries `dockmaster.portforward=*` labels (target ID
  and name, local and remote port, the address it listens on), so `:pf`
  lists forwards from the daemon alone, and they are told apart from user
  containers. A helper with no address label predates it and is on
  `127.0.0.1`.
- **AutoRemove.** Stopping a helper is the whole cleanup.
- **Forwards end with dockmaster**, as k9s's do: quitting stops the forwards
  this session started (bounded to 10 s, best effort). Helpers would
  otherwise outlive dockmaster and keep their ports.
- **The target's first network with an address.** Host- and none-networked
  containers are refused with the reason.
- The helper image is pulled on first use (a few megabytes).

## Colima

On Colima the daemon runs in a VM; Colima forwards ports bound in the VM to the
Mac's localhost, so `127.0.0.1` inside the VM is reachable as `localhost` on the
host. Verified end to end against a real `registry:2`: `GET
localhost:15000/v2/` → `200 {}`.

## Container labels: k9s's FastForwards

k9s reads two pod annotations: `k9scli.io/port-forwards` preselects the
forward in its dialog, and `k9scli.io/auto-port-forwards` starts it without
the dialog. dockmaster reads the same two as container labels — set them in
a compose file's `labels:` or with `docker run --label`:

| Label | Effect |
|---|---|
| `dockmaster.port-forwards` | `shift-f`'s prompt opens prefilled with the label's forwards |
| `dockmaster.auto-port-forwards` | `shift-f` asks a yes/no confirm of the label's forwards instead of the prompt; wins over the other |

The value is exactly what the prompt takes — `[local:]container-port`,
comma-separated for more than one (`8080:80,8443:443`; `5432` is
`5432:5432`) — optionally after k9s's `name::`, which must name this
container or its compose service (`web::8080:80`). A label on a container
names that container already, so the prefix is accepted for copying from a
k9s manifest, not needed. Docker has no named ports, so k9s's `bozo::http`
has no equivalent and is refused.

**A label is untrusted input.** Anyone who builds an image or writes a
compose file sets it, and it reaches dockmaster through the daemon. So:

- It is parsed by the prompt's own parser (`docker.ParseForwardSpecs`):
  ports 1–65535, a local port used once, at most 8 forwards and 256 bytes.
- A label that does not parse is **reported, never acted on**: `shift-f`
  opens the plain prompt, prefilled with the container's own first port —
  nothing from the label — and puts the error, which names the label and
  quotes its value with controls escaped, under it.
- **No label starts a forward without a yes.** The auto label skips typing
  the ports, not the confirm, which names the container, the forwards, the
  address, and the label that asked. k9s's auto annotation starts forwards
  with no dialog at all; dockmaster does not follow it there, because a
  forward opens a port on the user's machine and the label is not theirs.
- `--readonly` refuses `shift-f` before any label is read.

## The listen address

`portForwardAddress` in `config.yaml` (k9s's key and default) is the host
address the helper publishes on: an IP, or `localhost` for `127.0.0.1`.
Docker binds an address, not a name, so any other hostname is refused at
load, as are an address with a port and an IPv6 zone
(`config.ParseForwardAddress`).

Anything but loopback (`127.0.0.0/8`, `::1`) makes a forward reachable from
the network — `0.0.0.0` from every interface. The prompt then reads
`⇄ forward web on 0.0.0.0 ⚠ reachable from the network`, the auto confirm
says the same, and the started flash, `:pf` and the stop confirm name the
forward by its address (`0.0.0.0:8080 → web:80`) rather than `localhost`.
`:pf`'s URL is `localhost` for a forward on loopback or on every address,
and the address itself otherwise. The address is where the **daemon's**
host listens: on Colima that is inside the VM, which forwards to the Mac.

## Keys

| Where | Key | |
|---|---|---|
| containers | `shift-f` | forward: prompt `local:container` (comma-separated for more), prefilled from the label or the first TCP port; or a confirm, from the auto label |
| containers | `f` | that container's forwards: `:pf` scoped to it, named in the title (`esc` back; the scope lifts on leaving, as `U`'s) |
| containers | `b` | open the first published port in the browser |
| `:pf` | `b` / `enter` | open the forward |
| `:pf` | `ctrl-d` | stop it (confirms) |
