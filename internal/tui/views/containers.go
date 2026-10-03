// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package views holds dockmaster's resource views. Each implements [View]:
// it renders a table or viewport, reports its row count and loading state,
// and translates keystrokes into (action, param) requests that the app
// layer — not the view — carries out.
package views

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// pending is shown in a column whose value has not arrived yet. It is
// deliberately distinct from a real zero: "0.00%" for a container we have
// not sampled would be a lie, and CPU 0% is exactly what a wedged
// container looks like.
const pending = "—"

// Thresholds colour the CPU% and MEM columns: orange at warn, red at
// critical, in percent. CPU is judged by docker.Stats.CPUShare, memory by
// MemPerc. Zero values mean DefaultThresholds.
type Thresholds struct {
	CPUWarn, CPUCritical float64
	MemWarn, MemCritical float64
}

// DefaultThresholds are k9s's: warn at 70, critical at 90.
func DefaultThresholds() Thresholds {
	return Thresholds{CPUWarn: 70, CPUCritical: 90, MemWarn: 70, MemCritical: 90}
}

// thresholdText renders a value in orange past warn, red past critical.
func thresholdText(text string, pct, warn, critical float64) string {
	switch {
	case pct >= critical:
		return lipgloss.NewStyle().Foreground(style.ColorRed).Bold(true).Render(text)
	case pct >= warn:
		return lipgloss.NewStyle().Foreground(style.ColorOrange).Render(text)
	}
	return text
}

// nodeMarker flags a kind/k3d node in the NAME column, where c opens the
// containers inside it — visible before the row is selected. ⎈ is the
// Kubernetes helm.
const nodeMarker = "⎈ "

// containerName is the NAME cell: the name, marked when it is part of a
// kind/k3d cluster — a node, or the cluster's local registry.
func containerName(c docker.Container) string {
	// A Kubernetes container by what it is — container, then its pod —
	// not cri-dockerd's k8s_<container>_<pod>_<namespace>_<uid>_<n>.
	if k, ok := c.Kube(); ok {
		return truncate(k.Container+style.Muted.Render(" "+k.Pod), 28)
	}
	if _, node := docker.NodeRole(c); node || docker.IsClusterRegistry(c) {
		return lipgloss.NewStyle().Foreground(style.ColorDockerBlue).Render(nodeMarker) +
			truncate(c.Name, 28-lipgloss.Width(nodeMarker))
	}
	return truncate(c.Name, 28)
}

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
	tableMarks
	tableSort
	client *docker.Client
	err    error

	// scopeMatch, when set, keeps only the containers it accepts, and
	// scopeLabel names that in the title: the users of an image, volume
	// or network (#8).
	scopeMatch func(docker.Container) bool
	scopeLabel string

	// wide shows the ID, command, networks and address columns (ctrl+w);
	// width is the last width, to refit the columns when they change.
	wide  bool
	width int

	stats map[string]docker.Stats
	// thresholds colour CPU% and MEM (config thresholds:).
	thresholds Thresholds

	filter  string
	all     []docker.Container
	visible []docker.Container
	table   table.Model

	loading bool
	showAll bool
	// showFaults lists only the containers in trouble (Container.Fault),
	// stopped ones included — k9s's ctrl+z.
	showFaults bool
	// showKube lists the containers a runtime's built-in Kubernetes runs
	// its pods in; hidden by default, they would otherwise bury the user's
	// own (Rancher Desktop starts eleven).
	showKube bool
	statsOn  bool
	inFlight bool
}

// NewContainersView builds the containers view. showAll starts the view in
// `docker ps -a` mode; statsOn enables the background CPU/MEM poll.
func NewContainersView(client *docker.Client, showAll, statsOn bool) *ContainersView {
	t := table.New(
		table.WithColumns(containerColumns(false)),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &ContainersView{
		client:     client,
		table:      t,
		loading:    true,
		showAll:    showAll,
		statsOn:    statsOn,
		thresholds: DefaultThresholds(),
		stats:      make(map[string]docker.Stats),
	}
}

// containerColumns are the containers view's columns; wide adds the ID,
// command, networks and address — k9s's ctrl+w.
func containerColumns(wide bool) []table.Column {
	if !wide {
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
	return []table.Column{
		{Title: "NAME", Width: 28},
		{Title: "ID", Width: 12},
		{Title: "IMAGE", Width: 30},
		{Title: "COMMAND", Width: 24},
		{Title: "STATE", Width: 10},
		{Title: "HEALTH", Width: 9},
		{Title: "CPU%", Width: 7},
		{Title: "MEM", Width: 11},
		{Title: "PORTS", Width: 22},
		{Title: "NETWORKS", Width: 14},
		{Title: "IP", Width: 15},
		{Title: "AGE", Width: 6},
	}
}

// ToggleWide shows the wide columns, or the usual ones again. The sort
// follows its column by name; a sort on a column that goes away falls back
// to the first.
func (v *ContainersView) ToggleWide() {
	before := containerColumns(v.wide)
	v.wide = !v.wide
	v.remapSort(before, containerColumns(v.wide))
	// Rows are never wider than the columns: clear them, set the new
	// columns, then rebuild the rows to match.
	v.table.SetRows(nil)
	v.table.SetColumns(v.columns(fitColumns(containerColumns(v.wide), v.width)))
	v.rebuildRows()
}

// Wide reports whether the wide columns are shown.
func (v *ContainersView) Wide() bool { return v.wide }

// Init kicks off the first fetch.
func (v *ContainersView) Init() tea.Cmd { return v.refresh() }

// ShowAll reports whether stopped containers are included.
func (v *ContainersView) ShowAll() bool { return v.showAll }

// ToggleKube shows or hides the containers Kubernetes runs pods in.
func (v *ContainersView) ToggleKube() {
	v.showKube = !v.showKube
	v.rebuildRows()
}

// ToggleFaults lists only the containers in trouble, or all again. Failed
// containers have exited, so faults fetches stopped ones too, without
// changing the stopped/all toggle.
func (v *ContainersView) ToggleFaults() tea.Cmd {
	v.showFaults = !v.showFaults
	v.rebuildRows()
	return v.refresh()
}

// ShowFaults reports whether only faults are listed.
func (v *ContainersView) ShowFaults() bool { return v.showFaults }

// ShowKube reports whether Kubernetes containers are listed.
func (v *ContainersView) ShowKube() bool { return v.showKube }

// Status notes the Kubernetes containers hidden from the list, for the
// border title — so a daemon running only Kubernetes does not read as
// empty.
func (v *ContainersView) Status() string {
	var parts []string
	if v.scopeLabel != "" {
		parts = append(parts, v.scopeLabel)
	}
	if v.showFaults {
		parts = append(parts, "faults")
	}
	if n := v.hiddenKube(); n > 0 {
		parts = append(parts, fmt.Sprintf("+%d kube hidden", n))
	}
	return strings.Join(parts, " ")
}

// hiddenKube counts the Kubernetes containers ctrl+k would show; 0 while
// they are shown.
func (v *ContainersView) hiddenKube() int {
	if v.showKube {
		return 0
	}
	n := 0
	for _, c := range v.all {
		if k, ok := c.Kube(); ok && !k.Sandbox {
			n++
		}
	}
	return n
}

// ToggleAll flips between `docker ps` and `docker ps -a` and refetches.
func (v *ContainersView) ToggleAll() tea.Cmd {
	v.showAll = !v.showAll
	return v.refresh()
}

// All is the last listing, unfiltered.
func (v *ContainersView) All() []docker.Container { return v.all }

// SetThresholds replaces the CPU/MEM colour thresholds; zero keeps the
// defaults.
func (v *ContainersView) SetThresholds(t Thresholds) {
	if t != (Thresholds{}) {
		v.thresholds = t
		v.rebuildRows()
	}
}

// StatsEnabled reports whether the CPU/MEM poll is on.
func (v *ContainersView) StatsEnabled() bool { return v.statsOn }

// ToggleStats turns the CPU/MEM poll on or off. Off is worth having: the
// stats endpoint costs one blocking request per running container per
// poll, which on a large host is the most expensive thing dockmaster does.
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
	v.width = width
	v.table.SetColumns(v.columns(fitColumns(containerColumns(v.wide), width)))
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
		case "ctrl+k":
			return "toggle_kube", ""
		case "ctrl+z":
			return "toggle_faults", ""
		case "ctrl+w":
			return "toggle_wide", ""
		}
		return "", ""
	}
	return v.keyFor(key, c)
}

// keyFor is the action key asks for on one row.
func (v *ContainersView) keyFor(key string, c docker.Container) (string, string) {
	// A Kubernetes node's workloads are inside it, not in this daemon: n
	// opens them (c is copy, as in k9s — #3). enter stays logs on every
	// row, so the key for logs never depends on what is selected.
	if key == "n" {
		if _, node := docker.NodeRole(c); node {
			return "node_containers", NodeParam(c.ID, "", c.Name)
		}
		if docker.IsClusterRegistry(c) {
			return "registry_not_node", c.Name
		}
		return "not_a_node", c.Name
	}
	switch key {
	case KeyEnter, "l":
		return "logs", c.ID
	case "o":
		return "inspect_container", c.ID
	case "J":
		// k9s's shift-j jumps to a resource's owner; a container's is its
		// compose project.
		if c.Project == "" {
			return "no_project", c.Name
		}
		return "jump_project", c.Project
	case "H":
		return "health", c.ID
	case "T":
		return "top", c.ID
	case "D":
		return "diff", c.ID
	case "S":
		return "stats", c.ID
	case "C":
		return "copy_form", c.ID
	case "e":
		return "edit_form", c.ID
	case "u":
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
	case "s":
		return "exec", c.ID
	case "v":
		// The container's image, by the ID it was created from: the tag
		// may have moved to a newer image since.
		if c.ImageID != "" {
			return "scan_image", c.ImageID
		}
		return "scan_image", c.Image
	case "A":
		if !c.Running() {
			return "not_running", c.Name
		}
		return "attach", c.ID
	case "a":
		return "toggle_all", ""
	case "ctrl+k":
		return "toggle_kube", ""
	case "ctrl+z":
		return "toggle_faults", ""
	case "ctrl+w":
		return "toggle_wide", ""
	case "t":
		return "toggle_stats", ""
	case "F":
		return "portforward", c.ID
	case "b":
		return "open_published", c.ID
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
		if n := v.hiddenKube(); n > 0 {
			hint = fmt.Sprintf("only Kubernetes's containers here (%d) — press <ctrl-k> to show them", n)
		}
		if v.showFaults {
			hint = "no faults — nothing unhealthy, restarting, dead or exited with an error (<ctrl-z> shows all)"
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
	all := v.showAll || v.showFaults
	return func() tea.Msg {
		ctx, cancel := v.client.RequestContext(20 * time.Second)
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
		ctx, cancel := client.RequestContext(20 * time.Second)
		defer cancel()
		return ContainerStatsMsg{Stats: client.SampleStats(ctx, ids)}
	}
}

func (v *ContainersView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]

	for _, c := range v.all {
		k, kube := c.Kube()
		// A pod's pause container is never listed; the rest only on ctrl+k.
		if kube && (k.Sandbox || !v.showKube) {
			continue
		}
		if v.showFaults && !c.Fault() {
			continue
		}
		if v.scopeMatch != nil && !v.scopeMatch(c) {
			continue
		}
		if !f.Empty() && !f.Match([]string{
			c.Name, c.Image, c.State, c.Status, c.Project, c.Service, c.Short(),
			k.Namespace, k.Pod, k.Container,
		}, c.Labels) {
			continue
		}

		cpu, mem := pending, pending
		if !v.statsOn {
			cpu, mem = "", ""
		} else if s, ok := v.stats[c.ID]; ok && s.OK {
			th := v.thresholds
			cpu = thresholdText(fmt.Sprintf("%.2f", s.CPUPerc), s.CPUShare(), th.CPUWarn, th.CPUCritical)
			mem = thresholdText(docker.HumanSize(s.MemUsage), s.MemPerc(), th.MemWarn, th.MemCritical)
		} else if !c.Running() {
			// A stopped container has no stats and never will — an
			// eternal "…" there reads as a hung poll.
			cpu, mem = "", ""
		}

		health := c.Health
		if health == "" {
			health = "—"
		}

		state := style.StateStyle(c.State).Render(c.State)
		healthCell := style.HealthStyle(c.Health).Render(health)
		if v.wide {
			rows = append(rows, table.Row{
				containerName(c), c.Short(), truncate(c.Image, 30), truncate(c.Command, 40),
				state, healthCell, cpu, mem, truncate(c.Ports, 22),
				truncate(c.Network, 24), c.IP, c.Age(),
			})
		} else {
			rows = append(rows, table.Row{
				containerName(c), truncate(c.Image, 30), state, healthCell,
				cpu, mem, truncate(c.Ports, 22), c.Age(),
			})
		}
		v.visible = append(v.visible, c)
	}
	sortRows(&v.tableSort, rows, v.visible)
	rows = markRows(&v.tableMarks, rows, v.visible, v.all, containerMarkKey)
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

	// Still over: the fixed columns alone are wider than the terminal (the
	// containers table needs 81 cells for them at 80 columns). Shave those
	// next, widest first, down to their heading, so every column stays.
	for tableWidth(out) > width {
		widest, at := 0, -1
		for i, c := range out {
			if floor := fixedFloor(c); c.Width > floor && c.Width > widest {
				widest, at = c.Width, i
			}
		}
		if at < 0 {
			break
		}
		out[at].Width--
	}

	// And past that, drop columns from the right. bubbles renders only the
	// columns it is given, so a row's extra cells are simply not drawn.
	// A clipped table is the price of a terminal this narrow; a table wider
	// than the screen wraps every row and takes the frame's border with it.
	for tableWidth(out) > width && len(out) > 1 {
		out = out[:len(out)-1]
	}
	return out
}

// fixedFloor is how narrow a column may be shaved: its heading, and never
// below three cells.
func fixedFloor(c table.Column) int {
	return max(3, lipgloss.Width(c.Title))
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

// ByID returns the container with the given ID from the last listing.
func (v *ContainersView) ByID(id string) (docker.Container, bool) {
	for i := range v.all {
		if v.all[i].ID == id {
			return v.all[i], true
		}
	}
	return docker.Container{}, false
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

// Table is the view's table, for the keys every table shares.
func (v *ContainersView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *ContainersView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// containerMarkKey identifies a row for marks.
func containerMarkKey(c docker.Container) string { return c.ID }

// MarkKey marks rows on space, ctrl+space and ctrl+\.
func (v *ContainersView) MarkKey(key string) bool {
	if !markKey(&v.tableMarks, key, v.visible, v.table.Cursor(), containerMarkKey) {
		return false
	}
	v.rebuildRows()
	return true
}

// ClearMarks unmarks every row.
func (v *ContainersView) ClearMarks() {
	if v.marks != nil {
		v.marks.Clear()
		v.rebuildRows()
	}
}

// BulkKey is key's action on every marked row; nil when none is marked.
func (v *ContainersView) BulkKey(key string) []Action {
	return bulk(&v.tableMarks, v.visible, containerMarkKey, func(c docker.Container) Action {
		name, param := v.keyFor(key, c)
		return Action{Name: name, Param: param, Label: c.Name}
	})
}

// SetScope narrows the list to the containers match accepts — the users of
// an image, a volume or a network (#8) — and names the narrowing in the
// title, as "using image nginx". It lasts until ClearScope.
func (v *ContainersView) SetScope(label string, match func(docker.Container) bool) {
	v.scopeLabel, v.scopeMatch = label, match
	v.rebuildRows()
}

// ClearScope lifts SetScope's narrowing.
func (v *ContainersView) ClearScope() {
	if v.scopeMatch == nil {
		return
	}
	v.scopeLabel, v.scopeMatch = "", nil
	v.rebuildRows()
}

// Scope is the active narrowing's label, "" when there is none.
func (v *ContainersView) Scope() string { return v.scopeLabel }
