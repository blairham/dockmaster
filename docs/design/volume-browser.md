# Design: Volume browser

**Status:** Living
**Code:** `internal/docker/volbrowse.go`, `internal/tui/views/volbrowse.go`

## Why a helper container

Docker has no API for the files in a volume. `enter` on a volume therefore
runs a short-lived helper for each directory listing: the port-forward
image (`alpine/socat`, already pulled once a forward has run), with the
volume mounted **read-only** at `/v` and **no network**, running
`find -mindepth 1 -maxdepth 1 -exec stat` over the directory. The helper
is created, run, read and force-removed on every listing, so nothing is
left behind; a listing takes about half a second.

The path never enters the script text — it is passed in `$DMPATH`, and
`VolumePath` cleans it first so `..` cannot climb out of the volume.

## Navigation

The app keeps one view per type, so the browser walks directories **in
place**: `enter` on a directory lists it, `esc` (or `q`) climbs one level
and leaves the view only at the volume's root (`views.Backer`). A listing
that arrives for a directory already left is dropped.

`enter` on a file shows its first megabyte in the text viewer, control
characters made safe; a file with a NUL byte is reported as binary
instead. `o` on a volume still inspects it.

## Readonly

Browsing works under `--readonly`: the volume is mounted read-only, and
the only daemon state touched is the helper, which is removed.
