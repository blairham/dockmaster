// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// ColumnLayouts validates views.yaml (#14) before the UI starts, as the
// hotkeys file is: every view is one dockmaster has, every column one that
// view shows (in any mode — the containers view's wide columns count), no
// column twice, and sortColumn is COLUMN, COLUMN:asc or COLUMN:desc on a
// column that is shown. Column names match regardless of case and come
// back as the view spells them.
func ColumnLayouts(entries map[string]config.ViewColumns) (map[string]views.ColumnLayout, error) {
	var errs []string
	out := map[string]views.ColumnLayout{}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		titles, ok := views.ColumnTitles(name)
		if !ok {
			errs = append(errs, fmt.Sprintf("unknown view %q (views are %s)",
				name, strings.Join(views.LayoutViews(), ", ")))
			continue
		}
		l, lerrs := columnLayout(entries[name], titles)
		for _, e := range lerrs {
			errs = append(errs, fmt.Sprintf("%s: %s", name, e))
		}
		if len(lerrs) == 0 {
			out[name] = l
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s: %s", config.ViewsFileName, strings.Join(errs, "; "))
	}
	if len(out) == 0 {
		return nil, nil //nolint:nilnil // no views.yaml, or an empty one: no layouts
	}
	return out, nil
}

// columnLayout checks one view's entry against titles, the view's columns.
func columnLayout(e config.ViewColumns, titles []string) (views.ColumnLayout, []string) {
	var (
		l    views.ColumnLayout
		errs []string
	)
	known := strings.Join(titles, ", ")
	for _, c := range e.Columns {
		title, ok := matchTitle(c, titles)
		switch {
		case !ok:
			errs = append(errs, fmt.Sprintf("unknown column %q (columns are %s)", c, known))
		case slices.Contains(l.Columns, title):
			errs = append(errs, fmt.Sprintf("column %q is listed twice", c))
		default:
			l.Columns = append(l.Columns, title)
		}
	}
	if e.SortColumn == "" {
		return l, errs
	}
	col, dir, hasDir := cutLast(e.SortColumn, ":")
	switch {
	case hasDir && !strings.EqualFold(dir, "asc") && !strings.EqualFold(dir, "desc"):
		errs = append(errs, fmt.Sprintf("sortColumn %q: the direction is asc or desc, as in AGE:desc", e.SortColumn))
		return l, errs
	case strings.TrimSpace(col) == "":
		errs = append(errs, fmt.Sprintf("sortColumn %q: no column, as in AGE:desc", e.SortColumn))
		return l, errs
	}
	title, ok := matchTitle(col, titles)
	switch {
	case !ok:
		errs = append(errs, fmt.Sprintf("sortColumn %q: unknown column %q (columns are %s)", e.SortColumn, col, known))
	case len(l.Columns) > 0 && !slices.Contains(l.Columns, title):
		errs = append(errs, fmt.Sprintf("sortColumn %q: %s is not among the columns shown (%s)",
			e.SortColumn, title, strings.Join(l.Columns, ", ")))
	default:
		l.SortColumn, l.SortDesc = title, strings.EqualFold(dir, "desc")
	}
	return l, errs
}

// matchTitle is name as titles spells it, ignoring case and surrounding
// space.
func matchTitle(name string, titles []string) (string, bool) {
	name = strings.TrimSpace(name)
	for _, t := range titles {
		if strings.EqualFold(t, name) {
			return t, true
		}
	}
	return "", false
}

// cutLast splits s around the last sep: a column title may itself hold
// nothing but letters, spaces and %, so the direction is what follows the
// last colon.
func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}
