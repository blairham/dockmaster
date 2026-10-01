# Design: Kubernetes nodes (kind, k3d)

**Status:** Living
**Code:** `internal/docker/node.go`, `internal/tui/views/node.go`, node actions in `internal/tui/actions.go`

## The problem

A kind or k3d cluster is a handful of docker containers — its nodes — each
running its own containerd. Every pod's containers live in that inner
runtime, so `docker ps` (and dockyard's containers view) shows the nodes and
nothing in them. On 2026-10-01 a local rig was up and healthy, 34
containers deep inside `k8s-worker`, while dockyard showed three rows:
`k8s-control-plane`, `k8s-worker`, `kind-registry`. Polling more often
cannot help; the daemon does not have them.

## The drill-in

`docker.NodeRole` recognises a node by its labels: `io.x-k8s.kind.role`
(kind), `k3d.role` = `server`/`agent` (k3d; its load balancer and registry
have no runtime inside). In the containers view, `enter` on a node opens
the node view instead of the node's own logs (`l` still tails those).

The node view lists the inner containers — namespace, pod, name, state,
restarts, image, age — read with `crictl ps -a -o json` over a docker exec
into the node. No Kubernetes API or kubeconfig is involved: this is the
docker host's view of what is running on it, and it works for any cluster
whose nodes are containers. From a row:

| Key | Does | How |
|---|---|---|
| `enter` / `l` | logs | `crictl logs -f`, streamed into the ordinary logs view |
| `o` | inspect | `crictl inspect` |
| `s` | shell | `docker exec -it <node> crictl exec -it <id> sh` |

In a node container's logs, `x` and `R` are not offered: they would hand
the docker daemon a containerd ID it has never seen.

## Streams must not outlive the view

Closing a docker exec stream does not stop the process inside the
container, so a bare `crictl logs -f` would leave one orphan in the node per
log view ever opened. `nodeLogScript` runs crictl under a watcher that
kills it when the exec's stdin closes, and dockyard closes stdin when the
view's context is canceled. The watcher reads stdin through fd 3 because a
non-interactive `sh` gives background jobs `/dev/null` as stdin — the
first version saw EOF at once and killed crictl before it printed a line.
Verified against a live kind node: one `crictl` in the node while the view
is open, zero after `esc` (`pgrep -xc crictl`; `pgrep -f 'crictl logs'`
counts its own shell and reads 1 either way).
