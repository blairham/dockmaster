# Design: Daemon latency and the polling model

**Status:** Living
**Code:** `internal/tui/views/*.go` (the `inFlight` guards), `internal/docker/stats.go`

## Purpose

k9s polls the Kubernetes API every two seconds and it is fine. The Docker
daemon is not the Kubernetes API. This note records what was measured and what
the numbers forced.

## Measurements

Taken on a Colima host during development. Each was as slow through the
`docker` CLI as through the SDK, so the cost is daemon-side, not client-side:

| Call | Time |
|---|---|
| `GET /_ping` | instant |
| `docker ps` (7 containers) | ~6 s |
| `docker images` (651 images) | **did not complete — timed out at 300 s on two separate clean runs** |

## Consequences

**1. Every list view single-flights its refresh.** Each holds an `inFlight`
flag; `refresh()` returns `nil` while a request is outstanding, so the poll
tick is a no-op rather than a second request. Without this, a 6-second `ps` on
a 3-second tick queues a new request every tick forever, and the socket
eventually refuses connections — a failure that looks like the daemon dying.

**2. The poll interval is 3 s, not 2 s**, and timeouts are per-call rather than
global: 20 s for containers and networks, 60 s for volumes, and
`imageListTimeout` = **5 minutes** for images, with a comment saying why.

**3. The CPU/MEM sampler is opt-outable.** It costs one blocking request per
running container per poll — by far the most expensive thing dockyard does —
so `<t>` turns it off and `--no-stats` starts it off. The fan-out is bounded
at 12 concurrent requests: they must be parallel (each blocks ~1 s server-side)
but 200 containers must not mean 200 simultaneous connections.

**4. Volume sizes are off by default.** The size/ref counts require the daemon
to walk each volume's tree. `<z>` turns them on and says in the flash that it
is slow.

## The stats formula

`sampleOne` uses `ContainerStats(ctx, id, false)` — the two-sample form, which
is what `docker stats --no-stream` uses. It does **not** use
`ContainerStatsOneShot`, despite the name being the obvious fit: one-shot
returns a **zeroed `PreCPU` block**, and docker's CPU formula against a zero
baseline produces numbers in the thousands of percent. `TestLiveStats` asserts
a plausible upper bound specifically to catch a regression back to it.

Memory subtracts `inactive_file` from the reported usage, as docker's own CLI
does. Without that, a container that merely read a large file appears pinned at
its memory limit forever.
