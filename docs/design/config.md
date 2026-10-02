# Design: config.yaml

**Status:** Living
**Code:** `internal/config/config.go`, `internal/config/flags.go` (flag layering, `dockmaster config`)

## Shape

The file follows k9s's `config.yaml`: a single top-level key, `dockmaster:`
here and `k9s:` there, with the same camelCase key names where the settings
match (`refreshRate`, `readOnly`, `requestTimeout` — k9s's `apiServerTimeout` — `ui.headless`, `ui.logoless`,
`ui.crumbsless`, `ui.splashless`, `logger.tail`, `logger.showTime`). Settings
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

## Not yet

k9s's `logger.buffer` and `logger.sinceSeconds`, and
`liveViewAutoRefresh` have no dockmaster equivalent yet. Each lands in this
file when its feature does.
