# Design: Architecture

**Status:** Living
**Code:** `internal/docker/`, `internal/tui/`

## Purpose

dockyard is a k9s-style TUI over the Docker Engine API. This note records the
layering and the two invariants that keep it from turning into nine views that
each know how to delete things.

## Layers

```
main.go            flags → resolve endpoint → dial → hand a *docker.Client to the TUI
   │
internal/docker    Engine API wrapper. Plain values and errors. NO bubbletea.
   │
internal/tui       root App model: message dispatch, view stack, mutations
   │
internal/tui/views one file per view; render + report intent, never mutate
   │
tuikit/chrome      the frame: top section, bars, help overlay, footer
```

`internal/docker` importing bubbletea would be the first step toward views
calling the daemon directly. It is also what lets `live_test.go` exercise the
whole API surface against a real daemon with no program loop running.

## Invariant 1 — views report intent, the app acts

`View.HandleKey(key) (action, param string)` returns a *request*. Nothing in
`internal/tui/views` calls a mutating client method.

The payoff is concentration. `--readonly` is this, in `handleAction`, once:

```go
if a.readonly && mutating[action] {
    a.errFlash = "readonly mode — ... refused"
    return a, nil
}
```

A view that called `client.RemoveContainer` directly would bypass it, and the
bypass would be invisible — the flag would still appear to work everywhere
else. The same concentration is what makes "every destructive action confirms"
checkable by reading one function instead of nine.

## Invariant 2 — one confirm path

Destructive actions do not run from `handleAction`. They open
`chrome.Confirm` with a question and park an `pendingAction`; the dispatch
closure runs `executeConfirmed` on yes. `TestConfirmBarOwnsEveryKey` pins the
ordering in `handleKey` that makes this safe: while a question is on screen,
**no** other key reaches a view — otherwise a stray `:` would open the command
palette on top of a pending "remove this container?" and silently discard it.

## The view stack

`switchView` (digit hotkeys, `:` commands) clears the stack and tears down
stoppable views. `pushView`/`popView` handle drill-ins and deliberately do
**not** tear everything down: escaping out of an inspect view back onto a log
tail must not kill the tail's stream. Only the view actually being left is
stopped.

## Compose projects are a label, not an endpoint

There is no Engine-API concept of a project. Compose is a client-side
convention expressed through `com.docker.compose.project` labels, so
`ProjectsView` folds the same container list the containers view uses
(`docker.Projects`), and drilling into a project is a *filtered containers
view* rather than a new data source. Containers with no project label form no
bucket — inventing a "(standalone)" project would make dockyard's count
disagree with `docker compose ls`.
