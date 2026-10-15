// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// The builders view's actions; each param is the builder's name.
const (
	ActInspectBuilder  = "inspect_builder"
	ActStartBuilder    = "builder_start"
	ActStopBuilder     = "builder_stop"
	ActUseBuilder      = "builder_use"
	ActRemoveBuilder   = "confirm_remove_builder"
	ActPruneBuilder    = "confirm_prune_builder"
	ActBuilderForm     = "builder_form"
	ActBuilderCreate   = "builder_create"
	ActBuildersRefresh = "builders_refresh"
)

// BuildersRefreshMsg carries `docker buildx ls`.
type BuildersRefreshMsg struct {
	Err      error
	Builders []docker.BuildxBuilder
}

// ListBuildersFunc lists the builders; the app hands the view one bound to
// the daemon on screen, and tests a fake.
type ListBuildersFunc func(ctx context.Context) ([]docker.BuildxBuilder, error)

// builderListTimeout bounds one listing: buildx asks every builder for its
// status, and a remote or kubernetes one can be slow to answer (buildx
// gives each 20 s).
const builderListTimeout = 60 * time.Second

// BuildersView lists the buildx builders (#119), as Docker Desktop's Builds
// area does: driver, endpoint, status, BuildKit version and platforms, the
// current one marked. Listing asks every builder for its status, so it
// loads when opened and on r, never on the poll.
type BuildersView struct {
	tableSort
	err     error
	list    ListBuildersFunc
	filter  string
	all     []docker.BuildxBuilder
	visible []docker.BuildxBuilder
	table   table.Model
	loading bool
}

// NewBuildersView builds the builders view over list.
func NewBuildersView(list ListBuildersFunc) *BuildersView {
	t := table.New(
		table.WithColumns(builderColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &BuildersView{tableSort: tableSort{layoutKey: "builders"}, table: t, list: list, loading: true}
}

// Column titles the builders view shares with others.
const (
	colName     = "NAME"
	colDriver   = "DRIVER"
	colEndpoint = "ENDPOINT"
	colStatus   = "STATUS"
)

func builderColumns() []table.Column {
	return []table.Column{
		{Title: "", Width: 2},
		{Title: colName, Width: 24},
		{Title: colDriver, Width: 16},
		{Title: colEndpoint, Width: 30},
		{Title: colStatus, Width: 9},
		{Title: "BUILDKIT", Width: 10},
		{Title: "PLATFORMS", Width: 30},
		{Title: "ERROR", Width: 40},
	}
}

// Init lists the builders.
func (v *BuildersView) Init() tea.Cmd { return v.Refresh() }

// Refresh lists them again.
func (v *BuildersView) Refresh() tea.Cmd {
	v.loading = true
	list := v.list
	return func() tea.Msg {
		if list == nil {
			return BuildersRefreshMsg{Err: docker.ErrNoBuildx}
		}
		ctx, cancel := context.WithTimeout(context.Background(), builderListTimeout)
		defer cancel()
		bs, err := list(ctx)
		return BuildersRefreshMsg{Builders: bs, Err: err}
	}
}

// Selected returns the builder under the cursor.
func (v *BuildersView) Selected() (docker.BuildxBuilder, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.BuildxBuilder{}, false
	}
	return v.visible[i], true
}

// Builder finds a listed builder by name.
func (v *BuildersView) Builder(name string) (docker.BuildxBuilder, bool) {
	for _, b := range v.all {
		if b.Name == name {
			return b, true
		}
	}
	return docker.BuildxBuilder{}, false
}

// Update folds the listing in.
func (v *BuildersView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(BuildersRefreshMsg); ok {
		v.loading = false
		v.err = m.Err
		v.all = m.Builders
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *BuildersView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *BuildersView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(builderColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *BuildersView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the listing is outstanding.
func (v *BuildersView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *BuildersView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey maps a keystroke to an app action: enter and o inspect, u / x
// start and stop, U makes the builder the default, P prunes its cache and
// ctrl+d removes it (both confirm), ctrl+n creates one.
func (v *BuildersView) HandleKey(key string) (string, string) {
	if key == KeyCtrlN {
		return ActBuilderForm, ""
	}
	b, ok := v.Selected()
	if !ok {
		return "", ""
	}
	switch key {
	// enter is the builder's build history once :builds (#118) lands;
	// until then it inspects, as o does.
	case KeyEnter, "o":
		return ActInspectBuilder, b.Name
	case "u":
		return ActStartBuilder, b.Name
	case "x":
		return ActStopBuilder, b.Name
	case "U":
		return ActUseBuilder, b.Name
	case "P":
		return ActPruneBuilder, b.Name
	case KeyCtrlD:
		return ActRemoveBuilder, b.Name
	}
	return "", ""
}

// View renders the table.
func (v *BuildersView) View() string {
	if v.err != nil && !v.loading {
		return style.Error.Render("  " + v.err.Error())
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  no buildx builders — ctrl-n creates one")
	}
	return fixSelectedRow(v.table.View())
}

// builderStatus colors a builder's status: running green, error red, the
// rest (stopped, inactive) muted.
func builderStatus(s string) string {
	switch s {
	case statusRunning:
		return style.Success.Render(s)
	case "error":
		return style.Error.Render(s)
	}
	return style.Muted.Render(s)
}

func (v *BuildersView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, b := range v.all {
		platforms := strings.Join(b.Platforms(), ", ")
		if !f.Empty() && !f.MatchesAny(b.Name, b.Driver, b.Endpoint(), b.Status(), platforms) {
			continue
		}
		marker, name := " ", b.Name
		if b.Current {
			marker = style.Success.Render("▸")
			name = style.Success.Render(b.Name)
		}
		driver := b.Driver
		if driver == "" {
			driver = style.Muted.Render("—")
		}
		rows = append(rows, table.Row{
			marker,
			truncate(name, 24),
			driver,
			truncate(b.Endpoint(), 30),
			builderStatus(b.Status()),
			b.BuildKit(),
			truncate(platforms, 30),
			truncate(cleanCell(b.Reason()), 40),
		})
		v.visible = append(v.visible, b)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// Table is the view's table, for the keys every table shares.
func (v *BuildersView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *BuildersView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// Relayout lays the table out again after the column layouts changed.
func (v *BuildersView) Relayout() { v.relayout(&v.table, v.rebuildRows) }

// CopyFields is the selected builder's name, which is also how buildx
// names it everywhere.
func (v *BuildersView) CopyFields() (string, string, bool) {
	b, ok := v.Selected()
	return b.Name, b.Name, ok
}
