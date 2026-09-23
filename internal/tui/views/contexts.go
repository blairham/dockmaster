package views

import (
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
)

// ContextsRefreshMsg carries the docker context store listing.
type ContextsRefreshMsg struct {
	Contexts []docker.Context
}

// ContextsView lists the docker contexts from the CLI's context store and
// lets the user switch dockyard's daemon endpoint to any of them.
//
// This exists because the context store is the *only* place the endpoint
// lives for Colima, Rancher Desktop, Podman, and remote hosts. Without it
// dockyard would dial /var/run/docker.sock and report a dead daemon on a
// machine where `docker ps` works fine.
type ContextsView struct {
	current string

	filter  string
	all     []docker.Context
	visible []docker.Context
	table   table.Model

	loading bool
}

// NewContextsView builds the context switcher. current is the context
// dockyard is presently connected through.
func NewContextsView(current string) *ContextsView {
	t := table.New(
		table.WithColumns(contextColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &ContextsView{table: t, current: current, loading: true}
}

func contextColumns() []table.Column {
	return []table.Column{
		{Title: "", Width: 2},
		{Title: "NAME", Width: 24},
		{Title: "ENDPOINT", Width: 52},
		{Title: "DESCRIPTION", Width: 36},
	}
}

// Init loads the store.
func (v *ContextsView) Init() tea.Cmd { return v.Refresh() }

// Selected returns the context under the cursor.
func (v *ContextsView) Selected() (docker.Context, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.Context{}, false
	}
	return v.visible[i], true
}

// Update folds the listing in.
func (v *ContextsView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(ContextsRefreshMsg); ok {
		v.loading = false
		v.all = m.Contexts
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *ContextsView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *ContextsView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(fitColumns(contextColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *ContextsView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the store read is outstanding.
func (v *ContextsView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *ContextsView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// SetCurrent marks which context is active after a successful switch.
func (v *ContextsView) SetCurrent(name string) {
	v.current = name
	v.rebuildRows()
}

// HandleKey maps a keystroke to an app action.
func (v *ContextsView) HandleKey(key string) (string, string) {
	c, ok := v.Selected()
	if !ok {
		return "", ""
	}
	if key == KeyEnter {
		return "switch_context", c.Name + "\x00" + c.Host
	}
	return "", ""
}

// View renders the table.
func (v *ContextsView) View() string {
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  no docker contexts found")
	}
	return fixSelectedRow(v.table.View())
}

// Refresh re-reads the context store. It is a filesystem read, so it is
// cheap enough to do on the ordinary poll.
func (v *ContextsView) Refresh() tea.Cmd {
	return func() tea.Msg { return ContextsRefreshMsg{Contexts: docker.Contexts()} }
}

func (v *ContextsView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]

	for _, c := range v.all {
		if !f.Empty() && !f.MatchesAny(c.Name, c.Host, c.Description()) {
			continue
		}
		marker := " "
		name := c.Name
		if c.Name == v.current {
			marker = style.Success.Render("▸")
			name = style.Success.Render(c.Name)
		}
		rows = append(rows, table.Row{
			marker,
			truncate(name, 24),
			truncate(c.Host, 52),
			truncate(c.Description(), 36),
		})
		v.visible = append(v.visible, c)
	}
	setTableRows(&v.table, rows)
}
