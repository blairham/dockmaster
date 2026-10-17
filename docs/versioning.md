# Versioning

dockmaster follows [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html)
exactly. Which part of a version a release bumps is not decided at release
time: each change records its kind when it is made — in its pull request's
title — and the release is the largest bump among the changes it carries.
[release-please](https://github.com/googleapis/release-please) reads the
titles and does the arithmetic.

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

A pull request's title is a [Conventional Commit](https://www.conventionalcommits.org/en/v1.0.0/),
and that title is the record: the repository squash-merges with the title as
the commit subject, and release-please reads the subjects. The **PR title**
check, required on `main`, fails a title that is not one.

```text
feat(builds): a build history view from docker buildx history
fix: a stopped container's logs no longer hang the view
feat!: remove the --legacy flag
docs: say where the release notes come from
```

The text after the colon is the changelog entry, so write it for someone
upgrading, in the same voice as the entries already there. The scope in
parentheses is optional.

| Title | Bump | Changelog | Use it for |
|---|---|---|---|
| `feat!:` / `fix!:` (any type with `!`) | major | ⚠ Breaking Changes | anything in the public API that stops working as it did: a flag, key, command or config key removed, renamed or given a different meaning; a default changed in a way someone relying on it would notice; a feature taken out |
| `feat:` | minor | Added | something new, everything old still working; an existing behavior changed *compatibly*; something deprecated — still working, announced for removal (rule 7) |
| `fix:` | patch | Fixed | a bug fixed, the API untouched (rule 6); a vulnerability fixed compatibly — if the fix has to break something, it is `fix!:` |
| `perf:` | patch | Performance | faster or lighter, nothing else changed |
| `revert:` | patch | Reverted | undoing an earlier change |
| `docs:` `ci:` `build:` `refactor:` `test:` `chore:` `style:` | none | hidden | nothing a user of dockmaster can see |

When in doubt between two, take the bigger bump. A pull request with several
user-visible changes can say so in its body, which becomes the commit body —
a `BREAKING CHANGE: …` footer there also makes it major.

**A title that went wrong after the merge** is fixed on the merged pull
request, not in git: edit its body to add the messages it should have had
between `BEGIN_COMMIT_OVERRIDE` and `END_COMMIT_OVERRIDE`, **separated by blank
lines** (one message per line reads as a single message with the rest as its
body). release-please reads that block instead of the commit subject, even for a
conventional commit, and starts at the first `BEGIN_COMMIT_OVERRIDE` anywhere in
the body, so never write that word elsewhere in a PR body, even in backticks.
The PR title check on the release pull request reads each commit as
release-please will and fails on any message that is not a Conventional Commit,
rather than letting release-please skip it silently; fix it this way and re-run
the check.

## Releasing

There is nothing to run. Every merge to `main` updates one standing pull
request, **chore(main): release X.Y.Z**, holding the next version (the
largest bump since the last release) and its section of `CHANGELOG.md`.
Merging it is the release: release-please tags `vX.Y.Z` and creates the
GitHub release, and the release workflow builds, signs and publishes it with
that section as the notes. The release workflow refuses a tag that is not the
version in `.release-please-manifest.json`.

To force a version, put `Release-As: X.Y.Z` in the body of a pull request
before it merges. `CHANGELOG.md`'s existing sections may be edited by hand;
release-please only inserts new ones above them.

## Before 1.0.0

Rule 4: in `0.y.z` "anything MAY change at any time", so until 1.0.0 the
version carries no compatibility promise, whatever bump the titles record.
release-please still applies the table above, which means a breaking (`!`)
change merged while dockmaster is `0.x` makes the next release `1.0.0`.
Marking a change breaking is therefore also the decision to declare the API
stable, and should be made as one.
