// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"cmp"
	"regexp"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/table"
	tktable "github.com/blairham/tuikit/table"
)

// tableSort is the column sort every table view carries. It is off until
// the user first presses shift+←/→, so a view keeps its own order — the
// containers view's running-first, say — until asked for another, or until
// views.yaml gives the view a sortColumn.
//
// It is also where views.yaml's column layout (#14) is applied: a view's
// columns go through layout and its rows through sortRows, and both
// project them onto the layout by title (layout.go).
type tableSort struct {
	// src is the full column set last laid out — what the view's rows are
	// built from — and width the width it was laid out at.
	src []table.Column
	// shown is the titles layout last displayed, before fitting: what the
	// sorter's column index counted, for a relayout to follow by title.
	shown []string
	// widths are the shown columns' widths as last laid out, by title,
	// for a right-aligned expression column to pad its cells to.
	widths map[string]int
	// layoutKey is the view's views.yaml key; "" never has a layout.
	layoutKey string
	sorter    *tktable.Sorter
	width     int
	// layoutSort is set while the sorter is views.yaml's sortColumn rather
	// than one the user chose, so a reload can replace it.
	layoutSort bool
}

// Sort direction keys, beside tuikit's shift+←/→ for the column.
const (
	keySortAsc  = "shift+up"
	keySortDesc = "shift+down"
)

// sortKey handles the sort keys on t: shift+←/→ move the sort column,
// shift+↑/↓ set the direction, and the first sort key pressed sorts by the
// first column. It redraws the header's direction indicator and calls
// rebuild to re-sort the rows, and reports whether key was a sort key.
func (s *tableSort) sortKey(key string, t *table.Model, rebuild func()) bool {
	switch key {
	case tktable.KeySortPrev, tktable.KeySortNext, keySortAsc, keySortDesc:
	default:
		return false
	}
	s.layoutSort = false
	first := s.sorter == nil
	if first {
		s.sorter = tktable.NewSorter(0, false)
		s.sorter.SetCompare(s.compare)
	}
	switch {
	case key == keySortAsc && s.sorter.Descending(), key == keySortDesc && !s.sorter.Descending():
		s.sorter.Reverse()
	case !first:
		s.sorter.HandleKey(key, len(t.Columns()))
	}
	t.SetColumns(s.columns(t.Columns()))
	rebuild()
	return true
}

// columns marks the sort column's title with its direction, replacing a
// mark left by an earlier sort.
func (s *tableSort) columns(cols []table.Column) []table.Column {
	out := make([]table.Column, len(cols))
	for i, c := range cols {
		c.Title = strings.TrimRight(c.Title, tktable.SortAscIndicator+tktable.SortDescIndicator)
		out[i] = c
	}
	s.ensureSorter()
	if s.sorter == nil {
		return out
	}
	return s.sorter.Columns(out)
}

// sortRows projects a view's rows onto its views.yaml columns, then sorts
// them by the active column, and items — the values the rows were made
// from, which Selected indexes by cursor — into the same order. nil items
// sorts the rows alone. Projection comes first so the sort column counts
// the columns the user sees.
//
// An expression column's cells (#61) are read here, from items: the i-th
// row was built from items[i], so before the sort reorders either, each
// row gains its expression cells after its own, where titlesOf puts their
// titles, and is projected with them.
func sortRows[T any](s *tableSort, rows []table.Row, items []T) {
	src := s.source()
	titles := s.titlesOf(src)
	if idx := s.projection(titles); idx != nil {
		exprs := s.shownExprs(columnTitles(src))
		for i, r := range rows {
			if len(exprs) > 0 {
				var item any
				if i < len(items) {
					item = items[i]
				}
				cells := make([]string, len(exprs))
				for k, e := range exprs {
					cells[k] = e.cell(s.layoutKey, item)
				}
				r = withExprCells(r, len(src), cells)
			}
			rows[i] = projectRow(r, idx, len(titles))
		}
		s.alignRight(rows, s.shownTitles(titles))
	}
	s.ensureSorter()
	if s.sorter == nil || len(rows) < 2 {
		return
	}
	// Carry each row's original index through the sort in a cell past the
	// last column, where the table never draws it.
	from := make(map[string]int, len(rows))
	for i := range rows {
		k := strconv.Itoa(i)
		from[k] = i
		rows[i] = append(rows[i], k)
	}
	s.sorter.Sort(rows)
	var old []T
	if items != nil {
		old = append([]T(nil), items...)
	}
	for i, r := range rows {
		last := len(r) - 1
		if old != nil {
			items[i] = old[from[r[last]]]
		}
		rows[i] = r[:last]
	}
}

// alignRight pads the cells of every right-aligned (R) expression column
// shown to its width as last laid out, found by title.
func (s *tableSort) alignRight(rows []table.Row, shown []string) {
	for col, title := range shown {
		e, ok := s.exprNamed(title)
		if !ok || !e.Right {
			continue
		}
		w := s.widths[title]
		for _, r := range rows {
			if col < len(r) {
				r[col] = alignRight(r[col], w)
			}
		}
	}
}

// compare is the sorter's comparison: an N expression column's, found by
// the shown column's title, or cellCompare.
func (s *tableSort) compare(col int, a, b string) int {
	if col < len(s.shown) {
		if e, ok := s.exprNamed(s.shown[col]); ok && e.Numeric {
			return numericCompare(a, b)
		}
	}
	return cellCompare(col, a, b)
}

// magnitudeRe matches the cells dockmaster writes as a number and a unit:
// ages (18s, 5m, 3h, 2d, 1y) and sizes (512B, 24.1MB, 2GiB).
var magnitudeRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)(s|m|h|d|y|B|kB|KB|MB|GB|TB|PB|KiB|MiB|GiB|TiB)$`)

var unitScale = map[string]float64{
	"s": 1, "m": 60, "h": 3600, "d": 86400, "y": 365 * 86400,
	"B": 1, "kB": 1e3, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "PB": 1e15,
	"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40,
}

// magnitude reads an age or size cell as a number of seconds or bytes.
func magnitude(s string) (float64, bool) {
	m := magnitudeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return n * unitScale[m[2]], true
}

// cellCompare orders ages and sizes by what they measure — 2h after 45m,
// 1.2GB after 311MB — and everything else as tuikit does: numbers as
// numbers, text without regard to case.
func cellCompare(_ int, a, b string) int {
	if x, ok := magnitude(a); ok {
		if y, ok := magnitude(b); ok {
			return cmp.Compare(x, y)
		}
	}
	return tktable.NaturalCompare(a, b)
}

// remapSort moves the sort to the same-named column in after, keeping its
// direction, when a view swaps its column set; a column that went away
// sorts by the first. Both sets are the view's full ones, and are compared
// as displayed — through the views.yaml layout — since that is what the
// sorter's column counts.
func (s *tableSort) remapSort(before, after []table.Column) {
	s.followSort(s.shownTitles(s.titlesOf(before)), s.shownTitles(s.titlesOf(after)))
}

// followSort moves the sorter from a column of before to the column of the
// same title in after, both displayed titles; one that went away sorts by
// the first. views.yaml's sortColumn is dropped instead, for ensureSorter
// to find again by its title — or not, if the column is not shown now.
func (s *tableSort) followSort(before, after []string) {
	if s.layoutSort {
		s.sorter, s.layoutSort = nil, false
		return
	}
	if s.sorter == nil {
		return
	}
	title := ""
	if c := s.sorter.Column(); c < len(before) {
		title = before[c]
	}
	col := 0
	for i, t := range after {
		if t == title {
			col = i
		}
	}
	desc := s.sorter.Descending()
	s.sorter = tktable.NewSorter(col, desc)
	s.sorter.SetCompare(s.compare)
}
