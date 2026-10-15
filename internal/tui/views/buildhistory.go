// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// BuildHistorySource is what the builds view reads: docker.BuildHistory, or
// one over a fake runner in tests.
type BuildHistorySource interface {
	List(ctx context.Context) ([]docker.BuildRecord, error)
	Detail(ctx context.Context, id string) (docker.BuildDetail, error)
}

// BuildsRefreshMsg carries a history listing and the error lines read for
// failed records not seen before, by record ID.
type BuildsRefreshMsg struct {
	Err     error
	View    *BuildsView
	Errors  map[string]string
	Records []docker.BuildRecord
}

// buildHistoryTimeout bounds one listing: a buildx CLI call, plus one
// inspect per new failure.
const buildHistoryTimeout = 30 * time.Second

// maxErrorReads is how many failed records' reasons one refresh reads:
// each is an inspect, and a history of a hundred failures should fill in
// over a few polls rather than hold one up.
const maxErrorReads = 5

// colError is the column a failure's reason goes in, here and in the
// builders view; it grows with the screen (flexibleColumns).
const colError = "ERROR"

// statusRunningLocal is a dockmaster build's status, above the history.
const statusRunningLocal = "RUNNING"

// buildRow is one row: a build dockmaster is running, or a buildx record.
type buildRow struct {
	local *Build
	rec   docker.BuildRecord
}

// BuildsView is the build history (#118), as Docker Desktop's Builds tab:
// every record the builder keeps — `docker buildx history ls` — newest
// first, with dockmaster's own running builds (#121) above them. A failed
// record's reason is read once, by inspect, and kept for the session.
type BuildsView struct {
	tableSort
	tableMarks
	err        error
	src        BuildHistorySource
	errLines   map[string]string
	filter     string
	all        []docker.BuildRecord
	local      []*Build
	visible    []buildRow
	table      table.Model
	loading    bool
	inFlight   bool
	errorsOnly bool
}

// NewBuildsView builds the view over src; nil is a history that cannot be
// read (no docker CLI), which err says.
func NewBuildsView(src BuildHistorySource, err error) *BuildsView {
	t := table.New(
		table.WithColumns(buildsColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &BuildsView{
		tableSort: tableSort{layoutKey: "builds"},
		src:       src, err: err, errLines: map[string]string{},
		table: t, loading: src != nil,
	}
}

func buildsColumns() []table.Column {
	return []table.Column{
		{Title: colName, Width: 32},
		{Title: colStatus, Width: 10},
		{Title: "STEPS", Width: 7},
		{Title: "CACHED", Width: 7},
		{Title: "DURATION", Width: 9},
		{Title: colAge, Width: 6},
		{Title: colError, Width: 40},
	}
}

// Init reads the history.
func (v *BuildsView) Init() tea.Cmd { return v.Refresh() }

// Refresh lists the history again, single-flighted, and reads the reasons
// of up to maxErrorReads failed records not seen before.
func (v *BuildsView) Refresh() tea.Cmd {
	if v.src == nil || v.inFlight {
		return nil
	}
	v.inFlight = true
	src := v.src
	known := make(map[string]bool, len(v.errLines))
	for id := range v.errLines {
		known[id] = true
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), buildHistoryTimeout)
		defer cancel()
		recs, err := src.List(ctx)
		if err != nil {
			return BuildsRefreshMsg{View: v, Err: err}
		}
		lines := map[string]string{}
		for _, r := range recs {
			if len(lines) == maxErrorReads {
				break
			}
			if r.Status != docker.BuildError || known[r.ID()] {
				continue
			}
			// A failed read is kept as no reason, so it is not asked again
			// every poll; o shows the record whatever the error column says.
			d, _ := src.Detail(ctx, r.ID()) //nolint:errcheck // best-effort column
			lines[r.ID()] = d.ErrorLine()
		}
		return BuildsRefreshMsg{View: v, Records: recs, Errors: lines}
	}
}

// SetLocal is dockmaster's running builds, listed above the history.
func (v *BuildsView) SetLocal(builds []*Build) {
	v.local = builds
	v.rebuildRows()
}

// ToggleErrors lists only failed builds, or every build again.
func (v *BuildsView) ToggleErrors() {
	v.errorsOnly = !v.errorsOnly
	v.rebuildRows()
}

// ErrorsOnly reports whether only failed builds are listed.
func (v *BuildsView) ErrorsOnly() bool { return v.errorsOnly }

// Status is the filter in force, for the title.
func (v *BuildsView) Status() string {
	if v.errorsOnly {
		return "errors"
	}
	return ""
}

// Update folds a listing in; one for another instance is dropped.
func (v *BuildsView) Update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(BuildsRefreshMsg)
	if !ok || m.View != v {
		return nil
	}
	v.loading, v.inFlight, v.err = false, false, m.Err
	if m.Err == nil {
		v.all = m.Records
	}
	for id, line := range m.Errors {
		v.errLines[id] = line
	}
	v.rebuildRows()
	return nil
}

// selected is the row under the cursor.
func (v *BuildsView) selected() (buildRow, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return buildRow{}, false
	}
	return v.visible[i], true
}

// recordParam is a record's ID and name, for an action.
func recordParam(r docker.BuildRecord) string { return r.ID() + "\x00" + r.Name }

// HandleKey maps a keystroke to an app action: enter the build's log —
// a running dockmaster build's output view — o its inspect, ctrl-d
// remove the record (confirms), R build it again, x stop a running
// dockmaster build, ctrl-z failed builds only.
func (v *BuildsView) HandleKey(key string) (string, string) {
	if key == KeyCtrlZ {
		return "toggle_build_errors", ""
	}
	row, ok := v.selected()
	if !ok {
		return "", ""
	}
	if b := row.local; b != nil {
		id := strconv.FormatInt(b.ID(), 10)
		switch key {
		case KeyEnter, "o":
			return "attach_build", id
		case "x":
			return "confirm_stop_build", id
		case KeyCtrlD:
			return "build_still_running", b.Title
		}
		return "", ""
	}
	return v.recordKey(key, row.rec)
}

func (v *BuildsView) recordKey(key string, r docker.BuildRecord) (string, string) {
	switch key {
	case KeyEnter:
		return "build_logs", recordParam(r)
	case "o":
		return "build_inspect", recordParam(r)
	case KeyCtrlD:
		return "confirm_remove_build", recordParam(r)
	case "R":
		return "rebuild", recordParam(r)
	}
	return "", ""
}

// CopyFields is the selected record's name and ref.
func (v *BuildsView) CopyFields() (string, string, bool) {
	row, ok := v.selected()
	if !ok || row.local != nil {
		return "", "", false
	}
	return row.rec.Name, row.rec.Ref, true
}

// buildMarkKey identifies a row for marks: a record's ref, or a running
// build's ID, which no ref can be.
func buildMarkKey(r buildRow) string {
	if r.local != nil {
		return "local:" + strconv.FormatInt(r.local.ID(), 10)
	}
	return r.rec.Ref
}

// MarkKey marks rows on space, ctrl+space and ctrl+\.
func (v *BuildsView) MarkKey(key string) bool {
	if !markKey(&v.tableMarks, key, v.visible, v.table.Cursor(), buildMarkKey) {
		return false
	}
	v.rebuildRows()
	return true
}

// ClearMarks unmarks every row.
func (v *BuildsView) ClearMarks() {
	if v.marks != nil {
		v.marks.Clear()
		v.rebuildRows()
	}
}

// BulkKey is key's action on every marked row; a running build's row has
// none to remove, so the app skips it.
func (v *BuildsView) BulkKey(key string) []Action {
	return bulk(&v.tableMarks, v.visible, buildMarkKey, func(r buildRow) Action {
		name, param := v.keyFor(key, r)
		label := r.rec.Name
		if r.local != nil {
			label = r.local.Title
		}
		return Action{Name: name, Param: param, Label: label}
	})
}

// keyFor is key's action on row r, for a marked row; ctrl-d on a running
// build says it is running rather than removing anything.
func (v *BuildsView) keyFor(key string, r buildRow) (string, string) {
	if r.local == nil {
		return v.recordKey(key, r.rec)
	}
	if key == KeyCtrlD {
		return "build_still_running", r.local.Title
	}
	return "", ""
}

// UpdateTable forwards navigation keys.
func (v *BuildsView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *BuildsView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(buildsColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *BuildsView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first listing is outstanding.
func (v *BuildsView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *BuildsView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// View renders the table, or why there is none: a buildx without
// history says so once, rather than showing an empty table.
func (v *BuildsView) View() string {
	if v.err != nil && !v.loading {
		msg := v.err.Error()
		if errors.Is(v.err, docker.ErrNoBuildxHistory) {
			msg += " — `docker buildx version` shows which you have"
		}
		return style.Error.Render("  " + msg)
	}
	if len(v.visible) == 0 && !v.loading {
		if v.errorsOnly {
			return style.Muted.Render("  no failed builds — ctrl-z lists every build")
		}
		return style.Muted.Render("  no builds in this builder's history — :build starts one")
	}
	return fixSelectedRow(v.table.View())
}

func (v *BuildsView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.local)+len(v.all))
	v.visible = v.visible[:0]
	all := make([]buildRow, 0, len(v.local)+len(v.all))
	for _, b := range v.local {
		all = append(all, buildRow{local: b})
		if v.errorsOnly || !b.Running() || (!f.Empty() && !f.MatchesAny(b.Title, statusRunningLocal)) {
			continue
		}
		rows = append(rows, table.Row{
			truncate(b.Title, 32),
			style.StateRestarting.Render(statusRunningLocal),
			"", "",
			buildDuration(time.Since(b.Started)),
			since(b.Started),
			"",
		})
		v.visible = append(v.visible, buildRow{local: b})
	}
	now := time.Now()
	for _, r := range v.all {
		all = append(all, buildRow{rec: r})
		errLine := v.errLines[r.ID()]
		if v.errorsOnly && r.Status != docker.BuildError {
			continue
		}
		if !f.Empty() && !f.MatchesAny(r.Name, r.Status, errLine) {
			continue
		}
		rows = append(rows, table.Row{
			truncate(r.Name, 32),
			buildStatusCell(r.Status),
			fmt.Sprintf("%d/%d", r.CompletedSteps, r.TotalSteps),
			fmt.Sprintf("%d%%", r.CachedPercent()),
			buildDuration(r.Duration(now)),
			since(r.Created),
			style.Error.Render(truncate(errLine, 120)),
		})
		v.visible = append(v.visible, buildRow{rec: r})
	}
	rows = markRows(&v.tableMarks, rows, v.visible, all, buildMarkKey)
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// buildStatusCell colors a record's status as the containers view colors
// a state.
func buildStatusCell(status string) string {
	switch status {
	case docker.BuildCompleted:
		return style.Success.Render(status)
	case docker.BuildError:
		return style.Error.Render(status)
	case docker.BuildRunning:
		return style.StateRestarting.Render(status)
	}
	return status
}

// buildDuration is a build's length the way buildx prints it: tenths of a
// second under a minute, whole seconds above.
func buildDuration(d time.Duration) string {
	if d < time.Minute {
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

// Table is the view's table, for the keys every table shares.
func (v *BuildsView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *BuildsView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// Relayout lays the table out again after the column layouts changed.
func (v *BuildsView) Relayout() { v.relayout(&v.table, v.rebuildRows) }
