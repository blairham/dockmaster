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
// containers view's running-first, say — until asked for another.
type tableSort struct {
	sorter *tktable.Sorter
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
	first := s.sorter == nil
	if first {
		s.sorter = tktable.NewSorter(0, false)
		s.sorter.SetCompare(cellCompare)
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
	if s.sorter == nil {
		return out
	}
	return s.sorter.Columns(out)
}

// sortRows sorts a view's rows by the active column, and items — the
// values the rows were made from, which Selected indexes by cursor — into
// the same order. nil items sorts the rows alone.
func sortRows[T any](s *tableSort, rows []table.Row, items []T) {
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
