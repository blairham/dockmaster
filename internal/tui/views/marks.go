// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"charm.land/bubbles/v2/table"
	tktable "github.com/blairham/tuikit/table"
	"github.com/charmbracelet/x/ansi"
)

// Action is one row's (action, param) and a label for it, which a bulk key
// produces for every marked row.
type Action struct {
	Name, Param, Label string
}

// tableMarks marks rows for bulk actions with tuikit's Marks. It keys on
// the rows' items — a container ID, not the name cell, which is truncated
// and could match two containers — by handing Marks one-cell rows of keys
// laid out like the table's.
type tableMarks struct {
	marks *tktable.Marks
}

func (m *tableMarks) set() *tktable.Marks {
	if m.marks == nil {
		m.marks = tktable.NewMarks(nil)
	}
	return m.marks
}

// keyRows is items as one-cell rows of their keys.
func keyRows[T any](items []T, key func(T) string) []table.Row {
	out := make([]table.Row, len(items))
	for i, it := range items {
		out[i] = table.Row{key(it)}
	}
	return out
}

// markRows drops marks whose items are gone from all, and draws the marked
// rows — rows and items in display order — in the theme's mark style.
func markRows[T any](m *tableMarks, rows []table.Row, items, all []T, key func(T) string) []table.Row {
	if m.marks == nil || m.marks.Len() == 0 {
		return rows
	}
	m.marks.Prune(keyRows(all, key))
	for i, it := range items {
		if !m.marks.IsMarked(table.Row{key(it)}) {
			continue
		}
		styled := make(table.Row, len(rows[i]))
		for j, c := range rows[i] {
			styled[j] = pkgTheme.MarkStyle.Render(ansi.Strip(c))
		}
		rows[i] = styled
	}
	return rows
}

// markKey applies space, ctrl+space and ctrl+\ to items, the rows in
// display order with the cursor at cursor, and reports whether key was one.
func markKey[T any](m *tableMarks, key string, items []T, cursor int, keyOf func(T) string) bool {
	return m.set().HandleKey(key, keyRows(items, keyOf), cursor)
}

// marked is the marked items, in display order.
func marked[T any](m *tableMarks, items []T, key func(T) string) []T {
	if m.marks == nil || m.marks.Len() == 0 {
		return nil
	}
	var out []T
	for _, it := range items {
		if m.marks.IsMarked(table.Row{key(it)}) {
			out = append(out, it)
		}
	}
	return out
}

// bulk maps a key over the marked items: each one's action as the key
// would give it for that row alone. nil when nothing is marked.
func bulk[T any](m *tableMarks, items []T, key func(T) string, act func(T) Action) []Action {
	ms := marked(m, items, key)
	if len(ms) == 0 {
		return nil
	}
	out := make([]Action, 0, len(ms))
	for _, it := range ms {
		out = append(out, act(it))
	}
	return out
}

// MarkCount is the number of marked rows.
func (m *tableMarks) MarkCount() int {
	if m.marks == nil {
		return 0
	}
	return m.marks.Len()
}
