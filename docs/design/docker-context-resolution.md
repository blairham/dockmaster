# Design: Docker context resolution

**Status:** Living
**Code:** `internal/docker/context.go`

## Purpose

To explain why dockyard reimplements a piece of the docker CLI, and what
breaks if this file is ever "simplified" back to `client.FromEnv`.

## The problem

The Docker Go SDK's `client.FromEnv` reads exactly one thing: `DOCKER_HOST`.
It does **not** read docker *contexts*.

But on a large share of developer machines the daemon endpoint lives *only* in
the context store:

- Colima → `unix://~/.colima/default/docker.sock`
- Rancher Desktop, Podman, and any remote/ssh context → likewise

On those machines `DOCKER_HOST` is empty, so an SDK program falls back to
`/var/run/docker.sock`, finds nothing there, and reports *"Cannot connect to
the Docker daemon. Is the docker daemon running?"* — immediately after
`docker ps` worked fine in the same shell. The error actively points the user
at the wrong problem.

This was not hypothetical: it is exactly what dockyard did on the first
machine it was run on.

## The resolution order

`ResolveHost` mirrors the CLI's own precedence:

1. an explicit `--host`
2. `$DOCKER_HOST`
3. `$DOCKER_CONTEXT`, looked up in the store
4. `currentContext` from `~/.docker/config.json`, looked up in the store
5. the platform default socket

A missing or unreadable store is not an error — it means the default socket,
which is right for a stock Docker Engine install.

## The store layout

```
$DOCKER_CONFIG/contexts/meta/<hex sha256 of the context name>/meta.json
```

The directory name is the hex SHA-256 of the context name — `colima` hashes to
`f24fd374…dadd16`. `TestContextDigestMatchesDockerCLI` pins that against a
real value, because a drift here fails **silently**: dockyard would find no
entry, fall back to the default socket, and declare a live daemon dead. That
is the worst failure shape available, so it gets a test rather than a comment.

## Downstream

Because the endpoint is resolved rather than assumed, two other things have to
follow it:

- **The context switcher** (`:ctx`, or the picker) rebuilds every view against
  a freshly dialed client rather than mutating the old one. Each view caches
  its own rows; showing one daemon's containers under another daemon's name,
  even for a single tick, is how the wrong container gets killed.
- **`e` (shell into a container)** passes `--host` to the `docker` CLI. Without
  it, switching context inside dockyard and then pressing `e` would exec
  against whatever context the *shell* has — possibly a different machine.
