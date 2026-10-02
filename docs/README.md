# dockmaster docs

| Dir | What |
|---|---|
| [`design/`](design/) | Per-subsystem living documents. One per area, each with a `Status:` and `Code:` header. |
| [`images/`](images/) | The README screenshots. Generated — `make screenshots` re-renders them; never hand-edit. |
| [`demo/`](demo/) | What `make screenshots` stages (`compose.yaml`) and plays (`screenshots.tape`). |

Current design notes:

- [`design/architecture.md`](design/architecture.md) — the layering, the view contract, and why mutations live in one place.
- [`design/docker-context-resolution.md`](design/docker-context-resolution.md) — why dockmaster reads the docker context store itself.
- [`design/daemon-latency.md`](design/daemon-latency.md) — measured daemon call costs and the polling design they forced.
- [`design/port-forward.md`](design/port-forward.md) — forwarding through a socat helper, since Docker cannot add a port to a running container.
- [`design/compose.md`](design/compose.md) — compose verbs from labels, and the fallback when the files are elsewhere.
- [`design/runtimes.md`](design/runtimes.md) — the runtimes view: Colima, Podman, Docker Desktop, Rancher Desktop, OrbStack behind one interface.
- [`design/colima.md`](design/colima.md) — managing the Colima VMs under the daemon, and starting dockmaster when one is down.
- [`design/config.md`](design/config.md) — config.yaml: the k9s-shaped schema, where it lives, and flag precedence.
- [`design/kubernetes-nodes.md`](design/kubernetes-nodes.md) — kind/k3d nodes: listing the pods' containers inside a node with crictl.
