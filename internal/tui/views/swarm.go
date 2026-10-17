// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// SwarmKind is which swarm resource a SwarmView lists.
type SwarmKind int

// The swarm resources, each its own top-level view.
const (
	SwarmServices SwarmKind = iota
	SwarmTasks
	SwarmNodes
	SwarmSecrets
	SwarmConfigs
)

// The swarm views' actions. Each param is SwarmParam's NUL-joined parts.
const (
	ActSwarmInspect      = "inspect_swarm"          // kind, id, name
	ActSwarmLogs         = "swarm_logs"             // docker.SwarmLog*, id, name
	ActSwarmTasks        = "swarm_tasks"            // "service" or "node", id, name
	ActServiceScaleForm  = "service_scale_form"     // id, name, current replicas
	ActServiceScale      = "service_scale"          // a ScaleSpec
	ActNotScalable       = "not_scalable"           // name, mode
	ActServiceRestart    = "service_restart"        // id, name
	ActRemoveService     = "confirm_remove_service" // id, name
	ActNodeAvailability  = "node_availability"      // id, name, availability
	ActDrainNode         = "confirm_drain_node"     // id, name
	ActRemoveSwarmNode   = "confirm_remove_swarm_node"
	ActRemoveSecret      = "confirm_remove_secret"
	ActRemoveConfig      = "confirm_remove_config"
	ActToggleTaskHistory = "toggle_task_history"
)

// Column titles the swarm views share with others.
const (
	colImage = "IMAGE"
	colPorts = "PORTS"
)

// ActSwarmTasks's first part: a service's tasks or a node's.
const (
	SwarmByService = "service"
	SwarmByNode    = "node"
)

// SwarmParam joins an action's parts with NUL, which no ID or name holds.
func SwarmParam(parts ...string) string { return strings.Join(parts, "\x00") }

// SplitSwarmParam reads SwarmParam's parts back, padded to n so a short
// param never indexes out of range.
func SplitSwarmParam(param string, n int) []string {
	parts := strings.Split(param, "\x00")
	for len(parts) < n {
		parts = append(parts, "")
	}
	return parts
}

// Noun is the kind's resource word, singular: "service", "task".
func (k SwarmKind) Noun() string {
	return [...]string{SwarmByService, "task", SwarmByNode, "secret", "config"}[k]
}

// ViewType is the view the kind is shown in.
func (k SwarmKind) ViewType() style.ViewType {
	return [...]style.ViewType{
		style.ViewServices, style.ViewTasks, style.ViewSwarmNodes, style.ViewSecrets, style.ViewConfigs,
	}[k]
}

// layoutKey is the kind's views.yaml key.
func (k SwarmKind) layoutKey() string {
	return [...]string{"services", "tasks", "swarmnodes", "secrets", "configs"}[k]
}

// SwarmRow is one listed swarm object: exactly one of the typed fields is
// set, the one for the view's kind.
type SwarmRow struct {
	Data    docker.SwarmData
	Node    docker.SwarmNode
	Task    docker.Task
	Service docker.Service
}

// SwarmRefreshMsg carries one swarm listing. Scope is the task scope it was
// asked with, so a listing from before the scope changed is dropped.
type SwarmRefreshMsg struct {
	Err   error
	Scope docker.TaskScope
	Rows  []SwarmRow
	Kind  SwarmKind
}

// SwarmView lists one swarm resource (#64): services, tasks, nodes,
// secrets or configs. They share everything but their columns, cells and
// keys, so one view serves the five, keyed by kind.
type SwarmView struct {
	err        error
	client     *docker.Client
	scope      docker.TaskScope
	scopeLabel string
	filter     string
	tableSort
	all      []SwarmRow
	visible  []SwarmRow
	table    table.Model
	kind     SwarmKind
	loading  bool
	inFlight bool
	history  bool
}

// NewSwarmView builds the view for one swarm resource.
func NewSwarmView(client *docker.Client, kind SwarmKind) *SwarmView {
	v := &SwarmView{tableSort: tableSort{layoutKey: kind.layoutKey()}, client: client, kind: kind, loading: true}
	v.table = table.New(
		table.WithColumns(v.columns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return v
}

// Kind is the resource the view lists.
func (v *SwarmView) Kind() SwarmKind { return v.kind }

func (v *SwarmView) columns() []table.Column { return swarmColumns(v.kind)() }

// swarmColumns is each kind's column set, as columnSets wants it.
func swarmColumns(k SwarmKind) func() []table.Column {
	switch k {
	case SwarmServices:
		return serviceColumns
	case SwarmTasks:
		return taskColumns
	case SwarmNodes:
		return swarmNodeColumns
	}
	return swarmDataColumns
}

func serviceColumns() []table.Column {
	return []table.Column{
		{Title: colName, Width: 28},
		{Title: "ID", Width: 13},
		{Title: "MODE", Width: 14},
		{Title: "REPLICAS", Width: 9},
		{Title: colImage, Width: 30},
		{Title: colPorts, Width: 22},
		{Title: "STACK", Width: 14},
		{Title: "UPDATE", Width: 12},
		{Title: colAge, Width: 6},
	}
}

func taskColumns() []table.Column {
	return []table.Column{
		{Title: colName, Width: 28},
		{Title: "ID", Width: 13},
		{Title: colImage, Width: 26},
		{Title: "NODE", Width: 18},
		{Title: "DESIRED", Width: 9},
		{Title: "CURRENT", Width: 14},
		{Title: colError, Width: 36},
		{Title: colAge, Width: 6},
	}
}

func swarmNodeColumns() []table.Column {
	return []table.Column{
		{Title: "", Width: 2},
		{Title: "HOSTNAME", Width: 24},
		{Title: "ID", Width: 13},
		{Title: "ROLE", Width: 8},
		{Title: colStatus, Width: 8},
		{Title: "AVAILABILITY", Width: 12},
		{Title: "MANAGER", Width: 11},
		{Title: "ENGINE", Width: 10},
		{Title: "ADDRESS", Width: 16},
		{Title: colAge, Width: 6},
	}
}

func swarmDataColumns() []table.Column {
	return []table.Column{
		{Title: colName, Width: 32},
		{Title: "ID", Width: 13},
		{Title: colDriver, Width: 12},
		{Title: "STACK", Width: 14},
		{Title: "UPDATED", Width: 8},
		{Title: colAge, Width: 6},
	}
}

// Init kicks off the first fetch.
func (v *SwarmView) Init() tea.Cmd { return v.refresh() }

// Refresh refetches the listing.
func (v *SwarmView) Refresh() tea.Cmd { return v.refresh() }

// swarmListTimeout bounds one listing: a manager answers from its raft
// store, but the tasks listing is three requests.
const swarmListTimeout = 20 * time.Second

func (v *SwarmView) refresh() tea.Cmd {
	if v.inFlight || v.client == nil {
		return nil
	}
	v.inFlight = true
	client, kind, scope := v.client, v.kind, v.scope
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(swarmListTimeout)
		defer cancel()
		rows, err := fetchSwarm(ctx, client, kind, scope)
		return SwarmRefreshMsg{Kind: kind, Scope: scope, Rows: rows, Err: err}
	}
}

// fetchSwarm lists one kind's rows.
func fetchSwarm(ctx context.Context, c *docker.Client, kind SwarmKind, scope docker.TaskScope) ([]SwarmRow, error) {
	var rows []SwarmRow
	switch kind {
	case SwarmServices:
		list, err := c.Services(ctx)
		for _, s := range list {
			rows = append(rows, SwarmRow{Service: s})
		}
		return rows, err
	case SwarmTasks:
		list, err := c.Tasks(ctx, scope)
		for _, t := range list {
			rows = append(rows, SwarmRow{Task: t})
		}
		return rows, err
	case SwarmNodes:
		list, err := c.SwarmNodes(ctx)
		for _, n := range list {
			rows = append(rows, SwarmRow{Node: n})
		}
		return rows, err
	}
	list := c.Secrets
	if kind == SwarmConfigs {
		list = c.Configs
	}
	data, err := list(ctx)
	for _, d := range data {
		rows = append(rows, SwarmRow{Data: d})
	}
	return rows, err
}

// Update folds a listing in.
func (v *SwarmView) Update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(SwarmRefreshMsg)
	if !ok || m.Kind != v.kind {
		return nil
	}
	v.inFlight = false
	if m.Scope != v.scope {
		// Asked before the scope changed: ask again for the one on screen.
		return v.refresh()
	}
	v.loading = false
	if m.Err != nil {
		v.err = docker.FormatUserError(m.Err)
		return nil
	}
	v.err = nil
	v.all = m.Rows
	v.rebuildRows()
	return nil
}

// UpdateTable forwards navigation keys.
func (v *SwarmView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *SwarmView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(v.columns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *SwarmView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first fetch is outstanding.
func (v *SwarmView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *SwarmView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// SetScope narrows the tasks view to a service's or a node's tasks, named
// in the title as label; the next listing asks the manager for only those.
func (v *SwarmView) SetScope(scope docker.TaskScope, label string) {
	v.scope, v.scopeLabel = scope, label
	v.all = nil
	v.loading = true
	v.rebuildRows()
}

// ClearScope lifts SetScope's narrowing.
func (v *SwarmView) ClearScope() {
	if v.scope == (docker.TaskScope{}) {
		return
	}
	v.SetScope(docker.TaskScope{}, "")
}

// Status names the scope and whether task history is shown, for the title.
func (v *SwarmView) Status() string {
	var parts []string
	if v.scopeLabel != "" {
		parts = append(parts, v.scopeLabel)
	}
	if v.kind == SwarmTasks && v.history {
		parts = append(parts, "history")
	}
	return strings.Join(parts, " · ")
}

// Selected returns the row under the cursor.
func (v *SwarmView) Selected() (SwarmRow, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return SwarmRow{}, false
	}
	return v.visible[i], true
}

// HandleKey maps a keystroke to an app action.
func (v *SwarmView) HandleKey(key string) (string, string) {
	if v.kind == SwarmTasks && key == "a" {
		v.history = !v.history
		v.rebuildRows()
		return ActToggleTaskHistory, strconv.FormatBool(v.history)
	}
	r, ok := v.Selected()
	if !ok {
		return "", ""
	}
	switch v.kind {
	case SwarmServices:
		return serviceKey(key, r.Service)
	case SwarmTasks:
		return taskKey(key, r.Task)
	case SwarmNodes:
		return swarmNodeKey(key, r.Node)
	}
	return v.dataKey(key, r.Data)
}

// serviceKey: enter its tasks, l its logs, o inspect, s scale, R restart
// (a rolling replace, as service update --force), ctrl-d remove.
func serviceKey(key string, s docker.Service) (string, string) {
	switch key {
	case KeyEnter:
		return ActSwarmTasks, SwarmParam(SwarmByService, s.ID, s.Name)
	case "l":
		return ActSwarmLogs, SwarmParam(docker.SwarmLogService, s.ID, s.Name)
	case "o":
		return ActSwarmInspect, SwarmParam(SwarmServices.Noun(), s.ID, s.Name)
	case "s":
		if !s.Scalable() {
			return ActNotScalable, SwarmParam(s.Name, s.Mode)
		}
		return ActServiceScaleForm, SwarmParam(s.ID, s.Name, strconv.FormatUint(s.Desired, 10))
	case "R":
		return ActServiceRestart, SwarmParam(s.ID, s.Name)
	case KeyCtrlD:
		return ActRemoveService, SwarmParam(s.ID, s.Name)
	}
	return "", ""
}

// taskKey: enter and l the task's logs, o inspect.
func taskKey(key string, t docker.Task) (string, string) {
	switch key {
	case KeyEnter, "l":
		return ActSwarmLogs, SwarmParam(docker.SwarmLogTask, t.ID, t.Name)
	case "o":
		return ActSwarmInspect, SwarmParam(SwarmTasks.Noun(), t.ID, t.Name)
	}
	return "", ""
}

// swarmNodeKey: enter the node's tasks, o inspect, u active, p pause, x
// drain (confirms), ctrl-d remove (confirms).
func swarmNodeKey(key string, n docker.SwarmNode) (string, string) {
	switch key {
	case KeyEnter:
		return ActSwarmTasks, SwarmParam(SwarmByNode, n.ID, n.Hostname)
	case "o":
		return ActSwarmInspect, SwarmParam(SwarmNodes.Noun(), n.ID, n.Hostname)
	case "u":
		return ActNodeAvailability, SwarmParam(n.ID, n.Hostname, "active")
	case "p":
		return ActNodeAvailability, SwarmParam(n.ID, n.Hostname, "pause")
	case "x":
		return ActDrainNode, SwarmParam(n.ID, n.Hostname)
	case KeyCtrlD:
		return ActRemoveSwarmNode, SwarmParam(n.ID, n.Hostname)
	}
	return "", ""
}

// dataKey: a secret's or config's enter and o inspect, ctrl-d remove.
func (v *SwarmView) dataKey(key string, d docker.SwarmData) (string, string) {
	switch key {
	case KeyEnter, "o":
		return ActSwarmInspect, SwarmParam(v.kind.Noun(), d.ID, d.Name)
	case KeyCtrlD:
		if v.kind == SwarmConfigs {
			return ActRemoveConfig, SwarmParam(d.ID, d.Name)
		}
		return ActRemoveSecret, SwarmParam(d.ID, d.Name)
	}
	return "", ""
}

// View renders the table.
func (v *SwarmView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  no " + v.kind.Noun() + "s match")
	}
	return fixSelectedRow(v.table.View())
}

func (v *SwarmView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, r := range v.all {
		if v.kind == SwarmTasks && !v.history && !r.Task.Live() {
			continue
		}
		fields, labels := v.matchFields(r)
		if !f.Empty() && !f.Match(fields, labels) {
			continue
		}
		rows = append(rows, v.cells(r))
		v.visible = append(v.visible, r)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// matchFields are the texts and labels a filter reads on a row.
func (v *SwarmView) matchFields(r SwarmRow) ([]string, map[string]string) {
	switch v.kind {
	case SwarmServices:
		s := r.Service
		return []string{s.Name, s.Short(), s.Image, s.Mode, s.Stack, s.Ports}, s.Labels
	case SwarmTasks:
		t := r.Task
		return []string{t.Name, t.Short(), t.Image, t.Node, t.State, t.Err}, nil
	case SwarmNodes:
		n := r.Node
		return []string{n.Hostname, n.Short(), n.Role, n.State, n.Availability, n.Addr}, n.Labels
	}
	d := r.Data
	return []string{d.Name, d.Short(), d.Stack, d.Driver}, d.Labels
}

// orDash is s, or a muted dash for nothing.
func orDash(s string) string {
	if s == "" {
		return style.Muted.Render("—")
	}
	return s
}

// cells is a row's table cells.
func (v *SwarmView) cells(r SwarmRow) table.Row {
	switch v.kind {
	case SwarmServices:
		s := r.Service
		replicas := s.Replicas()
		if s.Running < s.Desired {
			replicas = style.StateRestarting.Render(replicas)
		}
		return table.Row{
			truncate(s.Name, 28), s.Short(), s.Mode, replicas, truncate(s.Image, 30),
			truncate(orDash(s.Ports), 22), truncate(orDash(s.Stack), 14), orDash(s.Update), s.Age(),
		}
	case SwarmTasks:
		t := r.Task
		state := t.Current()
		if t.State == "failed" || t.State == "rejected" {
			state = style.Error.Render(state)
		}
		return table.Row{
			truncate(t.Name, 28), t.Short(), truncate(t.Image, 26), truncate(orDash(t.Node), 18),
			t.Desired, state, truncate(orDash(t.Err), 36), t.Age(),
		}
	case SwarmNodes:
		n := r.Node
		self := ""
		if n.Self {
			self = "*"
		}
		state := n.State
		if state != "ready" {
			state = style.Error.Render(state)
		}
		avail := n.Availability
		if avail != "active" {
			avail = style.StateRestarting.Render(avail)
		}
		return table.Row{
			self, truncate(n.Hostname, 24), n.Short(), n.Role, state, avail,
			orDash(n.Manager), orDash(n.Engine), orDash(n.Addr), n.Age(),
		}
	}
	d := r.Data
	return table.Row{
		truncate(d.Name, 32), d.Short(), orDash(d.Driver), truncate(orDash(d.Stack), 14),
		since(d.Updated), d.Age(),
	}
}

// Table is the view's table, for the keys every table shares.
func (v *SwarmView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *SwarmView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// Relayout lays the table out again after the column layouts changed.
func (v *SwarmView) Relayout() { v.relayout(&v.table, v.rebuildRows) }

// CopyFields is the selected row's name and full ID.
func (v *SwarmView) CopyFields() (string, string, bool) {
	r, ok := v.Selected()
	if !ok {
		return "", "", false
	}
	switch v.kind {
	case SwarmServices:
		return r.Service.Name, r.Service.ID, true
	case SwarmTasks:
		return r.Task.Name, r.Task.ID, true
	case SwarmNodes:
		return r.Node.Hostname, r.Node.ID, true
	}
	return r.Data.Name, r.Data.ID, true
}

// Total is the unfiltered row count, for the info panel.
func (v *SwarmView) Total() int { return len(v.all) }
