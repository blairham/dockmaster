# Commands and keys

Everything dockmaster does from the keyboard: the `:` commands, what `-c`
accepts, and each view's keys. In the app, `?` shows the keys for the view
you are in, and `ctrl-a` lists every `:` command and alias and runs the one
you pick.

`TestCommandsDocCoversEveryCommand` fails when a `:` command, or any of its
spellings, is missing here, so this page cannot fall behind the palette.

## The `:` palette

`:` opens the command bar. As you type it suggests the closest command or
alias, which tab or `→` accepts and `ctrl-n` / `ctrl-p` cycle; `↑` and `↓`
recall earlier commands, kept across runs. Names are case-insensitive.

### Views

| Command | Also | Opens |
|---|---|---|
| `:containers` | `:container`, `:ps` | Containers (`0`) |
| `:images` | `:image` | Images (`1`) |
| `:volumes` | `:volume` | Volumes (`2`) |
| `:networks` | `:network` | Networks (`3`) |
| `:projects` | `:project`, `:compose` | Compose projects (`4`) |
| `:runtimes` | `:runtime`, `:engines`, `:machines`, `:machine`, `:colima`, `:podman`, `:vm`, `:vms`, `:profiles`, `:profile` | The runtimes under the daemon: Colima, Podman, Docker Desktop, Rancher Desktop, OrbStack (`5`) |
| `:events` | `:event`, `:ev` | The daemon's event feed (`6`) |
| `:pods` | `:pod` | Podman pods |
| `:pf` | `:portforward`, `:portforwards`, `:forwards`, `:forward` | Port forwards |
| `:df` | `:disk`, `:usage`, `:system` | Disk usage, as `docker system df` |
| `:pulses` | `:pulse`, `:pu` | The pulses dashboard: gauges and sparklines for the whole daemon |

A view command takes two optional parts, `@context` before `/filter`:

- **`/filter`**: open the view filtered, `:containers /postgres`. The filter
  is the same as `/`: a regex (a leading `!` negates it), `-f term` for
  fuzzy, `-l key=value` for labels.
- **`@context`**: switch to that docker context first (an `@` after the
  `/` is part of the filter), as k9s's
  `:pods @ctx` does, `:containers @prod /web`. Naming the context already
  shown just opens the view. Any command that opens a view takes it
  (`:xray @prod`); `:q` and `:ctx` do not.

### Other views and tools

| Command | Also | What it does |
|---|---|---|
| `:xray [root]` | `:x` | k9s's xray: project → service → container → its image, volumes and networks, as a tree |
| `:xray net` | `:xray network`, `:xray networks` | xray grown from the networks: each network → the containers on it → what each uses |
| `:xray vol` | `:xray volume`, `:xray volumes` | xray grown from the volumes: each volume → the containers mounting it |
| `:xray img` | `:xray image`, `:xray images` | xray grown from the images: each image → the containers created from it |
| `:lint` | | Each container's risky or fragile settings, worst first |
| `:dir [path]` | | Browse a directory for compose files and bring a project up from its file (default: the working directory; `~` works) |
| `:sd` | `:screendump`, `:screendumps`, `:dumps` | What `ctrl-s` has saved, tables and logs, newest first |
| `:hostshell` | | A **root** shell on the host of the daemon on screen — the VM under Docker Desktop, OrbStack or Rancher Desktop, or a remote context's server — through a privileged helper (`hostShell.image`, pulled if missing). Confirms; refused under `--readonly` and on a rootless daemon. See [`design/host-shell.md`](design/host-shell.md) |
| `:ctx [name]` | `:context [name]`, `:contexts` | The docker contexts, or switch straight to one |
| `:aliases` | `:alias` | Every `:` command and its spellings, and your own aliases from `aliases.yaml`, as a table; `enter` runs one. `ctrl-a` opens it too |
| `:help` | `:h`, `:?` | The key reference, as `?` |

### On the selected row

These act on the row selected in the current view, as their keys do.

| Command | Also | Key | What it does |
|---|---|---|---|
| `:logs` | `:log` | `l` | Its logs |
| `:inspect` | `:describe` | `o` | Inspect it |
| `:top` | | `T` | A container's processes |
| `:diff` | | `D` | The files a container has changed |
| `:health` | | `H` | A container's healthcheck: the check, its streak, the last probes |

### Images and cleanup

| Command | Also | What it does |
|---|---|---|
| `:pull <ref>` | | Pull an image; the reference keeps its case |
| `:prune` | | Remove stopped containers (confirms) |
| `:prune all` | | As `docker system prune`: stopped containers, unused networks, dangling images, build cache; volumes are kept (confirms) |
| `:prune all volumes` | `:prune all -v`, `:prune all --volumes` | The same, and every unused volume (confirms) |
| `:prune cache` | `:prune build`, `:prune builder` | The build cache (confirms) |

### Settings for this session

| Command | What it does |
|---|---|
| `:readonly` | Read-only mode on or off: every mutating action is refused. It lasts until the next context switch, which applies that context's `readOnly` again (`docs/design/config.md`) |
| `:all` | Stopped containers / all images shown or hidden, as `a` |
| `:stats` | The CPU/MEM poll on or off, as `t` |
| `:logo` | The header logo on or off |
| `:logoless` | The header logo off |
| `:q` | Quit; also `:q!`, `:quit`, `:exit` |

### Your own commands

- **Aliases**, in `aliases.yaml`, name any command line: `pg: containers /postgres`.
- **Hotkeys**, in `hotkeys.yaml`, put a command on a key. `override: true`
  makes the hotkey win over the view's own key; `keepHistory: true` opens
  its view on top of the current one, so `esc` comes back.
- **Plugins**, in `plugins.yaml` and any `*.yaml` in `plugins/` beside it,
  run your own programs on the selected row, with `$NAME`, `$IMAGE` and so
  on, an input form (`inputs`) and pipelines (`pipes`); `overwriteOutput:
  true` shows a background plugin's first line of output when it finishes.
- **Jumps**, in `jumps.yaml`, make `enter` in a view open another view
  filtered by the selected row: `enter` on a container opens its compose
  project's volumes, say. Help's and the header's `<enter>` say where it
  goes.

All four use k9s's file format; see [design/config.md](design/config.md).

## Starting on a command

`-c` (or `--command`) and config's `defaultView` take anything the palette
does: a view, a view with `/filter` and `@context`, any other command, or an
alias. `dockmaster -c xray`, `dockmaster -c pg`, `dockmaster -c "images @prod"`.
An unknown one stops startup with the reason. `dockmaster info` prints where
the config, state and log files are and which docker context it would use.

## Keys

Keys every view shares:

| Key | What it does |
|---|---|
| `0`–`6` | Containers, Images, Volumes, Networks, Projects, Runtimes, Events |
| `:` / `/` | Command bar / filter |
| `?` | Help for this view |
| `enter` | Drill in: logs, layers, a volume's files, a network's containers |
| `o`, `i` | Inspect; `i`, `d` and `y` do the same as `o` where the view has no key of its own |
| `esc` / `q` | Back out of a drill-in (`q` never quits) |
| `[` / `]` / `-` | View history back / forward / last |
| `r`, `ctrl-r` | Reload |
| `j` `k` `h` `l`, arrows | Move |
| `g` / `G`, `ctrl-f` / `ctrl-b` | Top / bottom, page down / up |
| `shift-←/→`, `shift-↑/↓` | Sort by column, sort direction |
| `space` / `ctrl-space` / `ctrl-\` | Mark a row / a range / clear marks; lifecycle and remove keys then act on every marked row |
| `c` / `I` | Copy the row's name / its full ID |
| `ctrl-s` | Save the table as text (`:sd` lists saves) |
| `ctrl-a` | Every command and alias (`:aliases`); in the `:` and `/` bars it is still start of line |
| `ctrl-g` / `ctrl-e` | Breadcrumbs / header on or off |
| `ctrl-c` | Quit |

### Containers

| Key | What it does | Key | What it does |
|---|---|---|---|
| `enter`, `l` | Logs; `enter` on a ⎈ node opens its pods instead | `o` | Inspect |
| `u` / `x` | Start / stop | `R` | Restart |
| `K` | Kill, SIGKILL (confirms) | `p` | Pause / unpause |
| `s` | Shell (config `shell`, then bash, then sh) | `A` | Attach to the main process (ctrl-c ends the attach, not the container) |
| `ctrl-d` | Remove (confirms) | `P` | Prune stopped (confirms) |
| `H` / `T` / `D` / `S` | Health / top / diff / stats | `C` | Copy files in or out, as `docker cp` |
| `e` | Edit limits, restart policy, name | `v` | Scan its image for vulnerabilities |
| `shift-f` | Port-forward: `local:container`, comma-separated for more; labels can prefill or confirm it | `b` | Open a published port in the browser |
| `f` | Its port forwards (`:pf` narrowed to it; `esc` back) | | |
| `J` | Jump to its compose project | `enter`, `n` | On a ⎈ kind/k3d node: the pods' containers inside it (`l` is still its logs) |
| `a` | Show stopped | `t` | CPU/MEM poll on or off |
| `ctrl-w` | Wide columns | `ctrl-z` | Faults only |
| `ctrl-k` | Containers a runtime's built-in Kubernetes runs | | |

### Images, volumes and networks

| View | Keys |
|---|---|
| Images | `enter` layers, `u` run (a form), `v` scan, `U` the containers using it, `ctrl-d` remove, `a` all, `P` prune |
| Volumes | `enter` browse its files, `o` inspect, `U` the containers using it, `z` sizes (slow), `ctrl-d` remove, `P` prune |
| Networks | `enter` / `o` inspect, `U` the containers on it, `ctrl-d` remove, `P` prune |

### Projects

`enter` its containers, `l` every container's logs in one stream, `u`
compose up, `x` stop, `R` restart, `p` pull, `e` edit the compose files then
up, `s` scale a service (a form: the service and its container count; a
scale down or to 0 confirms), `ctrl-d` compose down (confirms).

### Runtimes

`enter` connect to the machine's daemon, `n` new machine, `e` edit
resources, `o` inspect, `u` / `x` / `R` start / stop / restart, `ctrl-d`
delete, `s` a shell on the machine — ssh into the VM for Colima and Podman,
a root host shell (as `:hostshell`, confirms) for Docker Desktop, OrbStack
and Rancher Desktop — `K` the kind cluster on it. The last four confirm
where they take containers with them.

### Logs

`0`–`5` the usual backlog or the last 1m, 5m, 15m, 30m or 1h; `s` autoscroll;
`t` timestamps; `w` wrap; `f` fullscreen; `c` / `ctrl-s` copy / save what is
shown; `shift-c` clear; `m` mark; `/` filter; `o` inspect the container; `x`
/ `R` stop / restart it.

### Inspect and reports

`/` searches (matches highlighted, nothing hidden), `n` / `N` next /
previous match, `c` / `ctrl-s` copy / save, `f` fullscreen, `a` refresh on
the poll.

### Xray

`space`, `h` / `l` fold, `enter` opens what is selected, `o` inspects. On a
container: `s` shell, `A` attach, `u` / `x` / `R` start / stop / restart,
`K` kill, `p` pause, `ctrl-d` remove; `v` scans an image or a container's
image; on a project or service, `s` opens the scale form (on that service).

`:xray net`, `:xray vol` and `:xray img` (`:xray proj` is the default) root
the tree at networks, volumes or images, as k9s's `:xray <resource>` does.
It is the same one `docker ps -a`, so only what some container uses is in
it: a network, volume or image no container uses is in its list view, not
here. A container on two networks is under both. `enter` on a network root
opens the containers on it, on a volume root its files, on an image root its
layers; `o` inspects the root, `v` scans an image root; the containers under
a root keep every container key above.

### Events

`ctrl-z` faults only, `f` follow, `/` filter.

### The rest

| View | Keys |
|---|---|
| Port forwards (`:pf`) | `enter` / `b` open in the browser, `ctrl-d` stop |
| Disk usage (`:df`) | `P` prune the selected kind |
| Saved dumps (`:sd`) | `enter` read, `ctrl-d` delete |
| `:dir` | `enter` walk in or open, `u` up from the file, `e` edit then up, `ctrl-d` `compose -f <file> down` (confirms), `esc` back up |
| `:lint` | `enter` explain, `o` inspect the container |
| Image scan | `enter` the fix and the advisory link |
| Contexts (`:ctx`) | `enter` switch, `ctrl-d` `docker context rm` (confirms; not `default`, the one dockmaster is on, or the CLI's current one) |
| Aliases (`ctrl-a`) | `enter` run the command, `/` filter |
| Pods | `enter` / `o` inspect, `u` / `x` / `R` start / stop / restart, `ctrl-d` remove |
| A node's pods (`n`) | `enter` / `l` logs, `o` inspect, `s` shell, `ctrl-d` remove (exited only), `a` show exited |
| A volume's files | `enter` open, `esc` / `q` up a directory |
