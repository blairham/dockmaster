// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// LintRefreshMsg carries a finished lint.
type LintRefreshMsg struct {
	Err     error
	Results []docker.LintResult
}

// RunLint inspects every container and checks it; a var so tests can feed
// the view without a daemon.
var RunLint = func(ctx context.Context, c *docker.Client) ([]docker.LintResult, error) {
	return c.Lint(ctx)
}

// LintView lists each container with what container lint (#10) found in it,
// worst first: the settings that weaken isolation or make a service fragile.
// It costs one inspect per container, so it loads when opened and on r, not
// on the poll.
type LintView struct {
	tableSort
	client  *docker.Client
	err     error
	filter  string
	all     []docker.LintResult
	visible []docker.LintResult
	table   table.Model
	loading bool
}

// NewLintView builds the lint view.
func NewLintView(client *docker.Client) *LintView {
	t := table.New(
		table.WithColumns(lintColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &LintView{tableSort: tableSort{layoutKey: "lint"}, client: client, table: t, loading: true}
}

func lintColumns() []table.Column {
	return []table.Column{
		{Title: "WORST", Width: 6},
		{Title: "NAME", Width: 28},
		{Title: "FINDINGS", Width: 8},
		{Title: "FIRST FINDING", Width: 80},
	}
}

// Init runs the lint.
func (v *LintView) Init() tea.Cmd { return v.Refresh() }

// Refresh re-runs the lint.
func (v *LintView) Refresh() tea.Cmd {
	c := v.client
	v.loading = true
	return func() tea.Msg {
		// One inspect per container: on a slow daemon this is the long pole,
		// so it gets the image list's sort of budget, not a single request's.
		ctx, cancel := c.RequestContext(2 * time.Minute)
		defer cancel()
		res, err := RunLint(ctx, c)
		return LintRefreshMsg{Results: res, Err: err}
	}
}

// Update folds a finished lint in, worst containers first.
func (v *LintView) Update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(LintRefreshMsg)
	if !ok {
		return nil
	}
	v.loading, v.err = false, m.Err
	v.all = slices.Clone(m.Results)
	slices.SortStableFunc(v.all, func(a, b docker.LintResult) int {
		if d := lintRank(b) - lintRank(a); d != 0 {
			return d
		}
		return strings.Compare(a.Container.Name, b.Container.Name)
	})
	v.rebuildRows()
	return nil
}

// lintRank orders rows: any risk first, then by how much was found.
func lintRank(r docker.LintResult) int {
	w, found := r.Worst()
	if !found {
		return 0
	}
	return int(w)*100 + len(r.Findings) + 1
}

func (v *LintView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, r := range v.all {
		first := ""
		if len(r.Findings) > 0 {
			first = r.Findings[0].Message
		}
		if r.Err != nil {
			first = r.Err.Error()
		}
		if !f.Empty() && !f.Match([]string{r.Container.Name, r.Container.Image, first, lintRules(r)}, r.Container.Labels) {
			continue
		}
		rows = append(rows, table.Row{
			lintWorst(r),
			truncate(r.Container.Name, 28),
			fmt.Sprintf("%d", len(r.Findings)),
			truncate(first, 200),
		})
		v.visible = append(v.visible, r)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// lintRules is the row's rule names, so /privileged filters to them.
func lintRules(r docker.LintResult) string {
	names := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		names = append(names, f.Rule)
	}
	return strings.Join(names, " ")
}

func lintWorst(r docker.LintResult) string {
	w, found := r.Worst()
	switch {
	case r.Err != nil:
		return style.Muted.Render("?")
	case !found:
		return style.Success.Render("ok")
	case w == docker.Risk:
		return style.StateDead.Render("risk")
	case w == docker.Warn:
		return style.StatePaused.Render("warn")
	default:
		return style.Muted.Render("info")
	}
}

// Selected is the row under the cursor.
func (v *LintView) Selected() (docker.LintResult, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.LintResult{}, false
	}
	return v.visible[i], true
}

// LintReport is one container's findings as text, for the report view.
func LintReport(r docker.LintResult) string {
	if r.Err != nil {
		return r.Err.Error()
	}
	if len(r.Findings) == 0 {
		return r.Container.Name + ": nothing found"
	}
	var b strings.Builder
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "%-5s %-16s %s\n", f.Severity, f.Rule, f.Message)
	}
	return strings.TrimRight(b.String(), "\n")
}

// HandleKey maps a keystroke to an app action.
func (v *LintView) HandleKey(key string) (string, string) {
	r, ok := v.Selected()
	if !ok {
		return "", ""
	}
	switch key {
	case KeyEnter:
		return "lint_detail", r.Container.ID
	case "o":
		return actInspectContainer, r.Container.ID
	}
	return "", ""
}

// Result is the lint result for one container.
func (v *LintView) Result(id string) (docker.LintResult, bool) {
	for _, r := range v.all {
		if r.Container.ID == id {
			return r, true
		}
	}
	return docker.LintResult{}, false
}

// UpdateTable forwards navigation keys.
func (v *LintView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *LintView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(lintColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *LintView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the lint is running.
func (v *LintView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *LintView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// View renders the table.
func (v *LintView) View() string {
	if v.err != nil {
		return style.Error.Render("  " + docker.FormatUserError(v.err).Error())
	}
	if len(v.all) == 0 && !v.loading {
		return style.Muted.Render("  no containers to lint")
	}
	return fixSelectedRow(v.table.View())
}

// Table is the view's table, for the keys every table shares.
func (v *LintView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *LintView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// CopyFields is the selected container's name and ID.
func (v *LintView) CopyFields() (string, string, bool) {
	r, ok := v.Selected()
	return r.Container.Name, r.Container.ID, ok
}

// Relayout lays the table out again after the column layouts changed.
func (v *LintView) Relayout() { v.relayout(&v.table, v.rebuildRows) }
