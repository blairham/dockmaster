# Design: Compose projects

**Status:** Living
**Code:** `internal/docker/compose.go`, `internal/docker/util.go` (`Projects`), `internal/tui/views/projects.go`, `internal/tui/compose.go`

## Projects are labels

There is no Engine API for Compose. A project exists only as labels the
Compose CLI stamps on what it creates, so the projects view folds the
container list by `com.docker.compose.project`. A project whose containers
have all been removed (`compose down`) is gone from the daemon and from the
view — the same as `docker compose ls`.

## Verbs run the Compose CLI

`up`, `down`, `restart` and `pull` are client-side operations over a compose
file. The only faithful way to run them is the Compose CLI against the same
files the project was brought up with, and the labels record exactly that:

| Label | Becomes |
|---|---|
| `com.docker.compose.project` | `-p` |
| `com.docker.compose.project.working_dir` | `--project-directory` |
| `com.docker.compose.project.config_files` (comma-separated) | one `-f` each |
| `com.docker.compose.project.environment_file` (comma-separated) | one `--env-file` each |

Every invocation also passes `--host`, so compose acts on the daemon dockmaster
is showing rather than the shell's current context.

| Key | Verb | Confirms |
|---|---|---|
| `u` | `compose up -d` — creates what is missing, recreates what changed | no |
| `e` | opens the compose files in the editor, then `compose up -d` if they changed | no |
| `R` | `compose restart` | no |
| `p` | `compose pull` | no |
| `s` | a form for a service and its count, then `compose up -d --scale svc=N --no-recreate svc` | when it removes containers |
| `ctrl-d` | `compose down` — containers and networks; **volumes are kept** | yes |
| `x` | stops each container | yes |

All are actions in `handleAction`, so `--readonly` refuses them.

## When the files are not here

The paths are on the machine that ran `up`. On a remote daemon, or after a
checkout has moved, they do not exist, and running compose would fail or —
worse — act on whatever now sits at that path. `Project.MissingFile` checks
every compose and env file first, the COMPOSE FILE column says "not on this
machine", and the verbs fall back to what the Engine API can do on its own:

- `u` starts the existing containers, and says so;
- `R` restarts them one by one;
- `ctrl-d` asks to force-remove them one by one;
- `p` refuses, with the reason — there is nothing to pull without a file;
- `e` refuses too — there is no file to edit;
- `s` refuses, with the reason — only compose knows how to make another
  replica of a service, so there is nothing to fall back to.

## Scaling

`s` is k9s's scale. A form asks for the service — a choice over the
services the project's containers carry in `com.docker.compose.service` —
and the replicas, prefilled with that service's current container count.
The count includes stopped containers, because `--scale` counts them too:
a service of three with one stopped is at 3, not 2. Replicas must be a
whole number, 0 or more; anything else stays in the form with the reason.
On `:xray` the same form opens from a project node, or from a service node
already on that service.

Submitting runs

    docker <endpoint> compose <project args> up -d --scale <svc>=<N> --no-recreate <svc>

Naming the service keeps the rest of the project as it is — a bare `up`
would also create or recreate every other service that drifted — and
`--no-recreate` keeps the containers it leaves from being recreated when
their definition changed: a scale is not an update.

A scale below the current count removes containers, so it confirms,
naming the service and both counts ("scale web in shop from 3 to 1? 2
containers are removed"); to 0, the prompt says every container goes. A
scale up, or to the same count, removes nothing and runs directly. The
project's busy guard applies as for the other verbs, rechecked on submit.

## Editing

`e` is `kubectl edit` for a project: every compose file the labels record
opens in `$VISUAL`, else `$EDITOR`, else `vi` (split on spaces, so
`code --wait` works), with the terminal handed over as `s` hands it to a
shell. When the editor exits, `up -d` applies the edit — compose recreates
only the services whose definition changed.

Nothing runs unless the edit is one the user meant to keep:

- the files are fingerprinted by **content** before and after, so quitting
  without saving, or saving the same bytes, is "no changes" — an mtime
  would call a plain `:w` a change and recreate nothing for no reason;
- an editor that exits non-zero (vim's `:cq`) abandons the edit, even if
  it wrote the file first, as `git commit` treats it.

An edit that makes the file invalid is not caught up front: `up` fails and
its reason is the flash, and the file stays as written for the next `e`.

## Long-running

`up` pulls and builds what it needs, so compose calls get ten minutes, and
the project's row shows `starting…` / `pulling…` / `removing…` until the call
returns. A second lifecycle key — scale included — on a busy project is refused. compose's
failure reason is the last line of its stderr, and that is what the flash
shows.
