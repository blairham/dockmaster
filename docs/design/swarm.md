# Design: Swarm

**Status:** Living
**Code:** `internal/docker/swarm.go`, `internal/tui/views/swarm.go`, `internal/tui/swarm.go`

## Purpose

k9s's Kubernetes resources have Swarm counterparts on a daemon that
manages a swarm: services (deployments), tasks (pods), nodes, secrets and
configs. dockmaster lists each as a top-level view (#64) and carries the
verbs an operator reaches for — scale, restart, drain, remove — through the
same `handleAction` gate as every other mutation.

| View | Command | As |
|---|---|---|
| Services | `:services`, `:svc` | `docker service ls` |
| Tasks | `:tasks`, `enter` on a service or node | `docker service ps` |
| Nodes | `:nodes`, `:no`, `:swarm` | `docker node ls` |
| Secrets | `:secrets`, `:sec` | `docker secret ls` |
| Configs | `:configs`, `:cm` | `docker config ls` |

## One view, five kinds

The five lists differ only in their columns, cells, filter fields and keys,
so `views.SwarmView` serves them all, keyed by `views.SwarmKind`. Each kind
still has its own `style.ViewType`, its own `views.yaml` key (`services`,
`tasks`, `swarmnodes`, `secrets`, `configs`) and its own row in the help, so
to the user and to config they are five views. `SwarmRow` holds one typed
value per kind rather than an `any`, so a cell function cannot read the
wrong one by a type assertion that compiles.

`SwarmRefreshMsg` carries its kind and is routed to that kind's view
whichever view is showing (`App.handleSwarmRefresh`), not to the active
view: a listing that lands after its view was left must still clear the
view's single-flight guard, or that view never polls again.

## Off a manager

Only a manager answers these endpoints. On any other daemon the views show
the manager's own error ("This node is not a swarm manager…"), which names
the fix; dockmaster does not pre-empt it with a check of its own, so a
daemon that joins a swarm mid-session lists on the next poll.

## Writes are read-modify-write

A service or node update carries the object's `Version`, and the manager
refuses one based on a stale version. `Client.updateService` and
`SetNodeAvailability` therefore inspect, change the spec in place, and post
it back at the version they read — so a scale keeps every other field of
the spec as the manager has it, and a concurrent change elsewhere is a
refusal, not a silent overwrite.

- **Scale** (`s`): `docker service scale`. Only a replicated service has a
  count; a global service or a job is refused in the view
  (`not_scalable`) before a form opens, and `ScaleService` refuses again
  with `ErrNotScalable`. The form is the compose scale form
  (`views.NewServiceScaleForm`), which returns `service_scale` instead of
  `compose_scale`; a count below the current one confirms.
- **Restart** (`R`): `docker service update --force` — `ForceUpdate`
  incremented, nothing else changed — so the manager replaces every task
  under the service's own update policy. It does not confirm, as a
  container's `R` does not.
- **Availability**: `u` active and `p` pause run at once; `x` drain
  confirms, since it stops the node's tasks and reschedules them.
- **Remove**: a service, a node, a secret or a config, each after a
  confirm. A node is removed without `--force`, so the manager refuses one
  still up; a secret or config in use is refused by the manager too.

Every one of these is in `mutating`, so `--readonly` refuses it before a
form or confirm opens. Inspect and logs are not.

## Logs

`l` on a service and `enter` / `l` on a task tail
`/services/{id}/logs` and `/tasks/{id}/logs` through the manager
(`Client.StreamSwarmLogs`), so a task on another node streams as well as a
local one. The body is framed as a non-TTY container's unless the service
runs with a TTY, which the stream checks from the spec, sharing
`demuxLogs` with container logs. A swarm log has no container to stop or
restart; its `o` inspects the service or task.

## Tasks and history

The manager keeps a task's history in its slot — every replaced or failed
task. `docker service ps` shows it all; the tasks view shows only the tasks
meant to be running (desired state running, ready or accepted) and `a`
shows the history after each live task, as the containers view's `a` shows
stopped containers. A task is named `service.slot`, or `service.node` for a
global service, as the CLI names it, with service and node names from one
listing of each per refresh.

## Not here

Creating services, secrets and configs, `stack deploy`, rollback, and
promoting or demoting nodes. Each is a form of its own and was not asked
for; the views and the read-modify-write helper are where they would go.
