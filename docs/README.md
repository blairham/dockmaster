# dockyard docs

| Dir | What |
|---|---|
| [`design/`](design/) | Per-subsystem living documents. One per area, each with a `Status:` and `Code:` header. |

Current design notes:

- [`design/architecture.md`](design/architecture.md) — the layering, the view contract, and why mutations live in one place.
- [`design/docker-context-resolution.md`](design/docker-context-resolution.md) — why dockyard reads the docker context store itself.
- [`design/daemon-latency.md`](design/daemon-latency.md) — measured daemon call costs and the polling design they forced.
