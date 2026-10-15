# Design: Local registry

**Status:** Living
**Code:** `internal/docker/localregistry.go`, `internal/docker/clusters.go` (`Registry`, `EnsureRegistry`),
`internal/registry/`, `internal/tui/localregistry.go`, `internal/tui/views/registries.go`,
`internal/tui/views/registrycatalog.go`

A local registry makes `build → push → pull/run` work without Docker Hub, and
it is what kind and k3d clusters pull from. Docker Desktop, colima and OrbStack
ship none; people run `registry:2` beside the daemon, and that is what
`:registry up` does (#120).

## One registry, not two

`:registry up` (`u` in `:registries`) is `Client.UpLocalRegistry`. It lists the
registry containers on the daemon first (`Client.LocalRegistries`: dockmaster's
labeled one, kind's `kind-registry` or a `registry` image on the `kind`
network, k3d's `k3d.role=registry`, and any other `registry` image) and reuses
one rather than starting a second:

1. the container with `localRegistry.name`;
2. else dockmaster's own (`dockmaster.registry=local`) under another name;
3. else the first that publishes a port — a kind or k3d cluster's registry,
   or one the user ran by hand.

A stopped one is started. Only when there is none does it create
`LocalRegistrySpec(localRegistry.name, localRegistry.port)` through
`EnsureRegistry` — the same function the kind flow (`K` in runtimes) uses,
generalized: `Registry` gained `Volume`, `Labels` and `Env`, which kind's
`DefaultRegistry` leaves empty, so the kind registry is created exactly as
before.

dockmaster's registry is `registry:2`:

- named `dockmaster-registry`, published on `127.0.0.1:5000` only
  (`localRegistry.name`, `localRegistry.port`);
- its storage in the named volume `<name>-data`, so images survive a removed
  container and `:registry up` brings them back;
- labeled `dockmaster.registry=local`, `restart=unless-stopped`;
- with `REGISTRY_STORAGE_DELETE_ENABLED=true`, without which a registry answers
  every manifest DELETE with 405.

⚠️ macOS's AirPlay Receiver listens on port 5000. When it is on, the publish
fails with "address already in use"; set `localRegistry.port` (kind uses 5001
for the same reason).

`ctrl-d` removes the container — `docker rm -f`, never the volume — after a
confirm, and only dockmaster's own: a cluster's registry belongs to the
cluster, and removing it breaks the nodes' pulls. `x` stops any registry
container. All of start, stop, remove, garbage collection and tag delete are
in `mutating`; browsing is not.

## Browsing: the v2 API

`internal/registry` is a plain `net/http` client for the distribution API —
no bubbletea, no docker daemon. `enter` on a row of `:registries` opens
`views.RegistryCatalogView` (`GET /v2/_catalog`, every page by its `Link`
header), `enter` on a repository `views.RegistryTagsView`
(`GET /v2/<repo>/tags/list`, then each tag's manifest — asking for an OCI
index or a Docker manifest list first, so a multi-platform tag answers with
its index, whose digest is the tag's — and its config blob for the created
time). SIZE is the compressed size the registry stores, config plus layers;
for an index, its first platform's, and PLATFORMS lists them (attestations,
`unknown/unknown`, left out). Eight tags are read at a time. Neither view is
polled: it is a manifest and a config per tag.

`ctrl-d` on a tag deletes its manifest **by digest**
(`DELETE /v2/<repo>/manifests/<digest>`) — the API has no delete by tag, so
every tag on that digest goes with it, which the confirm says. The blobs stay
until `P`, which runs `registry garbage-collect --delete-untagged
/etc/docker/registry/config.yml` in the container (`docker exec`) after a
confirm, and reports how many blobs it deleted. A remote registry has no
container here, so `P` there says so.

### Authentication

A request that comes back 401 is answered once from its `WWW-Authenticate`
challenge, with the docker config's credential for the registry
(`docker.ResolveCredential` — the one `:login` stored, through the same
credential helper a push uses):

- `Basic` — the username and password on the retry;
- `Bearer` — a token from the challenge's realm for the challenge's scope:
  a GET with the credential as basic auth, or, for an identity token, the
  OAuth2 refresh-token POST. With no credential the realm is asked
  anonymously, which is how public repositories read.

Tokens are kept per scope for the client's life. The credential is read only
when a registry asks, so browsing the local registry never runs a helper.

## Which address

An image reference names the local registry as `localhost:<port>`, and that
is where the **daemon** pushes and pulls it — the port is published on the
daemon's host. dockmaster's own HTTP client is on *this* machine, so it uses
the port the daemon reports for the container (`NetworkSettings.Ports`, else
`HostConfig.PortBindings` for a stopped one) and dials `127.0.0.1:<port>`
over plain HTTP, as the daemon treats loopback registries as insecure. Any
other registry is HTTPS.

- **Docker Desktop, colima, OrbStack, Rancher Desktop:** the registry runs in
  the VM, and the runtime's port forwarding makes `127.0.0.1:<port>` on the
  Mac the same registry. Browsing and pushing agree.
- **A remote context (`ssh://`, `tcp://`):** the registry is published on the
  remote host's loopback, which this machine cannot reach. Pushes still work —
  the daemon pushes to its own `localhost` — but the browser needs a tunnel:
  `ssh -L 5000:127.0.0.1:5000 <host>`, then `:catalog localhost:5000`.

## Tests

`internal/registry` runs against `httptest` fakes of the v2 API (pages, an
index with an attestation, 405 on delete, Basic and Bearer challenges);
`internal/docker` against the fake daemon; the TUI through seams
(`views.ListLocalRegistries`, `localRegistryUpFn`, `registryStopFn`,
`registryRemoveFn`, `registryGCFn`) plus an `httptest` registry for browsing.
No test starts a registry container.
