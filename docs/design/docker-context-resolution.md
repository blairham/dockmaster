# Design: Docker context resolution

**Status:** Living
**Code:** `internal/docker/context.go`, `internal/docker/endpoint.go`

## Purpose

To explain why dockmaster reimplements a piece of the docker CLI, and what
breaks if this file is ever "simplified" back to `client.FromEnv`.

## The problem

The Docker Go SDK's `client.FromEnv` reads the environment — `DOCKER_HOST`,
`DOCKER_API_VERSION`, `DOCKER_CERT_PATH` and `DOCKER_TLS_VERIFY` — and nothing
else. It does **not** read docker *contexts*.

But on a large share of developer machines the daemon endpoint lives *only* in
the context store:

- Colima → `unix://~/.colima/default/docker.sock`
- Rancher Desktop, Podman, and any remote/ssh context → likewise

On those machines `DOCKER_HOST` is empty, so an SDK program falls back to
`/var/run/docker.sock`, finds nothing there, and reports *"Cannot connect to
the Docker daemon. Is the docker daemon running?"* — immediately after
`docker ps` worked fine in the same shell. The error actively points the user
at the wrong problem.

This was not hypothetical: it is exactly what dockmaster did on the first
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
real value, because a drift here fails **silently**: dockmaster would find no
entry, fall back to the default socket, and declare a live daemon dead. That
is the worst failure shape available, so it gets a test rather than a comment.

## More than the host

A context's endpoint is more than its host, and the SDK, given only the host,
gets the rest wrong (#33, #34). `endpoint.go` does what the CLI does:

- **TLS.** `docker context create --docker host=tcp://…,ca=…,cert=…,key=…`
  copies the files into the store, under
  `contexts/tls/<digest>/docker/{ca,cert,key}.pem`, and records
  `SkipTLSVerify` in the metadata. `contextTLS` builds the CLI's TLS
  configuration from them: a `ca.pem` *replaces* the system roots, the
  client certificate is presented when both halves are there, and
  verification is on unless `SkipTLSVerify`. No files and no `SkipTLSVerify`
  is plain TCP, as in the CLI. Without this a TLS context was spoken to in
  plain HTTP unless `DOCKER_CERT_PATH` / `DOCKER_TLS_VERIFY` happened to be
  exported — and then without verifying the daemon unless the latter was.
- **ssh.** The SDK has no ssh transport: an `ssh://user@host` host was looked
  up as the hostname `user@host`. The CLI's connection helper runs
  `ssh -o ConnectTimeout=30 -T -l user -p port -- host docker system
  dial-stdio` and speaks HTTP over the process's stdin and stdout;
  `sshDialer` does the same (a path in the URL becomes `docker --host
  unix://<path>` on the far side). One ssh process per pooled connection,
  ended when the connection closes. ssh's own complaint — "Permission
  denied", "Could not resolve hostname" — is what the error says, rather
  than "EOF". Authentication is ssh's: keys and agent, as for the CLI.

The TLS material is found by context *name*, so every place that dials a
context by name goes through `NewForContext(name, host)`: `--context`, `:ctx`
and the picker. A runtime profile that shares a context's name but dials
elsewhere keeps its own host. The tests run a TLS daemon that demands a
client certificate, and a fake `ssh` on `PATH` whose far end is the test
binary itself serving the Engine API on stdio.

## Downstream

Because the endpoint is resolved rather than assumed, two other things have to
follow it:

- **The context switcher** (`:ctx`, or the picker) rebuilds every view against
  a freshly dialed client rather than mutating the old one. Each view caches
  its own rows; showing one daemon's containers under another daemon's name,
  even for a single tick, is how the wrong container gets killed.
- **`s` (shell into a container)**, `A` (attach) and the compose actions pass
  `Client.EndpointArgs` to the `docker` CLI: `--host`, or `--context <name>`
  for a context with TLS material, whose certificates `--host` would leave
  behind. Without it, switching context inside dockmaster and then pressing
  `s` would exec against whatever context the *shell* has — possibly a
  different machine.
- **Still the host alone:** the image scanner (`v`, which hands `grype` /
  `trivy` `DOCKER_HOST`), kind (`K` in the runtimes view) and a plugin's
  `$DOCKER_HOST`. Against a TLS context those need the TLS variables
  exported; a plugin can use `--context $CONTEXT` instead.
