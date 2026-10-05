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
spelling. k9s's per-context `skin` and `readOnly` live under `contexts:`
(see [Per-context settings](#per-context-settings)). `dockmaster config init` writes the commented defaults
(`config.Sample`); `TestSampleIsTheDefaults` keeps that sample in step with
`config.Default()`.

## Where it lives

`$DOCKMASTER_CONFIG_DIR/config.yaml`, else `$XDG_CONFIG_HOME/dockmaster/config.yaml`,
else `~/.config/dockmaster/config.yaml`. `dockmaster config path` prints the
answer; `dockmaster info` prints it with every other path dockmaster uses —
skins, the state directory with its dumps, saved logs, log file and
history — and the docker endpoint it would dial, without dialing it. k9s's own default on macOS is `~/Library/Application Support`;
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
and ignores the rest. `views.charts.defaultChartColors`' first two
entries color `:pulses`' sparklines (CPU, then memory). A skin with no colors under `k9s:`, an unknown name,
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

## Per-context settings

k9s keeps settings per cluster context; dockmaster keeps them per **docker
context**, in a `contexts:` block of `config.yaml` keyed by the context's
name (#55):

```yaml
dockmaster:
  readOnly: false
  ui:
    skin: dracula
  contexts:
    prod:
      skin: red          # a skin in skins/, as ui.skin
      readOnly: true     # replaces the top-level readOnly here
      defaultView: containers   # what a switch to prod, or a start on it, opens
    colima:
      readOnly: false
```

`skin`, `readOnly` and `defaultView` are the only keys, and decoding is as
strict as anywhere else in the file: any other key is an error. An entry
does not have to name a context the docker store has — contexts come and
go — and one that does not is simply never used. Each entry is checked at
startup and on every reload (`main.go`'s `loadSettings`): its skin must
load exactly as `ui.skin` must (`config.ContextThemes`; the error names the
context), and its `defaultView` is checked as `-c` is, except that it may
not name an `@context` of its own (`tui.ValidateContextCommand`).

They apply whenever dockmaster is on that context: at startup, after
`:ctx` or the picker, `:<command> @context`, the runtimes view's reconnect,
and `--context`. Leaving it for a context with no entry puts the top-level
skin and readOnly back. The context name is the one the info panel shows —
`default` for the stock socket.

**readOnly**, in order:

1. `--readonly` on the command line forces it on, on every context, whatever
   a context's `readOnly` says (`Options.ForceReadOnly`). `--readonly=false`
   only sets the top-level value, so it does not undo a context's
   `readOnly: true`.
2. Else the context's `readOnly`, when its entry sets one — `false` as well
   as `true`, so `readOnly: false` opens one context up under a top-level
   `readOnly: true`.
3. Else the top-level `readOnly`.

`:readonly` flips it for the session **until the next context switch**,
which applies the rules above again — a switch back to `prod` re-arms its
`readOnly: true`. The switch's flash says so when the context's own setting
is what made the session read-only; `[RO]` beside the context in the info
panel shows it either way.

**Skin**, in order: `DOCKMASTER_SKIN` (there is no skin flag) beats
everything; else the context's `skin`; else `ui.skin`; else the default
look. `--invert` / `ui.invert` applies on top of whichever wins. A context
skin is still loaded, and so still checked, when `DOCKMASTER_SKIN` is set.

The switch reskins without a restart exactly as a reload does
(`App.reskin`): `style.SetBase` re-derives every style through the
`style.OnBase` hooks, then `App.buildChrome` builds the frame and bars
again on the new theme. Each context's theme is the context's skin laid
over the **default** theme — never over the skin it replaces — so leaving
and coming back is the same picture (`config.ContextThemes`, resolved by
`main.go` into `tui.ContextSettings`). The app keeps the top-level theme
(`App.globalTheme`) to put back; a reload replaces it, and on a context with
a skin of its own keeps that skin on screen.

**Where a switch lands.** An explicit landing always wins: `-c` at startup,
`:<view> @context` on a switch. Otherwise:

- at startup: `-c`, else the context's `defaultView`, else the top-level
  `defaultView`, else the view last open on the context, else containers;
- on a switch (`:ctx`, the picker): the context's `defaultView`, else the
  view last open on it, else containers. The top-level `defaultView` is a
  startup setting and does not apply to a switch;
- the runtimes view's reconnect stays on the runtimes view.

`dockmaster config init` writes `defaultView: ""`, so a fresh config starts
on the remembered view. A config written by v0.0.9 or earlier has
`defaultView: containers`, which is explicit and wins: delete the line to
start on the remembered view.

**The last view** is kept in the state directory, never in `config.yaml`
(nothing writes `config.yaml`): `$XDG_STATE_HOME/dockmaster/contexts.json`,
else `~/.local/state/dockmaster/contexts.json`, next to `history.json`:

```json
{
  "lastView": {
    "colima": "images",
    "prod": "containers"
  }
}
```

It records the top-level view switched to — a digit, `:images`, `[` / `]` /
`-` — never a drill-in or a pushed view, and only views `-c` names (the
palette names in `ViewCommandNames`). It is written when the view changes,
through a temporary file, merged with what the file already holds so two
dockmasters on different contexts do not erase each other. A write that
fails is logged (`internal/applog`), never flashed; a missing or corrupt
file is an empty one, and a name in it that is not a view is ignored. The
path comes in as `Options.ContextStateFile`, set by `main.go`, so tests
never touch the real state directory.

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
check to skip.

## ui.reactive

With `ui.reactive: true`, saved changes to the files in the config
directory — `config.yaml`, `skins/`, `aliases.yaml`, `hotkeys.yaml`,
`plugins.yaml`, `views.yaml` — apply without a restart (#38). On each tick
the app takes a fingerprint of the directory (names, sizes, modification times); a change is
read only once it has held still for a tick, so an editor's multi-write save
or a half-written file is never what gets applied. The read is `main.go`'s
`loadSettings`, the same function startup uses, with the same command-line
flags over it — a reload cannot accept what startup would refuse, and a file
that does not load leaves the running config untouched and says why.

A reskin rebuilds the frame and the bars (`App.buildChrome`), keeping their
history and the header and crumbs; the skin is laid over the default theme,
not over the previous skin, or `invert: true` would undo itself on every
reload. Runtime toggles — `:readonly`, `ctrl-e`, `ctrl-g`, `:logo` — change
only when their value in the file changed; for `:readonly` that is what
readOnly comes to on the current context, so a reload that changes only
another context's entry leaves it alone. The docker `context` and
`requestTimeout` are read at startup; changing them says a restart is
needed.

A changed `views.yaml` lays out again every table view already open — the
one shown and those under it on the stack (`App.applyColumnLayouts`) — so
none keeps rows built for its old columns. The cursor stays where it was; a
sort the user chose follows its column by name, and the file's own
`sortColumn` is replaced by the new one.

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
suggestions. `ctrl-a` (or `:aliases`) opens k9s's aliases view: every `:`
command with its other spellings, and your aliases first, marked `alias`
with what each stands for; `enter` runs the selected row as if it were
typed at the palette, and `/` and the sort keys work as in any table.

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
is `)`), Shift with a letter is the capital.

`keepHistory: true` is k9s's: the view the command opens goes on top of the
one the hotkey was pressed in, as a drill-in does, so `esc` (or `q`) comes
back to it. Without it a hotkey that names a view switches to it the way a
digit does, and the view stack starts over. It changes only commands that
switch top-level views (`:images`, `:pf`, an alias for one); `:xray`,
`:lint`, `:ctx` and the like always open on top. A view already under the
stack is switched to rather than stacked twice.

A hotkey is asked after the view's own keys and before navigation, so in a
view that binds the same key the view keeps it — a hotkey on `x` stops a
container in the containers view and opens its command elsewhere — unless
it sets `override: true`, as in k9s, which asks it before the view's keys
(after an `override` plugin on the same key). Keys that
could never reach it (`?`, `:`, `/`, the digits, `esc`, `q`, `r`, `ctrl-a`,
the chrome and table keys) or that would take navigation away (`j`/`k`, arrows,
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
  failure. With `overwriteOutput: true`, as in k9s, a background plugin
  that succeeds shows the first non-blank line it printed to stdout in
  place of "done", escapes and control characters removed; one that
  printed nothing still says "done". A foreground plugin's output is on the
  terminal already, so there the option changes nothing (k9s's neither).
- `confirm: true` asks first, showing the command line (and its pipes).
  With `inputs` it defaults to true, as k9s's does; `confirm: false` turns
  it off.
- `dangerous: true` is refused under `--readonly`; other plugins run there,
  being the user's own.
- A plugin's key is asked after the view's own keys, so the view keeps a
  key it binds — unless `override: true`, which puts the plugin first.
- The active view's plugins join its header shortcuts and a PLUGINS column
  in help.

### Inputs and pipes

k9s's `inputs` ask for values before a plugin runs, and its `pipes` run the
plugin's output through further commands. This one asks how far back to look,
and whether to show timestamps, then keeps the last twenty error or warning
lines of a container's log:

```yaml
plugins:
  grep-logs:
    shortCut: F2
    description: Grep logs
    scopes: [containers]
    command: docker
    args: [--host, $DOCKER_HOST, logs, --since, $INPUT_SINCE, --timestamps=$INPUT_TIMESTAMPS, $NAME]
    inputs:
      - name: since
        label: Since
        type: dropdown
        options: [10m, 1h, 24h]
        default: 1h
        required: true
      - name: timestamps
        type: bool
    pipes:
      - grep -i -E error|warn
      - tail -n 20
```

(Only the log's stdout goes through the pipes: `docker logs` writes a
container's stderr to its own stderr, which goes straight to the terminal.)

Pressing the key opens a form (`views.PluginFormView`) with every default
filled in. `tab` moves between fields; a `bool` is a toggle (`space`, or
`y` / `n`), starting false unless its default is `true`; a `dropdown` cycles
its options with `←` / `→` / `space`, starting on its default, or on no
choice if it has none; a `number` must parse as one (Go's `ParseFloat`,
k9s's test). `enter` refuses an empty `required` field or a bad number in
the form and runs nothing; `esc` abandons it and runs nothing. On submit
each input becomes `INPUT_<NAME>` (the name upper-cased): in the command's
environment, and filled into `args` like every other `$VAR` — once, so a
typed `$NAME` stays literal text. Then the confirm, then the command. The
row is the one selected when the key was pressed.

Each `pipes` entry is split into words the way k9s splits it (shlex: quotes
and backslashes group, no expansion) and the plugin runs as
`command args | pipe | pipe …`, the last command's output and every
command's errors on the terminal. dockmaster builds the pipeline itself —
each command exec'd with its own argument list, joined by OS pipes — so no
shell ever parses an input or a row's value. As in k9s, `$VAR`s are **not**
filled into pipes: `grep $NAME` in a pipe greps for the text `$NAME`. Every
command does get the plugin's environment, so a pipe that needs a value
reads it there — `sh -c 'grep -F -- "$INPUT_PATTERN"'`, where the shell
reads the variable and never sees the value as code. The pipeline's exit is
its last command's, as a shell's is without `pipefail`: a `grep` that
matches nothing fails the plugin; an upstream command ended by a closed
pipe does not.

### Checked at startup

The key parses, is not one dockmaster keeps and is not taken by a hotkey or
by another plugin in a view they share; there is a command and at least one
scope, and every scope is a view.

Inputs are held to k9s's rules — no name twice, a `dropdown` default among
its options, a `bool` default `true` or `false`, a `number` default that
parses — and to a few more, each where k9s would accept a file and then
behave unlike what it says:

- a name is letters, digits and `_`, not starting with a digit, because it
  becomes `$INPUT_<NAME>`, which neither dockmaster nor a shell could read
  with a `-` or a space in it;
- no two names that differ only in case, which would both be
  `$INPUT_<NAME>`, one silently replacing the other;
- `type` is `string` (the default when it is left out), `number`, `bool` or
  `dropdown` — a typo such as `boolean` is refused, not run as free text;
- a `dropdown` has `options`.

Pipes: an entry that does not split (an unclosed quote), or that is fewer
than two words, is refused. k9s skips such an entry silently, which would
run a different pipeline than the one written — `sort` on its own has to be
spelled with an argument (`sort -s`) or dropped. `background: true` with
pipes is refused too: k9s starts that pipeline without giving it the
terminal, so its output lands over the UI, and there is nothing sensible
to match.

## Views

`views.yaml`, beside `config.yaml`, is k9s's views file (#14): which of a
view's columns to show, in what order, and the column it opens sorted by —
and, in four views, columns of your own read from each row's labels and
fields ([expression columns](#expression-columns)).

```yaml
views:
  containers:
    columns: [NAME, STATE, IMAGE, AGE]   # these, in this order
    sortColumn: AGE:desc                 # optional: COLUMN, COLUMN:asc or COLUMN:desc
  images:
    columns: [REPOSITORY, TAG, SIZE]
```

A view not in the file keeps its own columns and order, and so does one
whose entry has only a `sortColumn`. Columns are named by their header,
regardless of case. The view keys, and the columns each can show:

| View | What it is | Columns |
|---|---|---|
| `containers` | `:containers` | NAME, IMAGE, STATE, HEALTH, CPU%, MEM, PORTS, AGE; wide mode (`ctrl-w`) adds ID, COMMAND, NETWORKS, IP |
| `images` | `:images` | REPOSITORY, TAG, IMAGE ID, SIZE, USED BY, AGE |
| `volumes` | `:volumes` | NAME, DRIVER, SIZE, REFS, PROJECT, MOUNTPOINT, AGE |
| `networks` | `:networks` | NAME, NETWORK ID, DRIVER, SCOPE, SUBNET, FLAGS, PROJECT, AGE |
| `projects` | `:projects` | PROJECT, STATUS, SERVICES, COMPOSE FILE, AGE |
| `runtimes` | `:runtimes` | PROVIDER, NAME, STATUS, ARCH, CPUS, MEMORY, DISK, RUNTIME, K8S, CONTEXT |
| `pods` | `:pods` | MACHINE, NAME, STATUS, READY, CONTAINERS, AGE |
| `portforwards` | `:pf` | NAME, LOCAL, PORT, URL, STATE, AGE |
| `diskusage` | `:df` | TYPE, TOTAL, ACTIVE, SIZE, RECLAIMABLE |
| `contexts` | `:ctx` | NAME, ENDPOINT, DESCRIPTION |
| `lint` | `:lint` | WORST, NAME, FINDINGS, FIRST FINDING |
| `dir` | `:dir` | NAME, KIND, SIZE, AGE |
| `dumps` | `:sd` | NAME, KIND, SIZE, AGE |
| `node` | `n` on a kind/k3d node | NAMESPACE, POD, NAME, STATE, RESTARTS, IMAGE, AGE |
| `layers` | `enter` on an image | LAYER, SIZE, AGE, CREATED BY |
| `files` | `enter` on a volume | NAME, SIZE, MODE, AGE |
| `scan` | `v` on an image | SEVERITY, ID, PACKAGE, INSTALLED, FIXED IN, TITLE |
| `aliases` | `ctrl-a`, `:aliases` | COMMAND, ALSO, KIND, DESCRIPTION |

The contexts and runtimes views' unnamed marker column (the current
context, the connected machine) is always shown, first. `top`'s columns are
the process listing's own and cannot be laid out; the logs, events, inspect
and xray views are not tables.

The containers view's wide columns can be named: they take their place in
the order given while wide mode is on and are skipped while it is off, and
a `sortColumn` among them sorts only while they are shown. A `sortColumn`
opens the view sorted; `shift-←/→` and `shift-↑/↓` take over from it as
usual, counting the columns as shown. Only shown columns are a plugin's
`$COL-<HEADER>`: a column `views.yaml` hides is not available to plugins
as `$COL-<HEADER>` (or `$COL_<HEADER>`), which then reads as empty unless
dockmaster's own environment has that name. A plugin that needs a hidden
column's value should use the row's named variable where there is one
(`$IMAGE`, `$STATE`, …), which does not depend on the columns shown.

### Expression columns

The containers, images, volumes and networks views also take k9s's
expression columns (#61), `TITLE:<path>|<attributes>`: a column read from
the row's own data — the list response the view already has, so it costs
no daemon call.

```yaml
views:
  containers:
    columns:
      - NAME
      - SVC:.Labels.com\.docker\.compose\.service
      - PROJ:.Labels.com\.docker\.compose\.project
      - STATE
      - 'CMD:.Command|W'          # only in wide mode (ctrl-w)
      - 'BORN:.Created|T'         # an age, sorted as one
    sortColumn: SVC:asc           # an expression column's title sorts too
  images:
    columns: [REPOSITORY, TAG, 'VERSION:.Labels.org\.opencontainers\.image\.version|R']
  volumes:
    columns: [NAME, DRIVER, 'PATH:.Mountpoint']
  networks:
    columns: [NAME, 'GW:.Gateway', 'V6:.IPv6|R']
```

Quote an entry holding `|` or starting with a character YAML reads
specially; a plain `\.` needs no quoting.

**The path** is `.Labels.<key>` — one label, its value or an empty cell
when the row does not carry it — or `.<Field>`, one of the row's fields.
Labels are one level deep, so the dots in a key are escaped as `\.`
(k9s's escape), and `\|` and `\\` are a bar and a backslash in a key; an
unescaped dot past the key is refused rather than read as nesting. Field
names are case-sensitive, as in k9s and docker's Go templates, and follow
the Engine API's list response where it has the field:

| View | Fields |
|---|---|
| `containers` | `.ID`, `.Name`, `.Image`, `.ImageID`, `.Command`, `.State`, `.Status`, `.Health`, `.Ports`, `.Network`, `.IP`, `.Project`, `.Service` (text); `.Created` (time); `.SizeRw` (number) |
| `images` | `.ID`, `.Repo`, `.Tag`, `.Digest` (text); `.Created` (time); `.Size`, `.Containers` (number); `.Dangling` (true/false) |
| `volumes` | `.Name`, `.Driver`, `.Scope`, `.Mountpoint`, `.Project` (text); `.Created` (time); `.Size`, `.Refs` (number) |
| `networks` | `.ID`, `.Name`, `.Driver`, `.Scope`, `.Subnet`, `.Gateway`, `.Project` (text); `.Created` (time); `.Containers` (number); `.Internal`, `.Attachable`, `.IPv6` (true/false) |

Every view's rows also have `.Labels`. A number the daemon did not report
(a size or count of -1) and a zero time are empty cells; a time is drawn
as `2006-01-02 15:04:05` in local time, which sorts as text, or as an age
with `T`. A label's tabs and line breaks become spaces and its other
control characters, escapes included, are dropped, so a label cannot act
on the frame.

**Attributes**, after the first unescaped `|`, one letter each:

| Attribute | Means | Takes |
|---|---|---|
| `R` | right-align: cells are padded to the column's edge | any column |
| `L` | left-align, the default | any column; not with `R` |
| `W` | shown only in wide mode (`ctrl-w`), in its place in the order given | the containers view, the only one with a wide mode |
| `T` | a time drawn as an age (`5m`, `3d`) and sorted by it, as `AGE` is | a time field (`.Created`) |
| `N` | sorted as a number: cells that are not numbers — an empty one included — after every number, where the usual order puts an empty cell first (two numbers compare by value in every column) | a number field or a label |

Everything else is refused, never ignored: `H` (hide — leave the column out
of `columns` instead), `S` (an expression column is always shown unless
`W`), lower case, and anything k9s has not got. Attributes go only on an
expression column; a view's own column with one (`NAME|R`) is refused.

An expression column behaves as the view's own columns do: it counts for
`shift-←/→`, sorts as text (or as `T` / `N` say), the selected row stays the
one drawn under the cursor whatever it is sorted by, `ctrl-s` saves it,
plugins see it as `$COL-<TITLE>`, and a reload applies a changed one. The
`/` filter matches the fields it always has — not every cell drawn — so an
expression column adds nothing to what a regex matches; `-l key=value`
filters by any label, the one a column shows included.

Expression columns go through the same two places as the view's own
(`views/layout.go`, `views/sort.go`): `sortRows` reads each row's cells
from the item it was built from, before the sort reorders both, and the
projection finds them by title.

### Checked at load

Checked at startup and on a reload: every view is one of the keys above,
every column is one that view can show or a well-formed expression column
for it, no title is listed twice — an expression column's title must not
be one of the view's own, in any mode — and `sortColumn` names one of its
columns — one of those listed, when `columns` is given — with no direction,
`asc` or `desc`. An expression column whose path names no field of that
view, or a label without its key, or that takes an attribute dockmaster
does not implement, is an error naming the view and the column; k9s's
JSONPath beyond these shapes (`.metadata.labels`, `[*]`) is refused the
same way. A mistake stops startup, naming the view and the names that
would have been right; on a reload it leaves the running layout as it was.
