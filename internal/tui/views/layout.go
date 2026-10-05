// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"slices"

	"charm.land/bubbles/v2/table"
	tktable "github.com/blairham/tuikit/table"
)

// ColumnLayout is one view's entry in views.yaml (#14), already checked
// against the view's columns: which columns to show, in what order, and the
// column it opens sorted by. Titles are the views' own, upper case.
type ColumnLayout struct {
	// SortColumn, when set, is the column the view opens sorted by.
	SortColumn string
	// Columns are the titles to show, in order; empty shows them all.
	// An expression column's title is among them, in its place.
	Columns []string
	// Exprs are the expression columns (#61) Columns names, read from
	// each row's own data.
	Exprs    []ExprColumn
	SortDesc bool
}

// columnLayouts is views.yaml as the app last set it. Like the theme, it is
// package state, read and written on the UI goroutine only.
var columnLayouts map[string]ColumnLayout

// SetColumnLayouts replaces every view's column layout, by views.yaml key.
// Views already built take it up on their next layout; Relayouter applies
// it at once.
func SetColumnLayouts(m map[string]ColumnLayout) { columnLayouts = m }

// Relayouter is a table view that can lay its columns and rows out again,
// after SetColumnLayouts changed them.
type Relayouter interface {
	Relayout()
}

// columnSets are each table view's column sets, by views.yaml key. The
// first is the set the view opens with; a layout may name a title from any
// of them — the containers view's wide mode (ctrl+w) adds columns.
var columnSets = map[string][]func() []table.Column{
	"containers": {
		func() []table.Column { return containerColumns(false) },
		func() []table.Column { return containerColumns(true) },
	},
	"images":       {imageColumns},
	"volumes":      {volumeColumns},
	"networks":     {networkColumns},
	"projects":     {projectColumns},
	"runtimes":     {runtimeColumns},
	"pods":         {podColumns},
	"portforwards": {portForwardColumns},
	"diskusage":    {diskUsageColumns},
	"contexts":     {contextColumns},
	"lint":         {lintColumns},
	"dir":          {dirColumns},
	"dumps":        {dumpColumns},
	"node":         {nodeColumns},
	"layers":       {layerColumns},
	"files":        {volumeBrowseColumns},
	"scan":         {scanColumns},
	"aliases":      {aliasColumns},
}

// LayoutViews is every view views.yaml can name, sorted.
func LayoutViews() []string {
	out := make([]string, 0, len(columnSets))
	for name := range columnSets {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// ColumnTitles is every column title the named view can show, in the order
// it shows them, wide-mode columns included; false for a view views.yaml
// cannot name. An untitled marker column is not listed: it is always shown.
func ColumnTitles(name string) ([]string, bool) {
	sets, ok := columnSets[name]
	if !ok {
		return nil, false
	}
	var out []string
	for _, set := range sets {
		for _, c := range set() {
			if c.Title != "" && !slices.Contains(out, c.Title) {
				out = append(out, c.Title)
			}
		}
	}
	return out, true
}

// columnTitles is a column set's titles.
func columnTitles(cols []table.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Title
	}
	return out
}

// layoutFor is the view's layout, if views.yaml gives it one.
func (s *tableSort) layoutFor() (ColumnLayout, bool) {
	if s.layoutKey == "" {
		return ColumnLayout{}, false
	}
	l, ok := columnLayouts[s.layoutKey]
	return l, ok
}

// source is the full column set the view's rows are built from: the set it
// was last laid out with, or the one it opens with before it ever was.
func (s *tableSort) source() []table.Column {
	if s.src != nil {
		return s.src
	}
	if sets := columnSets[s.layoutKey]; len(sets) > 0 {
		return sets[0]()
	}
	return nil
}

// titlesOf is the titles a view's rows can be projected from: the column
// set's, then the layout's expression columns shown with that set. A
// row's expression cells sit at the same place after its own (sortRows).
func (s *tableSort) titlesOf(cols []table.Column) []string {
	titles := columnTitles(cols)
	for _, e := range s.shownExprs(titles) {
		titles = append(titles, e.Title)
	}
	return titles
}

// shownExprs are the layout's expression columns shown with a column set
// of these titles: a wide-only one (W) only with the wide set. Wide is
// read from the titles, never remembered: any set but the view's first.
func (s *tableSort) shownExprs(titles []string) []ExprColumn {
	l, ok := s.layoutFor()
	if !ok || len(l.Exprs) == 0 {
		return nil
	}
	sets := columnSets[s.layoutKey]
	wide := len(sets) > 1 && !slices.Equal(titles, columnTitles(sets[0]()))
	var out []ExprColumn
	for _, e := range l.Exprs {
		if !e.Wide || wide {
			out = append(out, e)
		}
	}
	return out
}

// exprNamed is the layout's expression column of this displayed title.
func (s *tableSort) exprNamed(title string) (ExprColumn, bool) {
	l, ok := s.layoutFor()
	if !ok {
		return ExprColumn{}, false
	}
	for _, e := range l.Exprs {
		if e.Title == title {
			return e, true
		}
	}
	return ExprColumn{}, false
}

// projection maps the displayed columns onto titles, a view's full set: the
// i-th displayed column is titles[idx[i]]. It is computed from the titles
// each time, never cached by position, because the set changes under it —
// wide mode adds columns. An untitled marker column leads; a layout title
// not in titles right now is skipped. nil means titles as they are.
func (s *tableSort) projection(titles []string) []int {
	l, ok := s.layoutFor()
	if !ok || len(l.Columns) == 0 {
		return nil
	}
	var idx []int
	for i, t := range titles {
		if t == "" {
			idx = append(idx, i)
		}
	}
	for _, want := range l.Columns {
		if i := slices.Index(titles, want); i >= 0 {
			idx = append(idx, i)
		}
	}
	return idx
}

// shownTitles is titles as displayed: projected onto the layout.
func (s *tableSort) shownTitles(titles []string) []string {
	idx := s.projection(titles)
	if idx == nil {
		return titles
	}
	out := make([]string, len(idx))
	for i, j := range idx {
		out[i] = titles[j]
	}
	return out
}

// withExprCells is r with expression cells inserted after its ncols
// cells, before any past them that no column draws.
func withExprCells(r table.Row, ncols int, cells []string) table.Row {
	out := make(table.Row, 0, max(len(r), ncols)+len(cells))
	out = append(out, r[:min(len(r), ncols)]...)
	for len(out) < ncols {
		out = append(out, "")
	}
	out = append(out, cells...)
	if len(r) > ncols {
		out = append(out, r[ncols:]...)
	}
	return out
}

// projectRow is r's cells in the displayed order. Cells past the column
// set, which no column draws, are carried after them.
func projectRow(r table.Row, idx []int, ncols int) table.Row {
	out := make(table.Row, 0, len(idx)+max(len(r)-ncols, 0))
	for _, j := range idx {
		if j < len(r) {
			out = append(out, r[j])
		} else {
			out = append(out, "")
		}
	}
	if len(r) > ncols {
		out = append(out, r[ncols:]...)
	}
	return out
}

// layout is a view's columns for a table width-wide: cols, the view's full
// set, projected onto its views.yaml layout — expression columns included
// — fitted to the width, and marked with the sort direction. It records
// cols as the set the rows are built from, so sortRows projects them the
// same way, and each shown column's width, by title, for a right-aligned
// expression column's cells.
func (s *tableSort) layout(cols []table.Column, width int) []table.Column {
	s.src, s.width = cols, width
	titles := s.titlesOf(cols)
	idx := s.projection(titles)
	if idx == nil {
		s.shown, s.widths = columnTitles(cols), nil
		return s.columns(fitColumns(cols, width))
	}
	shown := make([]table.Column, len(idx))
	for i, j := range idx {
		if j < len(cols) {
			shown[i] = cols[j]
			continue
		}
		e, _ := s.exprNamed(titles[j])
		shown[i] = e.column(s.layoutKey)
	}
	s.shown = columnTitles(shown)
	fitted := fillWidth(fitColumns(shown, width), width, func(title string) bool {
		e, ok := s.exprNamed(title)
		return ok && e.Right
	})
	s.widths = make(map[string]int, len(fitted))
	for _, c := range fitted {
		s.widths[c.Title] = c.Width
	}
	return s.columns(fitted)
}

// fillWidth widens the last column to take up any room fitColumns left:
// a layout that keeps no free-text column has nothing for it to grow, and
// would leave the table short of the screen. A right-aligned column is
// passed over (fixed), so its cells stay against its edge.
func fillWidth(cols []table.Column, width int, fixed func(title string) bool) []table.Column {
	if width <= 0 || len(cols) == 0 {
		return cols
	}
	if slack := width - tableWidth(cols); slack > 0 {
		for i := len(cols) - 1; i >= 0; i-- {
			if i == 0 || !fixed(cols[i].Title) {
				cols[i].Width += slack
				break
			}
		}
	}
	return cols
}

// ensureSorter starts the views.yaml sortColumn's sort, when the view has
// one and no sort yet. A later sort key takes over from it.
func (s *tableSort) ensureSorter() {
	if s.sorter != nil {
		return
	}
	l, ok := s.layoutFor()
	if !ok || l.SortColumn == "" {
		return
	}
	col := slices.Index(s.shownTitles(s.titlesOf(s.source())), l.SortColumn)
	if col < 0 {
		// Not shown right now — a wide-mode column outside wide mode.
		return
	}
	s.sorter = tktable.NewSorter(col, l.SortDesc)
	s.sorter.SetCompare(s.compare)
	s.layoutSort = true
}

// relayout lays t out again after SetColumnLayouts: the columns from the
// view's full set, the rows rebuilt to match, the cursor kept. A sort the
// user chose follows its column by title; the layout's own sort is
// replaced by the new layout's.
func (s *tableSort) relayout(t *table.Model, rebuild func()) {
	src := s.source()
	s.followSort(s.shown, s.shownTitles(s.titlesOf(src)))
	cursor := t.Cursor()
	// Rows are never wider than the columns: clear them, set the new
	// columns, then rebuild the rows to match.
	t.SetRows(nil)
	t.SetColumns(s.layout(src, s.width))
	rebuild()
	if n := len(t.Rows()); n > 0 {
		t.SetCursor(min(max(cursor, 0), n-1))
	}
}

// Every view views.yaml can name relayouts on a reload.
var (
	_ Relayouter = (*ContainersView)(nil)
	_ Relayouter = (*ImagesView)(nil)
	_ Relayouter = (*VolumesView)(nil)
	_ Relayouter = (*NetworksView)(nil)
	_ Relayouter = (*ProjectsView)(nil)
	_ Relayouter = (*RuntimesView)(nil)
	_ Relayouter = (*PodsView)(nil)
	_ Relayouter = (*PortForwardsView)(nil)
	_ Relayouter = (*DiskUsageView)(nil)
	_ Relayouter = (*ContextsView)(nil)
	_ Relayouter = (*LintView)(nil)
	_ Relayouter = (*DirView)(nil)
	_ Relayouter = (*DumpsView)(nil)
	_ Relayouter = (*NodeView)(nil)
	_ Relayouter = (*LayersView)(nil)
	_ Relayouter = (*VolumeBrowseView)(nil)
	_ Relayouter = (*ScanView)(nil)
)
