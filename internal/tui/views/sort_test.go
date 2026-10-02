package views

import (
	"testing"

	"charm.land/bubbles/v2/table"
)

func TestCellCompareReadsAgesAndSizes(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{a: "45m", b: "2h", want: -1},
		{a: "2d", b: "23h", want: 1},
		{a: "311MB", b: "1.2GB", want: -1},
		{a: "2GiB", b: "1.5GiB", want: 1},
		{a: "9", b: "10", want: -1},
		{a: "web", b: "API", want: 1},
	} {
		if got := cellCompare(0, tc.a, tc.b); got != tc.want {
			t.Errorf("cellCompare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSortRowsKeepsItemsWithTheirRows(t *testing.T) {
	var s tableSort
	rows := []table.Row{{"b"}, {"c"}, {"a"}}
	items := []string{"B", "C", "A"}
	sortRows(&s, rows, items)
	if rows[0][0] != "b" || items[0] != "B" {
		t.Fatal("sorted before a sort key was pressed")
	}
	tbl := table.New(table.WithColumns([]table.Column{{Title: "X", Width: 3}}))
	s.sortKey("shift+right", &tbl, func() { sortRows(&s, rows, items) })
	for i, want := range []string{"a", "b", "c"} {
		if rows[i][0] != want || items[i] != string(rune(want[0]-32)) || len(rows[i]) != 1 {
			t.Errorf("row %d: %q with item %q", i, rows[i], items[i])
		}
	}
}
