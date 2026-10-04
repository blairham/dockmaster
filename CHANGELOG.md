# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Pre-stable releases (`v0.x.y`) make no compatibility promise — keys, flags and
config can change in any `v0.x` bump.

A release's notes are its section here: the release workflow publishes it and
fails when the tag has none.

## [Unreleased]

### Added

- `:xray` acts on the container under the cursor with the containers view's
  keys: `s` shell, `A` attach, `u` start, `x` stop, `R` restart, `K` kill
  and `ctrl-d` remove (both confirm), `p` pause / unpause, `c` / `i` copy its
  name and ID; `v` scans an image node's image. `--readonly` refuses the
  mutating ones, and the tree keeps its place as it refreshes after. (#56)
- `dockmaster info` prints where dockmaster reads its config and skins and
  writes its dumps, saved logs, log file and history, and the docker
  context and endpoint it would use and how it chose them — without dialing
  the daemon. (#60)
- A log file, `<state>/dockmaster.log` (`--log-file` moves it,
  `--log-level` sets debug / info / warn / error, default warn): every error
  that is flashed, plugin and hotkey runs, and context switches. Never env
  values, a plugin's arguments or output, or what was typed into its inputs
  form — only the inputs' names. Rotated once to `.1` at 5 MB. (#60)

### Fixed

- `:pull` keeps the reference's case: `:pull app:RC1` pulled `app:rc1`. (#52)
- `?` lists every `:` command, `shift-f`, and, over the projects view, the
  project keys; a test now fails when a command is added without one. (#53)
- The README's key table renders whole again, and `-c` names all eleven
  views it opens, in its usage, its error and the README. (#54)

## [0.0.8] - 2026-10-04

### Added

- Plugins take k9s's `inputs` and `pipes`. Inputs open a form before the
  plugin runs (string, number, bool and dropdown fields, required ones
  enforced), and each value reaches the command as `$INPUT_<NAME>` in its
  args and environment; a plugin with inputs confirms by default. Pipes run
  the command's output through further commands, as a pipeline dockmaster
  builds itself, so no shell parses a value. A pipe of one word, `background`
  with pipes, and inputs that k9s would accept but run differently are
  refused when `plugins.yaml` loads. (#18)
- `:<view> @context`, k9s's: switch to a docker context and open a view in
  one command, with an optional filter — `:containers @prod /web`. (#18)

## [0.0.7] - 2026-10-04

### Fixed

- A docker context with TLS material is dialed with it: its `ca.pem`,
  `cert.pem` and `key.pem` and its `SkipTLSVerify`, by the docker CLI's
  rules. It was spoken to in plain HTTP unless `DOCKER_CERT_PATH` and
  `DOCKER_TLS_VERIFY` were exported. `s`, `A` and the compose actions point
  the docker CLI at such a context with `--context`, which keeps its
  certificates. (#33, #46)
- An `ssh://` context works: dockmaster runs `docker system dial-stdio` over
  ssh, as the docker CLI does, where it used to look the host up as a
  hostname and fail. An ssh failure reports ssh's own reason. (#34, #46, #47)

## [0.0.6] - 2026-10-04

### Added

- `:pulses` (`:pu`), k9s's pulses for a daemon: containers running,
  healthchecks passing and disk reclaimable as gauges; total CPU, memory and
  the event rate as sparklines over the last minutes. A skin's chart colors
  apply. (#12, #44, #45)
- `views.yaml`, k9s's format: which columns each table view shows, their
  order, and its default sort. (#14, #43)

## [0.0.5] - 2026-10-04

### Added

- `:xray`: compose projects, their services and containers, and the image,
  volumes and networks each uses, as a tree. (#9, #42)
- `ui.reactive`: a saved `config.yaml`, skin or `aliases.yaml` applies
  without a restart. (#38, #41)

## [0.0.4] - 2026-10-03

### Added

- `ctrl-z` in the events view shows only faults; `:help`; a configurable
  `shell` for `s`, falling back to bash then sh. (#40)
- k9s's `noExitOnCtrlC`, `screenDumpDir`, `textWrap`, `disableAutoscroll`,
  `defaultsToFullScreen` and `enableMouse` config keys. (#17, #39)

## [0.0.3] - 2026-10-03

### Added

- `v` scans an image, or a container's image, for vulnerabilities with
  `grype` or `trivy`, whichever is installed. (#11, #36)
- `:dir [path]` browses for compose files and brings a project up from its
  file. (#15, #37)

## [0.0.2] - 2026-10-03

### Added

- `:lint`: each container's risky or fragile settings, worst first. (#10, #31)
- `A` attaches to a running container's main process without forwarding
  signals, so a ctrl-c ends the attach rather than the container. (#16, #30)

## [0.0.1] - 2026-10-03

### Added

- k9s's log keys (autoscroll, timestamps, wrap, fullscreen, copy, save,
  clear, mark); `d` and `y` inspect; `n` opens a node's pods. (#3, #19)
- `l` on a compose project follows every container's logs in one stream.
  (#4, #20)
- `c` copies a row's name and `i` its full ID. (#5, #21)
- Inspect: `c` copies, `ctrl-s` saves, `f` fullscreen, `a` auto-refresh, and
  `/` searches with `n`/`N` and a match counter. (#6, #22, #29)
- `U` lists the containers using an image, volume or network; `J` jumps from
  a container to its project. (#8, #25)
- The `:` and `/` bars recall earlier entries with up/down, across runs.
  (#13, #27)
- Filters take `-f` for fuzzy and `-l` for labels, as in k9s. (#7, #28)
- Every release carries SLSA build provenance. (#26)

## [0.0.0] - 2026-10-02

First public release: a k9s-style terminal UI for Docker. Containers,
images, volumes, networks and Compose projects in one frame, with log
tailing, inspect, the container lifecycle on the keyboard, a shell and
`docker cp` into containers, the docker context store followed as the CLI
does, k9s-shaped config, skins, aliases, hotkeys and plugins, and the
machines under the daemon — Colima, Podman, Docker Desktop, Rancher Desktop,
OrbStack — managed from the runtimes view. Archives are cosign-signed and
the formula is in `blairham/homebrew-tap`.

[Unreleased]: https://github.com/blairham/dockmaster/compare/v0.0.8...HEAD
[0.0.8]: https://github.com/blairham/dockmaster/compare/v0.0.7...v0.0.8
[0.0.7]: https://github.com/blairham/dockmaster/compare/v0.0.6...v0.0.7
[0.0.6]: https://github.com/blairham/dockmaster/compare/v0.0.5...v0.0.6
[0.0.5]: https://github.com/blairham/dockmaster/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/blairham/dockmaster/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/blairham/dockmaster/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/blairham/dockmaster/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/blairham/dockmaster/compare/v0.0.0...v0.0.1
[0.0.0]: https://github.com/blairham/dockmaster/releases/tag/v0.0.0
