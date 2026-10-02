# Security Policy

## What dockmaster can do

dockmaster is a client for the Docker Engine API. It acts with exactly the
access your docker socket or context grants, which on most machines is
root-equivalent on the daemon's host. It adds no privilege of its own, and it
takes none away except where it says so:

- `--readonly` refuses every action that changes daemon state — and deleting
  a saved dump — in one place (`internal/tui/actions.go`). It is a guard
  against slips, not a sandbox: dockmaster still holds the same socket.
- Plugins (`plugins.yaml`) run the commands **you** configure, with your
  privileges. Their `$VARS` are expanded by dockmaster into separate
  arguments and run without a shell, so a container or image name cannot
  inject a shell command — but a plugin that hands its arguments to `sh -c`
  gives up that protection.
- Browsing a volume starts a short-lived helper container with the volume
  mounted read-only and no network.
- Container output — logs, inspect, healthcheck results, files read from a
  volume — is untrusted. dockmaster strips terminal control sequences other
  than color before drawing it.

## Supported versions

Only the latest release receives fixes.

## Verifying a release

Releases are signed with [cosign](https://github.com/sigstore/cosign) keyless
signing: the signature is tied to the GitHub Actions workflow that built the
release, not to a key someone could leak.

`checksums.txt` is signed; it lists the digest of every archive. Verify the
signature, then the archives against it:

```sh
VERSION=v0.0.0
cosign verify-blob \
  --certificate-identity "https://github.com/blairham/dockmaster/.github/workflows/release.yml@refs/tags/$VERSION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle checksums.txt.sigstore.json checksums.txt
sha256sum --check --ignore-missing checksums.txt
```

## Reporting a vulnerability

**Do not open a public issue.** Report it privately through GitHub:
[Security → Report a vulnerability](https://github.com/blairham/dockmaster/security/advisories/new).

Please include the affected version or commit, what an attacker can do, and
the steps to reproduce. You should receive a response within a week.

In scope, among others:

- any action that changes daemon state, or deletes a file, while `--readonly`
  is on
- a container, image or volume name, label or log line that makes dockmaster
  run a command, or makes a plugin's arguments split or expand differently
  from what `plugins.yaml` says
- terminal escape sequences from container output that reach the terminal —
  moving the cursor, setting the title, writing to the clipboard
- the volume browser's helper container getting a network, a writable mount,
  or a mount other than the volume asked for
- `:sd` reading or deleting anything outside dockmaster's own dump
  directories

Out of scope: anything that requires already controlling your docker socket,
your config directory, or the commands your plugins run.
