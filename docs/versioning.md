# Versioning

dockmaster follows [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html)
exactly. Which part of a version a release bumps is not decided at release
time: each change records its kind when it is made, and the release is the
largest bump among the changes it carries. [changie](https://changie.dev)
keeps the records and does the arithmetic.

## The public API

Semantic Versioning starts by requiring a declared public API (rule 1), since
"breaking" means nothing until it says what can break. For dockmaster it is
what a user, a script or a config file can depend on:

> **Status: proposed** — this list is the coordinator's draft for the
> maintainer to confirm or change; until it is confirmed, treat it as the
> working definition.

- **The command line**: every flag, its meaning and default; the subcommands
  (`info`, `config path`, `config init`) and their output's shape; the exit
  status.
- **Configuration files**: `config.yaml`, `views.yaml`, `aliases.yaml`,
  `hotkeys.yaml`, `plugins.yaml` and `plugins/`, `jumps.yaml`, and skins —
  every key, its type, default and meaning, and where each file is looked for.
  dockmaster refuses unknown keys, so removing or renaming one breaks a config
  that worked.
- **Environment variables** dockmaster reads: `DOCKMASTER_CONFIG_DIR`,
  `DOCKMASTER_SKIN`, and the docker ones whose precedence it documents.
- **The keys and `:` commands**: every key binding in a view and every command
  and its spellings (`docs/commands.md`). Muscle memory is a dependency, and
  hotkeys, plugins and jumps name these.
- **Plugin variables**: the `$VAR`s a plugin's args can use and the
  `INPUT_<NAME>` values.
- **Labels** dockmaster reads or writes on containers:
  `dockmaster.port-forwards` and `dockmaster.auto-port-forwards` (set by
  users); `dockmaster.registry=local`, `dockmaster.helper` and
  `dockmaster.portforward.*` (set by dockmaster on its own containers, and
  read back by the next version to find them).
- **Where it writes**: the state directory's layout (`dumps/`, `logs/`,
  `exports/`, the log file).

Not public, and free to change in any release: everything under `internal/`,
how a view looks (colors, column widths, layout), the text of a flash or an
error, the log file's line format, and the contents of state files dockmaster
writes for itself (`history.json`, `contexts.json`, the scan cache).

## Recording a change

Every pull request a user could notice adds one file:

```bash
make change          # asks the kind and the text; writes .changes/unreleased/<kind>-<time>.yaml
```

or, without prompts, `go tool -modfile=tools/changie/go.mod changie new --kind Added --body '…'`.
The text is what goes in the changelog, in the same voice as the entries
already there. A pull request with nothing a user can see (CI, docs, a
refactor) takes the `no-changelog` label instead; the **Changelog** check
fails a pull request that has neither.

| Kind | Bump | Use it for |
|---|---|---|
| **Breaking** | major | anything in the public API that stops working as it did: a flag, key, command or config key removed, renamed or given a different meaning; a default changed in a way someone relying on it would notice |
| **Removed** | major | a feature taken out |
| **Added** | minor | something new, everything old still working |
| **Changed** | minor | an existing behavior changed *compatibly* — nobody's config, keys or scripts need to change |
| **Deprecated** | minor | something still working but announced for removal (rule 7) |
| **Fixed** | patch | a bug fixed, the API untouched (rule 6) |
| **Security** | patch | a vulnerability fixed compatibly; if the fix has to break something, it is **Breaking** |

When in doubt between two kinds, take the bigger bump. A pull request with
several user-visible changes records each.

## Releasing

```bash
make next-version    # what the pending changes add up to
make release-prep    # batch them into .changes/vX.Y.Z.md and rebuild CHANGELOG.md
```

Commit that in a release pull request, merge it, and tag the signed `vX.Y.Z`
it names on the merge commit. The release workflow refuses a tag that is not
changie's latest version, and publishes that version's section as the notes.
`RELEASE=minor make release-prep` (or `major`, `patch`, `vX.Y.Z`) overrides
`auto` when a release has to; a pre-release is
`go tool -modfile=tools/changie/go.mod changie batch auto --prerelease rc.1`.

`CHANGELOG.md` is generated from `.changes/` — never edit it by hand; the
Changelog check fails when it differs from what changie builds.
`make changelog-preview` shows it with the pending changes in place.

## Before 1.0.0

Rule 4: in `0.y.z` "anything MAY change at any time", so until 1.0.0 the
version carries no compatibility promise, whatever bump the changes record.
changie still applies the table above, which means a **Breaking** or
**Removed** change recorded while dockmaster is `0.x` makes the next release
`1.0.0`. Recording a breaking change is therefore also the decision to
declare the API stable, and should be made as one.
