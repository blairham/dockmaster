package views

import "charm.land/bubbles/v2/table"

// setTableRows replaces a table's rows and repairs the cursor.
//
// bubbles' Model.SetRows clamps the cursor down when the row set shrinks,
// and an empty row set clamps it to -1 (cursor = len(rows)-1). The clamp is
// one-directional: when rows come back, the guard `cursor > len(rows)-1` is
// false for -1, so the cursor is never restored. SelectedRow() returns nil
// for a negative cursor, which silently kills every row action for the rest
// of the session.
//
// Any filter that matches nothing reaches that state, and so does an
// ordinary `docker ps` on a host where the last container just stopped —
// so without this the view wedges after a perfectly normal stop.
func setTableRows(t *table.Model, rows []table.Row) {
	t.SetRows(rows)
	if len(rows) > 0 && t.Cursor() < 0 {
		t.SetCursor(0)
	}
}

// Tabler is a view drawn as a table. The keys every table shares — ctrl+s
// to save it — work on any view that implements it.
type Tabler interface {
	Table() *table.Model
}
