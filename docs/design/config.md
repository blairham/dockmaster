# Design: config.yaml

**Status:** Living
**Code:** `internal/config/config.go`, `internal/config/flags.go` (flag layering, `dockmaster config`)

## Shape

The file follows k9s's `config.yaml`: a single top-level key, `dockmaster:`
here and `k9s:` there, with the same camelCase key names where the settings
match (`refreshRate`, `readOnly`, `requestTimeout` — k9s's `apiServerTimeout` — `ui.headless`, `ui.logoless`,
`ui.crumbsless`, `ui.splashless`, `ui.skin`, `ui.invert`, `logger.tail`, `logger.buffer`,
`logger.sinceSeconds`, `logger.showTime`, `logger.textWrap`,
`logger.disableAutoscroll`, `ui.enableMouse`, `ui.defaultsToFullScreen`,
`noExitOnCtrlC`, `screenDumpDir`, `liveViewAutoRefresh`). Settings
k9s has no equivalent for (`showAll`, `noStats`, `context`) follow the same
spelling. `dockmaster config init` writes the commented defaults
(`config.Sample`); `TestSampleIsTheDefaults` keeps that sample in step with
`config.Default()`.

## Where it lives

`$DOCKMASTER_CONFIG_DIR/config.yaml`, else `$XDG_CONFIG_HOME/dockmaster/config.yaml`,
else `~/.config/dockmaster/config.yaml`. `dockmaster config path` prints the
answer. k9s's own default on macOS is `~/Library/Application Support`;
dockmaster uses `~/.config` on every platform, where command-line tools keep
their dotfiles.

## Precedence

Defaults, then the file, then flags **set on the command line**.
`flag.Visit` sees only the flags that were given, so a flag left at its
default never overwrites the file: without that, `readOnly: true` would be
undone by every run that does not pass `--readonly`. A flag that is given
wins in both directions (`--readonly=false` turns off a `readOnly: true`).
`TestApplyFlagsOnlySetFlagsWin` pins both halves.

`context:` is a default `--context`. It applies only when none of `--host`,
`--context`, `$DOCKER_HOST` or `$DOCKER_CONTEXT` is set, so it sits between
those and the docker CLI's `currentContext` in the resolution order
(`docker-context-resolution.md`).

## requestTimeout

Each daemon request carries its own deadline — 20s for a list or inspect,
60s for an action, 5m for the image list and disk usage. `requestTimeout`
(and `--request-timeout`) replaces all of them with one value, through
`docker.Client.RequestContext`: longer for a slow but honest daemon, shorter
to fail fast on one that hangs. It does not touch log and event streams,
Compose, or the runtime CLIs (colima, podman), which are not single daemon
requests. `TestDaemonRequestsHonorRequestTimeout` fails if a view grows a
hand-rolled `context.WithTimeout`.

## Strict on purpose

An unknown key is an error naming the file and line (`unknown key
"readonly"`), and so is an out-of-range value (`refreshRate` below 1 second,
`logger.tail` outside 1–100000). A misspelled key that was silently ignored
would look exactly like a setting that had taken effect. A missing file is
not an error: everything is optional.

## thresholds

`thresholds.cpu` and `thresholds.memory` (warn/critical, k9s's 70/90 by
default) colour the containers view's CPU% and MEM columns orange and red.
The displayed CPU% stays `docker stats`'s per-core figure, which reaches
CPUs × 100; the threshold judges `Stats.CPUShare`, that figure spread over
the CPUs the container can use. Otherwise one saturated core on a 14-CPU
VM — CPU% 100, a 7% share — would read as critical. Memory is judged by
usage over the container's limit, which for an unlimited container is the
host's memory. The selected row is drawn in the selection style and shows
no threshold colour.

## Skins

`ui.skin` names a skin in `skins/` beside `config.yaml` (`<name>.yaml` or
`.yml`); `DOCKMASTER_SKIN` overrides it, as `K9S_SKIN` does k9s's. A skin
file is k9s's own format — colors under a top-level `k9s:` key — so a k9s
skin drops in unchanged; tuikit's `theme.Skin` maps the keys it draws with
and ignores the rest. A skin with no colors under `k9s:`, an unknown name,
or an unreadable color stops startup with the reason (the color's key
path included), rather than silently drawing the default.

`--invert` (or `ui.invert: true`) is k9s's: every color's lightness is
flipped in OkLch and its hue kept, so a dark skin turns light. It applies
to the default look as well as to a skin.

The skin is applied before anything is built: `style.SetBase` re-derives
the palette and every package-level style, and the views register theirs
through `style.OnBase`. `TestNoPackageStyleSkipsTheSkin` fails any
package-level style set in its declaration, which would keep the default
colors for good. Tables repaint with `tktable.FixRows` in the theme's own
colors — the old fixed light-sky-blue and black left blocks of the
terminal's background behind every styled cell on any other canvas.

## Log buffer, log window, live views

`logger.buffer` (5000, k9s's default) is how many lines a log view keeps;
past it the oldest are dropped, through tuikit's `tail.SetMaxLines`. Without
it a log left following a chatty container grew without limit. It must be
at least `logger.tail`, or the opening backlog would be cut short, and at
most 100000.

`logger.sinceSeconds` is how far back a log opens: `-1` (the default) tails
the last `logger.tail` lines, a positive number opens that many seconds of
log. The border title names the window — `last 2m` — as the `1`–`5` keys'
ranges are named; `0` still returns to the tail.

`liveViewAutoRefresh` refreshes inspect views (inspect, health, diff,
stats) on the tick, as k9s's does its describe views. An inspect refresh now
replaces the document in place (`tail.ReplaceLines`), keeping the reader's
scroll position and filter — before, any refresh, manual `r` included,
jumped to the top and silently dropped the filter. Refreshes are
single-flight, so a slow daemon is never asked twice at once.

## k9s behavior keys

These behave as k9s's do (#17):

- `noExitOnCtrlC` — ctrl-c does nothing but say how to quit; `:q` still
  quits. For a terminal where ctrl-c is muscle memory for "stop the thing in
  front of me".
- `screenDumpDir` — where `ctrl-s` saves go (`dumps/` and `logs/` under it)
  and where `:sd` looks, instead of the state directory. A leading `~` is
  the home directory.
- `logger.textWrap`, `logger.disableAutoscroll`, `ui.defaultsToFullScreen`
  — how every log view opens: wrapped, paused rather than following, and
  fullscreen. All log views open through `App.openLogs`, so none can miss
  them.
- `shell` — what `s` opens in a container (zsh, ash, fish) when the
  container has it; otherwise bash, then sh. The name reaches the probe as
  an argument, never as script.
- `ui.enableMouse` — on by default, unlike k9s, because dockmaster has
  always had wheel scrolling; `false` turns mouse reporting off, so a
  plain drag selects text as in any other program.

k9s's `skipLatestRevCheck` has no counterpart: dockmaster has no update
check to skip. `ui.reactive`, reloading config and skins as they change, is
tracked on its own.

## Aliases

`aliases.yaml`, beside `config.yaml`, is k9s's aliases file: each name under
`aliases:` stands for a `:` command line.

```yaml
aliases:
  pg: containers /postgres   # a view, filtered
  img: images
  p: pg                      # through another alias
```

Words typed after an alias go along (`:img /node`). A view command can carry
a filter itself — `:containers /postgres` — which is what makes the first
example work; the filter keeps its case. Aliases join the palette's
suggestions, and `:aliases` lists them.

Like `config.yaml`, the file is checked at startup and a mistake stops it,
naming the alias: a name is one word that is not already a dockmaster
command (an alias cannot take over `ps`), and what it stands for has to
reach a command, through at most five aliases and without a loop.

## Hotkeys

`hotkeys.yaml`, beside `config.yaml`, is k9s's hotkeys file: a key that
runs a `:` command, aliases and `/filter` included.

```yaml
hotKeys:
  usage:
    shortCut: Shift-0
    description: Disk usage
    command: df
  pg:
    shortCut: Ctrl-U
    command: pg          # an alias
```

`shortCut` is spelled as k9s spells it — `Shift-0`, `Shift-A`, `Ctrl-U`,
`Alt-X`, `F2`, or one character — and translated to what the terminal
sends: Shift with a digit is that digit's symbol on a US layout (`Shift-0`
is `)`), Shift with a letter is the capital. `keepHistory` is accepted for
k9s's files and does nothing; every view switch is already in the history.

A hotkey is asked after the view's own keys and before navigation, so in a
view that binds the same key the view keeps it — a hotkey on `x` stops a
container in the containers view and opens its command elsewhere. Keys that
could never reach it (`?`, `:`, `/`, the digits, `esc`, `q`, `r`, the
chrome and table keys) or that would take navigation away (`j`/`k`, arrows,
page keys) are refused at startup, as are a key taken twice and a command
that does not resolve. Help lists hotkeys in a HOTKEYS column, the
description or else the command.

## Plugins

`plugins.yaml`, beside `config.yaml`, is k9s's plugins file: a key, in the
views you name, that runs a command on the selected row.

```yaml
plugins:
  dive:
    shortCut: Shift-D
    description: Dive image
    scopes: [images]
    command: dive
    args: [$IMAGE]
  ctop:
    shortCut: Ctrl-T
    description: Stats in ctop
    scopes: [containers]
    command: sh
    args: [-c, 'DOCKER_HOST=$DOCKER_HOST ctop -f $NAME']
```

`scopes` are view names as the palette spells them (`containers`, `images`,
`volumes`, `networks`, `projects`, `runtimes`, `pods`, `events`, …), plus
`logs`, `files` (the volume browser), `layers` and `node`, or `all`.

`$VAR`s in `args` are filled in by dockmaster, as k9s fills its own, from
the view: `$DOCKER_HOST` (so a plugin's `docker` reaches the daemon on
screen), `$CONTEXT` and `$FILTER` everywhere; the selected row's own
fields — `$NAME`, `$ID`, `$IMAGE`, `$CONTAINER`, `$PROJECT`, `$SERVICE`,
`$STATE`, `$VOLUME`, `$NETWORK`, `$DRIVER`, `$MOUNTPOINT`, `$WORKING_DIR`,
`$CONFIG_FILES`, `$PROVIDER`, by view; and every column of it as
`$COL-<HEADER>` (k9s's spelling) and `$COL_<HEADER>`. Anything else comes
from dockmaster's own environment; a name found nowhere is empty. The same
values are in the command's environment. A table view with no row selected
runs nothing.

- A plugin hands the terminal over, as `s` does; `background: true` runs it
  detached and flashes its outcome — the exit code and last line on
  failure.
- `confirm: true` asks first, showing the command line.
- `dangerous: true` is refused under `--readonly`; other plugins run there,
  being the user's own.
- A plugin's key is asked after the view's own keys, so the view keeps a
  key it binds — unless `override: true`, which puts the plugin first.
- The active view's plugins join its header shortcuts and a PLUGINS column
  in help.

Checked at startup: the key parses, is not one dockmaster keeps and is not
taken by a hotkey or by another plugin in a view they share; there is a
command and at least one scope, and every scope is a view. k9s's `pipes`
and `inputs` are refused rather than ignored, which would run a different
command than the one written; `overwriteOutput` is accepted and does
nothing, as dockmaster does not capture output.
