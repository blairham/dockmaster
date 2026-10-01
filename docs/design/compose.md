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
| `R` | `compose restart` | no |
| `p` | `compose pull` | no |
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
- `p` refuses, with the reason — there is nothing to pull without a file.

## Long-running

`up` pulls and builds what it needs, so compose calls get ten minutes, and
the project's row shows `starting…` / `pulling…` / `removing…` until the call
returns. A second lifecycle key on a busy project is refused. compose's
failure reason is the last line of its stderr, and that is what the flash
shows.
