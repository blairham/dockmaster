# Design: Colima profile management

**Status:** Living
**Code:** `internal/colima/`, `internal/tui/views/colima.go`, `internal/tui/views/colimaform.go`, `internal/tui/colima.go`, the startup fallback in `main.go`

## Purpose

On a Colima host the docker daemon runs inside a VM, and the VM is a thing you
start, stop and resize. Every other view in dockmaster manages objects *in* the
daemon; this one manages the machine *under* it. That makes it the one view
that keeps working when the daemon is gone — which is exactly when it is
needed.

## Shelling out to the CLI

`internal/colima` drives the `colima` binary rather than lima or colima's
state files. colima owns profile layout, provisioning and the docker context
it registers; its CLI is the only interface to that which is meant to be
stable. Like `internal/docker`, the package imports no bubbletea, and its
`Runner` is swappable so tests never start a VM.

Two shapes of colima's output are load-bearing:

- **`colima list --json` is NDJSON** — one object per line, not an array. A
  decoder expecting an array parses nothing, which reads exactly like a
  machine with no profiles. `TestListParsesOneObjectPerLine` pins it.
- **Every failure is a logrus line**: `time="…" level=fatal msg="…"`.
  `FatalMessage` extracts the `msg` so the flash says `colima: error starting
  vm: …` rather than a timestamp.

`colima delete` is called with `--force`: colima's own y/n prompt has no
terminal to ask on and would hang until the timeout. dockmaster's confirm bar is
the prompt.

## Profile ↔ endpoint

A profile's docker socket is `<colima home>/<profile>/docker.sock`, with the
home at `$COLIMA_HOME` or `~/.colima`. The socket path exists whether or not
the VM is running, so it is derived rather than looked up in the docker
context store. `ProfileForHost` is the inverse, and it is how dockmaster knows
that a daemon it cannot reach is a colima VM it could start.

colima registers the docker context `colima` for the default profile and
`colima-<name>` for the rest; the CONTEXT column shows which.

## Starting when the daemon is down

Without colima support, dockmaster refuses to start when the daemon does not
answer — a one-line error on stderr beats an alt-screen of empty tables. That
still holds, with one exception: when the unreachable endpoint is a colima
profile's socket, dockmaster opens on the colima view with a notice instead,
because starting the VM is the fix and dockmaster can do it.

## Lifecycle

| Key | Verb | Confirms |
|---|---|---|
| `n` | new profile (form) → `colima start <p> --cpus … --memory … --disk … --runtime …` | no |
| `e` | edit resources (form) → `colima start <p> --cpus … --memory … --disk …`, after `colima stop <p>` if running | only if running |
| `u` | `colima start <p>` | no |
| `x` | `colima stop <p>` | yes |
| `R` | `colima restart <p>` | yes |
| `ctrl-d` | `colima delete --force <p>` | yes |
| `s` | `colima ssh --profile <p>` (terminal handed over) | no |
| `enter` | connect dockmaster to the profile's daemon | no |

Stop and restart confirm, unlike their container counterparts: a profile is
the VM every container in it runs in, so either takes all of them down. The
prompt also says when the profile is the one dockmaster is connected to, since
then every other view goes dark too.

All of them are actions in `handleAction`, so `--readonly` refuses them in the
same single check as every container mutation.

## Creating and editing

`n` and `e` open a form — Name, CPUs, Memory (GiB), Disk (GiB), Runtime. There
is no colima "create" or "set" verb: `colima start <name>` with resource flags
creates a profile that does not exist, and on an existing one replaces its
saved configuration (`--save-config` defaults on). colima reads resources only
at start, so:

- editing a **stopped** profile starts it with the new resources;
- editing a **running** one stops it first, which takes every container in it
  down — that path confirms, and reconnects dockmaster if it was the daemon.

The form validates before anything reaches colima: names colima accepts as a
directory and a context name, not already taken; CPUs no more than this
machine has; memory at least 0.5 GiB; **disk never smaller than it is** —
colima can grow a disk and cannot shrink one. On edit, name and runtime are
locked: a profile cannot be renamed, and a different runtime is a different
profile. An unchanged edit is refused rather than restarting a VM for nothing.

**The form captures input.** The app normally takes digits, `r`, `/` and `:`
before a view sees them; a view implementing `views.InputCapturer` gets every
key but esc and ctrl-c instead, so `dev1` can be typed as a name.
`TestColimaCreateProfile` types one.

**Lifecycle calls get ten minutes**, not `actionTimeout`'s sixty seconds. A
first `colima start` provisions a VM, and abandoning one that was about to
succeed is worse than waiting.

**Busy state is tracked by dockmaster, not read from colima.** For most of a
start `colima list` still says `Stopped`; without a marker the row would claim
nothing is happening. The row shows `starting…` until the call returns, and a
second lifecycle key on a busy profile is refused.

## Reconnecting

Starting or restarting the profile dockmaster is connected to replaces the
docker client and rebuilds the docker views — the same path as a context
switch, but leaving the user on the colima view. The old client negotiated
against a daemon that no longer exists, and the views hold rows from before
it went away.

## Polling

The colima view is in the polled set: a profile's state changes on its own
during a start, and `colima list` is a local read that never touches the
daemon.

Its listing is delivered to the colima view whichever view is active. The
other views receive refreshes only while active; for one that polls a
minute-long operation, a listing landing after the user had moved away would
be dropped, its single-flight guard would never clear, and the view would
never refresh again. `TestColimaListingLandsWhileAway` pins this.
