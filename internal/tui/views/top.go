// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// TopRefreshMsg carries a container's process list.
type TopRefreshMsg struct {
	Err   error
	ID    string
	Procs docker.Processes
}

// TopView is `docker top`, refreshed with the poll. Its columns are
// whatever ps on the daemon's host reports, so they are built from the
// titles of each listing rather than declared.
type TopView struct {
	tableSort
	client *docker.Client
	err    error

	id, name string
	filter   string

	procs   docker.Processes
	cols    []table.Column
	table   table.Model
	width   int
	loading bool
}

// NewTopView builds the process drill-in for container id.
func NewTopView(client *docker.Client, id, name string) *TopView {
	t := table.New(
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &TopView{client: client, table: t, id: id, name: name, loading: true}
}

// Title names the container, for the border title.
func (v *TopView) Title() string { return v.name + " processes" }

// Init kicks off the first listing.
func (v *TopView) Init() tea.Cmd { return v.Refresh() }

// Update folds a listing in.
func (v *TopView) Update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(TopRefreshMsg)
	if !ok || m.ID != v.id {
		return nil
	}
	v.loading = false
	if m.Err != nil {
		v.err = docker.FormatUserError(m.Err)
		return nil
	}
	v.err = nil
	v.procs = m.Procs
	v.rebuild()
	return nil
}

// UpdateTable forwards navigation keys.
func (v *TopView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *TopView) Resize(width, height int) {
	v.width = width
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetStyles(tableStylesWithWidth(width))
	if v.cols != nil {
		v.table.SetColumns(v.layout(v.cols, width))
	}
}

// Count is the visible process count.
func (v *TopView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first listing is outstanding.
func (v *TopView) Loading() bool { return v.loading }

// SetFilter filters processes by any column.
func (v *TopView) SetFilter(f string) {
	v.filter = f
	v.rebuild()
}

// HandleKey has nothing of its own: processes are read-only here.
func (v *TopView) HandleKey(string) (string, string) { return "", "" }

// View renders the table.
func (v *TopView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	return fixSelectedRow(v.table.View())
}

// Refresh relists the processes.
func (v *TopView) Refresh() tea.Cmd {
	if v.client == nil {
		return nil
	}
	id, client := v.id, v.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(20 * time.Second)
		defer cancel()
		procs, err := client.Top(ctx, id)
		return TopRefreshMsg{ID: id, Procs: procs, Err: err}
	}
}

// topColumns sizes each ps column to its widest value, the last (the
// command line) taking whatever room fitColumns leaves it.
func topColumns(p docker.Processes) []table.Column {
	cols := make([]table.Column, len(p.Titles))
	for i, t := range p.Titles {
		w := lipgloss.Width(t)
		for _, r := range p.Rows {
			if i < len(r) {
				w = max(w, lipgloss.Width(r[i]))
			}
		}
		if i < len(p.Titles)-1 {
			w = min(w, 20)
		} else {
			w = max(w, 20)
		}
		cols[i] = table.Column{Title: t, Width: w}
	}
	return cols
}

func (v *TopView) rebuild() {
	cols := topColumns(v.procs)
	// A table's rows must never be wider than its columns: set the columns
	// first, and clear the rows when the shape changes.
	if len(cols) != len(v.cols) {
		v.table.SetRows(nil)
	}
	v.cols = cols
	v.table.SetColumns(v.layout(cols, v.width))

	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.procs.Rows))
	for _, r := range v.procs.Rows {
		if !f.Empty() && !f.MatchesAny(r...) {
			continue
		}
		row := make(table.Row, len(cols))
		copy(row, r)
		rows = append(rows, row)
	}
	sortRows[struct{}](&v.tableSort, rows, nil)
	setTableRows(&v.table, rows)
}

// NewDiffView shows `docker diff` for container id: what it has written
// over its image, A added / C changed / D deleted. Refetched with the poll.
func NewDiffView(client *docker.Client, id, name string) *InspectView {
	v := NewInspectFetchView(name+" diff", func(ctx context.Context) ([]byte, error) {
		changes, err := client.Diff(ctx, id)
		if err != nil {
			return nil, err
		}
		return []byte(strings.Join(FormatDiff(changes), "\n")), nil
	})
	v.plain = true
	return v
}

// FormatDiff renders filesystem changes, one per line, colored by kind.
func FormatDiff(changes []docker.FileChange) []string {
	if len(changes) == 0 {
		return []string{"", "  No changes — the container has written nothing over its image."}
	}
	added, changed, deleted := 0, 0, 0
	lines := make([]string, 0, len(changes)+2)
	for _, ch := range changes {
		var mark string
		switch ch.Kind {
		case "A":
			added++
			mark = style.Success.Render("A")
		case "D":
			deleted++
			mark = lipgloss.NewStyle().Foreground(style.ColorRed).Render("D")
		default:
			changed++
			mark = lipgloss.NewStyle().Foreground(style.ColorOrange).Render("C")
		}
		lines = append(lines, "  "+mark+" "+ch.Path)
	}
	summary := style.Muted.Render(fmt.Sprintf("  %d added, %d changed, %d deleted", added, changed, deleted))
	return append([]string{summary, ""}, lines...)
}

// NewStatsView shows container id's full stats sample, refetched with the
// poll: the numbers behind the containers view's CPU and MEM columns.
func NewStatsView(client *docker.Client, id, name string) *InspectView {
	v := NewInspectFetchView(name+" stats", func(ctx context.Context) ([]byte, error) {
		s, err := client.StatsFor(ctx, id)
		if err != nil {
			return nil, err
		}
		return []byte(strings.Join(FormatStats(s), "\n")), nil
	})
	v.plain = true
	return v
}

// FormatStats renders one stats sample.
func FormatStats(s docker.Stats) []string {
	label := func(l string) string { return style.Muted.Render(fmt.Sprintf("  %-12s", l)) }
	limit := docker.HumanSize(s.MemLimit)
	return []string{
		"",
		label("CPU") + fmt.Sprintf("%.2f%%", s.CPUPerc) +
			style.Muted.Render(fmt.Sprintf("   %.1f%% of %d CPUs", s.CPUShare(), s.CPUs)),
		label("Memory") + docker.HumanSize(s.MemUsage) +
			style.Muted.Render(fmt.Sprintf("   %.1f%% of %s", s.MemPerc(), limit)),
		label("PIDs") + fmt.Sprint(s.PIDs),
		label("Net I/O") + docker.HumanSize(s.NetRx) + style.Muted.Render(" in   ") +
			docker.HumanSize(s.NetTx) + style.Muted.Render(" out"),
		label("Block I/O") + docker.HumanSize(s.BlockRead) + style.Muted.Render(" read   ") +
			docker.HumanSize(s.BlockWrite) + style.Muted.Render(" written"),
		"",
		style.Muted.Render("  Network and block I/O are totals since the container started."),
	}
}

// Table is the view's table, for the keys every table shares.
func (v *TopView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *TopView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuild) }
