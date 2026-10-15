// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/alerts"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// FiredAlert is one alert raised this session (#109), with the docker
// context it was raised on: the list outlives a context switch.
type FiredAlert struct {
	Context string
	alerts.Alert
}

// AlertsView is :alerts: what the alert watcher raised this session,
// newest first. enter jumps to the container. Nothing here is fetched —
// the app hands the list over, and again whenever an alert fires.
type AlertsView struct {
	tableSort
	filter  string
	all     []FiredAlert
	visible []FiredAlert
	table   table.Model
}

// NewAlertsView lists fired, which is oldest first, as the app keeps it.
func NewAlertsView(fired []FiredAlert) *AlertsView {
	t := table.New(
		table.WithColumns(alertColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	v := &AlertsView{tableSort: tableSort{layoutKey: "alerts"}, table: t}
	v.SetAlerts(fired)
	return v
}

func alertColumns() []table.Column {
	return []table.Column{
		{Title: "TIME", Width: 8},
		{Title: "ALERT", Width: 12},
		{Title: "CONTAINER", Width: 28},
		{Title: "CONTEXT", Width: 16},
		{Title: "MESSAGE", Width: 50},
	}
}

// SetAlerts replaces the list, oldest first as the app keeps it, and shows
// it newest first.
func (v *AlertsView) SetAlerts(fired []FiredAlert) {
	v.all = make([]FiredAlert, 0, len(fired))
	for i := len(fired) - 1; i >= 0; i-- {
		v.all = append(v.all, fired[i])
	}
	v.rebuildRows()
}

// Init has nothing to load.
func (v *AlertsView) Init() tea.Cmd { return nil }

// Selected returns the alert under the cursor.
func (v *AlertsView) Selected() (FiredAlert, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return FiredAlert{}, false
	}
	return v.visible[i], true
}

// Update has no messages of its own.
func (v *AlertsView) Update(tea.Msg) tea.Cmd { return nil }

// UpdateTable forwards navigation keys.
func (v *AlertsView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *AlertsView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(alertColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *AlertsView) Count() int { return len(v.table.Rows()) }

// Loading is always false: the list is in hand.
func (v *AlertsView) Loading() bool { return false }

// SetFilter applies a row filter over every column.
func (v *AlertsView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// Refresh has nothing to re-read: the app pushes each new alert.
func (v *AlertsView) Refresh() tea.Cmd { return nil }

// HandleKey jumps to the selected alert's container on enter. The param
// is the context, a NUL, then the container's name: the app checks the
// context is the one on screen.
func (v *AlertsView) HandleKey(key string) (string, string) {
	a, ok := v.Selected()
	if !ok || key != KeyEnter {
		return "", ""
	}
	return "alert_jump", a.Context + "\x00" + a.Name
}

// View renders the table.
func (v *AlertsView) View() string {
	switch {
	case len(v.all) == 0:
		return style.Muted.Render("  no alerts this session — a container turning unhealthy, OOM-killed, " +
			"exiting non-zero or restart-looping is listed here")
	case len(v.visible) == 0:
		return style.Muted.Render("  no alert matches the filter")
	}
	return fixSelectedRow(v.table.View())
}

func (v *AlertsView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, a := range v.all {
		kind, msg := a.Kind.String(), a.Message()
		if !f.Empty() && !f.MatchesAny(a.Name, kind, a.Context, msg) {
			continue
		}
		rows = append(rows, table.Row{
			a.Time.Local().Format("15:04:05"),
			style.StateDead.Render(kind),
			truncate(a.Name, 28),
			truncate(a.Context, 16),
			truncate(msg, 80),
		})
		v.visible = append(v.visible, a)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// Table is the view's table, for the keys every table shares.
func (v *AlertsView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *AlertsView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// Relayout lays the table out again after the column layouts changed.
func (v *AlertsView) Relayout() { v.relayout(&v.table, v.rebuildRows) }

// CopyFields is the selected alert's container name and ID.
func (v *AlertsView) CopyFields() (string, string, bool) {
	a, ok := v.Selected()
	return a.Name, a.ID, ok
}
