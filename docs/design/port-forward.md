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

- **Loopback only.** The helper publishes on `127.0.0.1`, so a forward is
  reachable from this machine and nowhere else — the same as
  `kubectl port-forward`'s default. `TestForwardConfig` pins it.
- **Labeled.** Every helper carries `dockmaster.portforward=*` labels (target ID
  and name, local and remote port), so `:pf` lists forwards from the daemon
  alone, and they are told apart from user containers.
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

## Keys

| Where | Key | |
|---|---|---|
| containers | `shift-f` | forward: prompt `local:container`, prefilled with the first TCP port |
| containers | `b` | open the first published port in the browser |
| `:pf` | `b` / `enter` | open the forward |
| `:pf` | `ctrl-d` | stop it (confirms) |
