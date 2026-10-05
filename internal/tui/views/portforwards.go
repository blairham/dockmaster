// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"fmt"
	"strconv"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// PortForwardsRefreshMsg carries the forward helpers on the daemon.
type PortForwardsRefreshMsg struct {
	Err      error
	Forwards []docker.PortForward
}

// PortForwardsView lists port forwards — the relay containers dockmaster runs
// for them — with b to open one and ctrl-d to stop it.
type PortForwardsView struct {
	tableSort
	client *docker.Client
	err    error

	filter string
	// scopeID, when set, keeps only the forwards to that container (f in
	// the containers view); scopeName names it in the title.
	scopeID   string
	scopeName string
	all       []docker.PortForward
	visible   []docker.PortForward
	table     table.Model

	loading  bool
	inFlight bool
}

// NewPortForwardsView builds the view.
func NewPortForwardsView(client *docker.Client) *PortForwardsView {
	t := table.New(
		table.WithColumns(portForwardColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &PortForwardsView{
		tableSort: tableSort{layoutKey: "portforwards"},
		client:    client,
		table:     t,
		loading:   client != nil,
	}
}

func portForwardColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 28},
		{Title: "LOCAL", Width: 7},
		{Title: "PORT", Width: 7},
		{Title: "URL", Width: 26},
		{Title: "STATE", Width: 10},
		{Title: "AGE", Width: 6},
	}
}

// Init loads the list.
func (v *PortForwardsView) Init() tea.Cmd { return v.refresh() }

// Selected returns the forward under the cursor.
func (v *PortForwardsView) Selected() (docker.PortForward, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.PortForward{}, false
	}
	return v.visible[i], true
}

// Update folds the refresh message in.
func (v *PortForwardsView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(PortForwardsRefreshMsg); ok {
		v.loading = false
		v.inFlight = false
		if m.Err != nil {
			v.err = docker.FormatUserError(m.Err)
			return nil
		}
		v.err = nil
		v.all = m.Forwards
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *PortForwardsView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *PortForwardsView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(portForwardColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *PortForwardsView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first fetch is outstanding.
func (v *PortForwardsView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *PortForwardsView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey maps a keystroke to an app action.
func (v *PortForwardsView) HandleKey(key string) (string, string) {
	f, ok := v.Selected()
	if !ok {
		return "", ""
	}
	switch key {
	case "b", KeyEnter:
		return "open_url", f.URL()
	case KeyCtrlD:
		return "confirm_stop_forward", f.ID + "\x00" + ForwardLabel(f)
	}
	return "", ""
}

// ForwardLabel is "localhost:15000 → registry:5000", or "0.0.0.0:15000 → …"
// for a forward that is not on loopback.
func ForwardLabel(f docker.PortForward) string {
	return fmt.Sprintf("%s → %s:%d", f.Listen(), f.TargetName, f.Remote)
}

// SetScope narrows the list to the forwards to one container, by ID, and
// names it in the title. It lasts until ClearScope.
func (v *PortForwardsView) SetScope(id, name string) {
	v.scopeID, v.scopeName = id, name
	v.rebuildRows()
}

// ClearScope lifts SetScope's narrowing.
func (v *PortForwardsView) ClearScope() {
	if v.scopeID == "" {
		return
	}
	v.scopeID, v.scopeName = "", ""
	v.rebuildRows()
}

// Status names the container the list is narrowed to, for the title.
func (v *PortForwardsView) Status() string {
	if v.scopeID == "" {
		return ""
	}
	return "to " + v.scopeName
}

// View renders the table.
func (v *PortForwardsView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if len(v.visible) == 0 && !v.loading {
		if v.scopeID != "" {
			return style.Muted.Render("  no port forwards to " + v.scopeName + " — <shift-f> on it starts one")
		}
		return style.Muted.Render("  no port forwards — <shift-f> on a container starts one")
	}
	return fixSelectedRow(v.table.View())
}

// Refresh refetches the list.
func (v *PortForwardsView) Refresh() tea.Cmd { return v.refresh() }

func (v *PortForwardsView) refresh() tea.Cmd {
	if v.inFlight || v.client == nil {
		return nil
	}
	v.inFlight = true
	client := v.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(20 * time.Second)
		defer cancel()
		list, err := client.PortForwards(ctx)
		return PortForwardsRefreshMsg{Forwards: list, Err: err}
	}
}

func (v *PortForwardsView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, pf := range v.all {
		if v.scopeID != "" && pf.Target != v.scopeID {
			continue
		}
		if !f.Empty() && !f.MatchesAny(pf.TargetName, strconv.Itoa(pf.Local), strconv.Itoa(pf.Remote)) {
			continue
		}
		rows = append(rows, table.Row{
			truncate(pf.TargetName, 28),
			strconv.Itoa(pf.Local),
			strconv.Itoa(pf.Remote),
			pf.URL(),
			style.StateStyle(pf.State).Render(pf.State),
			docker.Container{Created: pf.Created}.Age(),
		})
		v.visible = append(v.visible, pf)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// Table is the view's table, for the keys every table shares.
func (v *PortForwardsView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *PortForwardsView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// Relayout lays the table out again after the column layouts changed.
func (v *PortForwardsView) Relayout() { v.relayout(&v.table, v.rebuildRows) }
