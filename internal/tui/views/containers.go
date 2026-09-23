// Package views holds dockyard's resource views. Each implements [View]:
// it renders a table or viewport, reports its row count and loading state,
// and translates keystrokes into (action, param) requests that the app
// layer — not the view — carries out.
package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
)

// pending is shown in a column whose value has not arrived yet. It is
// deliberately distinct from a real zero: "0.00%" for a container we have
// not sampled would be a lie, and CPU 0% is exactly what a wedged
// container looks like.
const pending = "—"

// ContainersRefreshMsg carries a refreshed container list.
type ContainersRefreshMsg struct {
	Err        error
	Containers []docker.Container
}

// ContainerStatsMsg carries a stats sample for the currently listed
// containers. It arrives on its own cadence, slower than the list.
type ContainerStatsMsg struct {
	Stats map[string]docker.Stats
}

// ContainersView is the root view: `docker ps` with live CPU/memory.
type ContainersView struct {
	client *docker.Client
	err    error

	stats map[string]docker.Stats

	filter  string
	all     []docker.Container
	visible []docker.Container
	table   table.Model

	loading  bool
	showAll  bool
	statsOn  bool
	inFlight bool
}

// NewContainersView builds the containers view. showAll starts the view in
// `docker ps -a` mode; statsOn enables the background CPU/MEM poll.
func NewContainersView(client *docker.Client, showAll, statsOn bool) *ContainersView {
	t := table.New(
		table.WithColumns(containerColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &ContainersView{
		client:  client,
		table:   t,
		loading: true,
		showAll: showAll,
		statsOn: statsOn,
		stats:   make(map[string]docker.Stats),
	}
}

func containerColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 28},
		{Title: "IMAGE", Width: 30},
		{Title: "STATE", Width: 10},
		{Title: "HEALTH", Width: 9},
		{Title: "CPU%", Width: 7},
		{Title: "MEM", Width: 11},
		{Title: "PORTS", Width: 22},
		{Title: "AGE", Width: 6},
	}
}

// Init kicks off the first fetch.
func (v *ContainersView) Init() tea.Cmd { return v.refresh() }

// ShowAll reports whether stopped containers are included.
func (v *ContainersView) ShowAll() bool { return v.showAll }

// ToggleAll flips between `docker ps` and `docker ps -a` and refetches.
func (v *ContainersView) ToggleAll() tea.Cmd {
	v.showAll = !v.showAll
	return v.refresh()
}

// StatsEnabled reports whether the CPU/MEM poll is on.
func (v *ContainersView) StatsEnabled() bool { return v.statsOn }

// ToggleStats turns the CPU/MEM poll on or off. Off is worth having: the
// stats endpoint costs one blocking request per running container per
// poll, which on a large host is the most expensive thing dockyard does.
func (v *ContainersView) ToggleStats() {
	v.statsOn = !v.statsOn
	if !v.statsOn {
		v.stats = make(map[string]docker.Stats)
	}
	v.rebuildRows()
}

// Selected returns the container under the cursor, or false when the list
// is empty.
func (v *ContainersView) Selected() (docker.Container, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.Container{}, false
	}
	return v.visible[i], true
}

// Update folds refresh and stats messages into the view.
func (v *ContainersView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ContainersRefreshMsg:
		v.loading = false
		v.inFlight = false
		if msg.Err != nil {
			v.err = docker.FormatUserError(msg.Err)
			return nil
		}
		v.err = nil
		v.all = msg.Containers
		v.rebuildRows()
		return v.sampleStats()

	case ContainerStatsMsg:
		for id, s := range msg.Stats {
			v.stats[id] = s
		}
		// Evict samples for containers that are gone, otherwise a removed
		// container's last CPU reading would be rendered against whichever
		// container later reuses the row.
		for id := range v.stats {
			if !v.hasContainer(id) {
				delete(v.stats, id)
			}
		}
		v.rebuildRows()
		return nil
	}
	return nil
}

func (v *ContainersView) hasContainer(id string) bool {
	for i := range v.all {
		if v.all[i].ID == id {
			return true
		}
	}
	return false
}

// UpdateTable forwards navigation keys to the bubbles table.
func (v *ContainersView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *ContainersView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(fitColumns(containerColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *ContainersView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first fetch is still outstanding.
func (v *ContainersView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *ContainersView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey maps a keystroke to an (action, param) pair for the app.
func (v *ContainersView) HandleKey(key string) (string, string) {
	c, ok := v.Selected()
	if !ok {
		// `a` and `t` are list-level, not row-level — they must keep
		// working on an empty list, which is exactly when a user wants to
		// switch to `ps -a` to find out where everything went.
		switch key {
		case "a":
			return "toggle_all", ""
		case "t":
			return "toggle_stats", ""
		}
		return "", ""
	}

	switch key {
	case KeyEnter, "l":
		return "logs", c.ID
	case "o":
		return "inspect_container", c.ID
	case "s":
		return "start", c.ID
	case "x":
		return "stop", c.ID
	case "R":
		return "restart", c.ID
	case "K":
		return "kill", c.ID
	case "p":
		if c.State == "paused" {
			return "unpause", c.ID
		}
		return "pause", c.ID
	case "e":
		return "exec", c.ID
	case "a":
		return "toggle_all", ""
	case "t":
		return "toggle_stats", ""
	case KeyCtrlD:
		return "confirm_remove_container", c.ID
	}
	return "", ""
}

// View renders the table.
func (v *ContainersView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if len(v.visible) == 0 && !v.loading {
		hint := "no running containers — press <a> to include stopped ones"
		if v.showAll {
			hint = "no containers on this daemon"
		}
		if v.filter != "" {
			hint = fmt.Sprintf("no containers match /%s", v.filter)
		}
		return style.Muted.Render("  " + hint)
	}
	return fixSelectedRow(v.table.View())
}

// Refresh refetches the container list.
func (v *ContainersView) Refresh() tea.Cmd { return v.refresh() }

func (v *ContainersView) refresh() tea.Cmd {
	// Single-flight. `docker ps` against a VM-backed daemon (Colima,
	// Rancher, Docker Desktop) measures whole seconds, well over the poll
	// interval — without this guard every tick stacks another blocking
	// request on the socket until the daemon starts refusing connections.
	if v.inFlight {
		return nil
	}
	v.inFlight = true
	all := v.showAll
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		list, err := v.client.Containers(ctx, all)
		return ContainersRefreshMsg{Containers: list, Err: err}
	}
}

func (v *ContainersView) sampleStats() tea.Cmd {
	if !v.statsOn {
		return nil
	}
	ids := make([]string, 0, len(v.all))
	for _, c := range v.all {
		if c.Running() {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	client := v.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return ContainerStatsMsg{Stats: client.SampleStats(ctx, ids)}
	}
}

func (v *ContainersView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]

	for _, c := range v.all {
		if !f.Empty() && !f.MatchesAny(c.Name, c.Image, c.State, c.Status, c.Project, c.Service, c.Short()) {
			continue
		}

		cpu, mem := pending, pending
		if !v.statsOn {
			cpu, mem = "", ""
		} else if s, ok := v.stats[c.ID]; ok && s.OK {
			cpu = fmt.Sprintf("%.2f", s.CPUPerc)
			mem = docker.HumanSize(s.MemUsage)
		} else if !c.Running() {
			// A stopped container has no stats and never will — an
			// eternal "…" there reads as a hung poll.
			cpu, mem = "", ""
		}

		health := c.Health
		if health == "" {
			health = "—"
		}

		rows = append(rows, table.Row{
			truncate(c.Name, 28),
			truncate(c.Image, 30),
			style.StateStyle(c.State).Render(c.State),
			style.HealthStyle(c.Health).Render(health),
			cpu,
			mem,
			truncate(c.Ports, 22),
			c.Age(),
		})
		v.visible = append(v.visible, c)
	}
	setTableRows(&v.table, rows)
}

// cellPadding is the horizontal padding tuikit's table styles apply per
// column (Padding(0, 1) — one cell each side).
const cellPadding = 2

// fitColumns redistributes column widths to fill the available terminal
// width, growing the two free-text columns (NAME, IMAGE) and leaving the
// fixed-shape ones alone. Without this the table leaves a ragged gap on a
// wide terminal and clips names on a narrow one.
func fitColumns(cols []table.Column, width int) []table.Column {
	if width <= 0 {
		return cols
	}
	total := 0
	for _, c := range cols {
		total += c.Width
	}
	// tuikit's table styles set Padding(0, 1) on both header and cell, so
	// every column costs its Width plus TWO cells, one each side. Counting
	// one was the bug that wrapped the last column onto a second line on a
	// terminal narrower than the sum of the declared widths.
	total += cellPadding * len(cols)

	out := make([]table.Column, len(cols))
	copy(out, cols)

	slack := width - total
	if slack == 0 {
		return out
	}

	// Grow or shrink the wide text columns first; they are the ones whose
	// content is unbounded.
	flexible := []int{}
	for i, c := range out {
		switch strings.ToUpper(c.Title) {
		case "NAME", "IMAGE", "REPOSITORY", "MOUNTPOINT", "SUBNET", "SERVICES", "CREATED BY", "COMMAND", "ENDPOINT":
			flexible = append(flexible, i)
		}
	}
	if len(flexible) == 0 {
		return out
	}

	per := slack / len(flexible)
	rem := slack % len(flexible)
	for n, i := range flexible {
		w := out[i].Width + per
		if n < rem {
			w++
		}
		// Never shrink a text column below something readable — better a
		// horizontally clipped table than a column of single letters.
		if w < minFlexWidth {
			w = minFlexWidth
		}
		out[i].Width = w
	}

	// The floor above can push the total back over budget on a very narrow
	// terminal. Shave the widest flexible column until it fits, so the
	// table clips rather than wrapping every row onto two lines.
	for tableWidth(out) > width {
		widest, at := 0, -1
		for _, i := range flexible {
			if out[i].Width > widest {
				widest, at = out[i].Width, i
			}
		}
		if at < 0 || widest <= minFlexWidth {
			break
		}
		out[at].Width--
	}
	return out
}

// minFlexWidth is the narrowest a free-text column may be squeezed to.
const minFlexWidth = 8

// tableWidth is the rendered width of a column set, padding included.
func tableWidth(cols []table.Column) int {
	total := cellPadding * len(cols)
	for _, c := range cols {
		total += c.Width
	}
	return total
}

// NameFor resolves a container ID to its name, for status messages. The
// app needs this after the row is gone from the table (a remove, say), so
// it searches the full list rather than the visible one.
func (v *ContainersView) NameFor(id string) string {
	for i := range v.all {
		if v.all[i].ID == id {
			return v.all[i].Name
		}
	}
	return ""
}

// RunningTotal is the running and total container counts, for the info
// panel. Counts the full list, not the filtered one — the info panel
// describes the daemon, not the current search.
func (v *ContainersView) RunningTotal() (running, total int) {
	for i := range v.all {
		if v.all[i].Running() {
			running++
		}
	}
	return running, len(v.all)
}
