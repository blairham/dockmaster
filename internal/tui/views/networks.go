package views

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// NetworksRefreshMsg carries a refreshed network list.
type NetworksRefreshMsg struct {
	Err      error
	Networks []docker.Network
}

// NetworksView lists networks.
type NetworksView struct {
	tableMarks
	tableSort
	client *docker.Client
	err    error

	filter  string
	all     []docker.Network
	visible []docker.Network
	table   table.Model

	loading  bool
	inFlight bool
}

// NewNetworksView builds the networks view.
func NewNetworksView(client *docker.Client) *NetworksView {
	t := table.New(
		table.WithColumns(networkColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &NetworksView{client: client, table: t, loading: true}
}

func networkColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 30},
		{Title: "NETWORK ID", Width: 13},
		{Title: "DRIVER", Width: 10},
		{Title: "SCOPE", Width: 7},
		{Title: "SUBNET", Width: 20},
		{Title: "FLAGS", Width: 12},
		{Title: "PROJECT", Width: 18},
		{Title: "AGE", Width: 6},
	}
}

// Init kicks off the first fetch.
func (v *NetworksView) Init() tea.Cmd { return v.refresh() }

// Selected returns the network under the cursor.
func (v *NetworksView) Selected() (docker.Network, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.Network{}, false
	}
	return v.visible[i], true
}

// Update folds the refresh message in.
func (v *NetworksView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(NetworksRefreshMsg); ok {
		v.loading = false
		v.inFlight = false
		if m.Err != nil {
			v.err = docker.FormatUserError(m.Err)
			return nil
		}
		v.err = nil
		v.all = m.Networks
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *NetworksView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *NetworksView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.columns(fitColumns(networkColumns(), width)))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *NetworksView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first fetch is outstanding.
func (v *NetworksView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *NetworksView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey maps a keystroke to an app action.
func (v *NetworksView) HandleKey(key string) (string, string) {
	n, ok := v.Selected()
	if !ok {
		if key == "P" {
			return "confirm_prune_networks", ""
		}
		return "", ""
	}
	return v.keyFor(key, n)
}

// keyFor is the action key asks for on one row.
func (v *NetworksView) keyFor(key string, n docker.Network) (string, string) {
	switch key {
	case KeyEnter, "o":
		return "inspect_network", n.ID
	case "P":
		return "confirm_prune_networks", ""
	case KeyCtrlD:
		// Built-ins are refused here rather than at the daemon so the
		// user gets the reason instead of a 403 with no context.
		if n.Builtin() {
			return "refuse_builtin_network", n.Name
		}
		return "confirm_remove_network", n.ID + "\x00" + n.Name
	}
	return "", ""
}

// View renders the table.
func (v *NetworksView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  no networks match")
	}
	return fixSelectedRow(v.table.View())
}

// Refresh refetches the network list.
func (v *NetworksView) Refresh() tea.Cmd { return v.refresh() }

func (v *NetworksView) refresh() tea.Cmd {
	if v.inFlight {
		return nil
	}
	v.inFlight = true
	return func() tea.Msg {
		ctx, cancel := v.client.RequestContext(20 * time.Second)
		defer cancel()
		list, err := v.client.Networks(ctx)
		return NetworksRefreshMsg{Networks: list, Err: err}
	}
}

func (v *NetworksView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]

	for _, n := range v.all {
		if !f.Empty() && !f.MatchesAny(n.Name, n.Driver, n.Subnet, n.Project, n.Short()) {
			continue
		}

		name := n.Name
		if n.Builtin() {
			name = style.Muted.Render(n.Name)
		}

		rows = append(rows, table.Row{
			truncate(name, 30),
			n.Short(),
			n.Driver,
			n.Scope,
			truncate(n.Subnet, 20),
			networkFlags(n),
			truncate(n.Project, 18),
			n.Age(),
		})
		v.visible = append(v.visible, n)
	}
	sortRows(&v.tableSort, rows, v.visible)
	rows = markRows(&v.tableMarks, rows, v.visible, v.all, networkMarkKey)
	setTableRows(&v.table, rows)
}

// NameFor resolves a network ID to its name.
func (v *NetworksView) NameFor(id string) string {
	for i := range v.all {
		if v.all[i].ID == id {
			return v.all[i].Name
		}
	}
	return ""
}

// Total is the unfiltered network count, for the info panel.
func (v *NetworksView) Total() int { return len(v.all) }

// networkFlags renders the network's boolean properties compactly.
//
// This column replaced an ATTACHED container count. The count is not
// available: /networks/json omits the Containers map entirely — every
// network on a real daemon came back with it nil — so the column was
// permanently "—". Filling it would cost one inspect call per network per
// poll, which on a daemon this slow is not worth a number docker's own
// `network ls` does not show either. These flags come free with the list.
func networkFlags(n docker.Network) string {
	flags := make([]string, 0, 3)
	if n.Internal {
		flags = append(flags, "int")
	}
	if n.Attachable {
		flags = append(flags, "att")
	}
	if n.IPv6 {
		flags = append(flags, "v6")
	}
	if len(flags) == 0 {
		return style.Muted.Render("—")
	}
	return strings.Join(flags, ",")
}

// Table is the view's table, for the keys every table shares.
func (v *NetworksView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *NetworksView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// networkMarkKey identifies a row for marks.
func networkMarkKey(n docker.Network) string { return n.ID }

// MarkKey marks rows on space, ctrl+space and ctrl+\.
func (v *NetworksView) MarkKey(key string) bool {
	if !markKey(&v.tableMarks, key, v.visible, v.table.Cursor(), networkMarkKey) {
		return false
	}
	v.rebuildRows()
	return true
}

// ClearMarks unmarks every row.
func (v *NetworksView) ClearMarks() {
	if v.marks != nil {
		v.marks.Clear()
		v.rebuildRows()
	}
}

// BulkKey is key's action on every marked row; nil when none is marked.
func (v *NetworksView) BulkKey(key string) []Action {
	return bulk(&v.tableMarks, v.visible, networkMarkKey, func(n docker.Network) Action {
		name, param := v.keyFor(key, n)
		return Action{Name: name, Param: param, Label: n.Name}
	})
}
