// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/table"

	"github.com/blairham/dockmaster/internal/docker"
)

// TestParseExprColumn: what each spelling of an expression column comes to.
func TestParseExprColumn(t *testing.T) {
	for _, tc := range []struct {
		view, spec string
		want       ExprColumn
	}{
		{
			view: "containers", spec: `SVC:.Labels.com\.docker\.compose\.service`,
			want: ExprColumn{Title: "SVC", Field: "Labels", Label: "com.docker.compose.service"},
		},
		{
			view: "containers", spec: ` Svc  :  .Labels.plain `,
			want: ExprColumn{Title: "Svc", Field: "Labels", Label: "plain"},
		},
		{
			view: "containers", spec: `CMD:.Command|R`,
			want: ExprColumn{Title: "CMD", Field: "Command", Right: true},
		},
		{
			view: "containers", spec: `BORN:.Created|TWR`,
			want: ExprColumn{Title: "BORN", Field: "Created", Age: true, Wide: true, Right: true},
		},
		{
			view: "containers", spec: `RW:.SizeRw|N`,
			want: ExprColumn{Title: "RW", Field: "SizeRw", Numeric: true},
		},
		{
			view: "images", spec: `V:.Labels.org\.opencontainers\.image\.version|NL`,
			want: ExprColumn{Title: "V", Field: "Labels", Label: "org.opencontainers.image.version", Numeric: true},
		},
		{
			view: "containers", spec: `IMG : .Image | R `,
			want: ExprColumn{Title: "IMG", Field: "Image", Right: true},
		},
		{
			view: "volumes", spec: `PATH:.Mountpoint|`,
			want: ExprColumn{Title: "PATH", Field: "Mountpoint"},
		},
		{
			view: "networks", spec: `K:.Labels.a\|b\\c`,
			want: ExprColumn{Title: "K", Field: "Labels", Label: `a|b\c`},
		},
		{
			view: "networks", spec: `V6:.IPv6`,
			want: ExprColumn{Title: "V6", Field: "IPv6"},
		},
	} {
		got, err := ParseExprColumn(tc.view, tc.spec)
		if err != nil || got != tc.want {
			t.Errorf("%s %q: %+v %v, want %+v", tc.view, tc.spec, got, err, tc.want)
		}
	}
}

// TestParseExprColumnRefuses: every mistake is an error saying what is
// wrong, and an attribute dockmaster does not implement is never ignored.
func TestParseExprColumnRefuses(t *testing.T) {
	for _, tc := range []struct {
		view, spec, want string
	}{
		{view: "containers", spec: "SVC", want: "not an expression column"},
		{view: "containers", spec: ":.Image", want: "no title"},
		{view: "containers", spec: "A|B:.Image", want: "holds no |"},
		{view: "containers", spec: "A↑:.Image", want: "no sort arrow"},
		{view: "containers", spec: "A\x1b:.Image", want: "control character"},
		{view: "projects", spec: "X:.Labels.a", want: "the projects view takes no expression columns (only containers, images"},
		{view: "containers", spec: "X:Image", want: "starts with a dot"},
		{view: "containers", spec: "X:", want: "starts with a dot"},
		{view: "containers", spec: "X:.", want: "an empty name"},
		{view: "containers", spec: "X:.Labels..a", want: "an empty name"},
		{view: "containers", spec: "X:.Image.", want: "an empty name"},
		{view: "containers", spec: "X:.Labels. |R", want: "an empty name"},
		{view: "containers", spec: `X:.Labels.a\x`, want: `escapes only`},
		{view: "containers", spec: `X:.Labels.a\`, want: `escapes only`},
		{view: "containers", spec: "X:.Labels", want: "name a label"},
		{view: "containers", spec: "X:.Labels.com.docker.compose.service", want: "escape the dots"},
		{view: "containers", spec: "X:.metadata.labels.a", want: `containers rows have no field "metadata" (fields are Command,`},
		{view: "images", spec: "X:.Name", want: `images rows have no field "Name"`},
		{view: "containers", spec: "X:.image", want: `no field "image"`},
		{view: "containers", spec: "X:.Image.Tag", want: ".Image has no fields under it"},
		{view: "containers", spec: "X:.Image|H", want: "attribute H is not supported"},
		{view: "containers", spec: "X:.Image|S", want: "attribute S is not supported"},
		{view: "containers", spec: "X:.Image|r", want: `attribute 'r' is not supported (the attributes are R, L, W, T, N)`},
		{view: "containers", spec: "X:.Image|RL", want: "aligned one way"},
		{view: "images", spec: "X:.Tag|W", want: "the images view has no wide mode"},
		{view: "containers", spec: "X:.Image|T", want: "attribute T: .Image is not a time"},
		{view: "containers", spec: `X:.Labels.a\.b|T`, want: `attribute T: .Labels.a\.b is not a time`},
		{view: "containers", spec: "X:.Image|N", want: "attribute N: .Image is not a number or a label"},
		{view: "containers", spec: "X:.Created|N", want: "attribute N: .Created"},
		{view: "networks", spec: "X:.Internal|N", want: "attribute N: .Internal"},
	} {
		if _, err := ParseExprColumn(tc.view, tc.spec); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %q: %v, want %q", tc.view, tc.spec, err, tc.want)
		}
	}
}

// TestExprFieldsReadTheirOwnField: every field reads what it is named for
// — a table this size invites a copy-paste slip.
func TestExprFieldsReadTheirOwnField(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	rows := map[string]any{
		"containers": docker.Container{
			ID: "ID", Name: "Name", Image: "Image", ImageID: "ImageID", Command: "Command", State: "State",
			Status: "Status", Health: "Health", Ports: "Ports", Network: "Network", IP: "IP",
			Project: "Project", Service: "Service", Created: created, SizeRw: 7,
		},
		"images": docker.Image{
			ID: "ID", Repo: "Repo", Tag: "Tag", Digest: "Digest", Created: created, Size: 7, Containers: 8,
			Dangling: true,
		},
		"volumes": docker.Volume{
			Name: "Name", Driver: "Driver", Scope: "Scope", Mountpoint: "Mountpoint", Project: "Project",
			Created: created, Size: 7, Refs: 8,
		},
		"networks": docker.Network{
			ID: "ID", Name: "Name", Driver: "Driver", Scope: "Scope", Subnet: "Subnet", Gateway: "Gateway",
			Project: "Project", Created: created, Containers: 8, Internal: true, Attachable: false, IPv6: true,
		},
	}
	special := map[string]string{
		"Created": "2026-01-02 03:04:05", "SizeRw": "7", "Size": "7", "Containers": "8", "Refs": "8",
		"Dangling": "true", "Internal": "true", "Attachable": "false", "IPv6": "true",
	}
	if got := ExprViews(); !slices.Equal(got, []string{"containers", "images", "networks", "volumes"}) {
		t.Errorf("ExprViews %q", got)
	}
	for view, row := range rows {
		names, _ := ExprFields(view)
		if names[0] != labelsField && !slices.Contains(names, labelsField) {
			t.Errorf("%s: no Labels in %q", view, names)
		}
		for _, name := range names {
			if name == labelsField {
				continue
			}
			want, ok := special[name]
			if !ok {
				want = name
			}
			if got := (ExprColumn{Field: name}).cell(view, row); got != want {
				t.Errorf("%s .%s = %q, want %q", view, name, got, want)
			}
		}
	}
	if _, ok := ExprFields("projects"); ok {
		t.Error("projects takes no expression columns")
	}
}

// TestExprCells: a label present, absent and with dots in its key; ages,
// unknown counts, and a row of another type.
func TestExprCells(t *testing.T) {
	labels := map[string]string{
		"com.docker.compose.service": "web", "plain": "p", "evil": "a\x1b[2Jb\nc\td\u009b",
	}
	created := time.Now().Add(-2 * time.Hour)
	rows := map[string]any{
		"containers": docker.Container{Labels: labels, Created: created, SizeRw: -1},
		"images":     docker.Image{Labels: labels, Created: created, Containers: -1},
		"volumes":    docker.Volume{Labels: labels, Created: created, Refs: -1},
		"networks":   docker.Network{Labels: labels, Created: created, Containers: -1},
	}
	unknown := map[string]string{
		"containers": "SizeRw",
		"images":     "Containers",
		"volumes":    "Refs",
		"networks":   "Containers",
	}
	for view, row := range rows {
		for spec, want := range map[string]string{
			`S:.Labels.com\.docker\.compose\.service`: "web",
			`S:.Labels.plain`:                         "p",
			`S:.Labels.absent`:                        "",
			`S:.Labels.com`:                           "",
			`S:.Labels.evil`:                          "a[2Jb c d",
			`S:.Created|T`:                            "2h",
			"S:." + unknown[view]:                     "",
		} {
			e, err := ParseExprColumn(view, spec)
			if err != nil {
				t.Fatalf("%s %q: %v", view, spec, err)
			}
			if got := e.cell(view, row); got != want {
				t.Errorf("%s %q = %q, want %q", view, spec, got, want)
			}
		}
		e, _ := ParseExprColumn(view, `S:.Labels.plain`)
		if got := e.cell(view, struct{}{}); got != "" {
			t.Errorf("%s: a row of another type reads %q", view, got)
		}
		if got := e.cell(view, nil); got != "" {
			t.Errorf("%s: no row reads %q", view, got)
		}
		if got := (ExprColumn{Field: "Created"}).cell(view, rows["nothing"]); got != "" {
			t.Errorf("%s: a nil row's time reads %q", view, got)
		}
	}
	if got := (ExprColumn{Field: "Created"}).cell("volumes", docker.Volume{}); got != "" {
		t.Errorf("a zero time reads %q", got)
	}
	if got := (ExprColumn{Field: "Created", Age: true}).cell("volumes", docker.Volume{}); got != "" {
		t.Errorf("a zero time as an age reads %q", got)
	}
	if got := (ExprColumn{Field: "Name"}).cell("projects", docker.Volume{Name: "x"}); got != "" {
		t.Errorf("a view with no expression fields reads %q", got)
	}
	if got := (ExprColumn{Field: "Nope"}).cell("volumes", docker.Volume{Name: "x"}); got != "" {
		t.Errorf("an unknown field reads %q", got)
	}
}

// TestExprWidths: a column is wide enough for its title, and sized for its
// kind of value.
func TestExprWidths(t *testing.T) {
	for _, tc := range []struct {
		e    ExprColumn
		want int
	}{
		{e: ExprColumn{Title: "S", Field: "Labels", Label: "a"}, want: 20},
		{e: ExprColumn{Title: strings.Repeat("W", 25), Field: "Labels", Label: "a"}, want: 25},
		{e: ExprColumn{Title: "B", Field: "Created", Age: true}, want: 6},
		{e: ExprColumn{Title: "B", Field: "Created"}, want: len(exprTimeLayout)},
		{e: ExprColumn{Title: "B", Field: "SizeRw"}, want: 8},
		{e: ExprColumn{Title: "B", Field: "Image"}, want: 20},
	} {
		if got := tc.e.column("containers"); got.Width != tc.want || got.Title != tc.e.Title {
			t.Errorf("%+v: %+v, want width %d", tc.e, got, tc.want)
		}
	}
	if got := (ExprColumn{Title: "B", Field: "Dangling"}).width("images"); got != 8 {
		t.Errorf("bool width %d", got)
	}
}

// TestNumericCompare: N sorts numbers by value — 1.25 before 1.5, which
// tuikit's natural order puts the other way round — and the rest after.
func TestNumericCompare(t *testing.T) {
	in := []string{"b", "", "10", "1.5", "a", "-3", "1.25", " 2 "}
	slices.SortStableFunc(in, numericCompare)
	if want := []string{"-3", "1.25", "1.5", " 2 ", "10", "", "a", "b"}; !slices.Equal(in, want) {
		t.Errorf("N order %q, want %q", in, want)
	}
}

// TestExprColumnsProjectAndSort: through the choke points, an expression
// column's cells come from each row's item, land under its title, sort,
// and carry the items with them; a W column is shown only with the wide
// set; R pads to the column's width; N sorts its own column only.
func TestExprColumnsProjectAndSort(t *testing.T) {
	t.Cleanup(func() { SetColumnLayouts(nil) })
	svc, _ := ParseExprColumn("containers", `SVC:.Labels.svc|R`)
	rep, _ := ParseExprColumn("containers", `REP:.Labels.rep|NW`)
	SetColumnLayouts(map[string]ColumnLayout{"containers": {
		Columns:    []string{"NAME", "SVC", "IP", "REP"},
		Exprs:      []ExprColumn{svc, rep},
		SortColumn: "SVC",
	}})
	items := []docker.Container{
		{ID: "1", Name: "one", IP: "10.0.0.1", Labels: map[string]string{"svc": "b", "rep": "1.5"}},
		{ID: "2", Name: "two", IP: "10.0.0.2", Labels: map[string]string{"svc": "c", "rep": "1.25"}},
		{ID: "3", Name: "three", IP: "10.0.0.3", Labels: map[string]string{"svc": "a"}},
	}
	build := func(wide bool) []table.Row {
		var rows []table.Row
		for _, c := range items {
			if wide {
				rows = append(rows, table.Row{c.Name, "id", "img", "cmd", "st", "h", "c", "m", "p", "n", c.IP, "age"})
			} else {
				rows = append(rows, table.Row{c.Name, "img", "st", "h", "c", "m", "p", "age"})
			}
		}
		return rows
	}

	s := tableSort{layoutKey: "containers"}
	cols := s.layout(containerColumns(false), 120)
	if got := columnTitles(cols); !slices.Equal(got, []string{"NAME", "SVC↑"}) {
		t.Fatalf("narrow columns %q", got)
	}
	rows := build(false)
	sortRows(&s, rows, items)
	if got := []string{rows[0][0], rows[1][0], rows[2][0]}; !slices.Equal(got, []string{"three", "one", "two"}) {
		t.Errorf("not sorted by SVC: %q", got)
	}
	if got := []string{items[0].ID, items[1].ID, items[2].ID}; !slices.Equal(got, []string{"3", "1", "2"}) {
		t.Errorf("items not carried with their rows: %q", got)
	}
	if w := cols[1].Width; rows[0][1] != strings.Repeat(" ", w-1)+"a" {
		t.Errorf("SVC not right-aligned to %d: %q", w, rows[0][1])
	}

	// Wide: the wide set's IP and the W column join, in the layout's order,
	// under the right titles.
	items = slices.Clone(items)
	wcols := s.layout(containerColumns(true), 200)
	if got := columnTitles(wcols); !slices.Equal(got, []string{"NAME", "SVC↑", "IP", "REP"}) {
		t.Fatalf("wide columns %q", got)
	}
	rows = build(true)
	sortRows(&s, rows, items)
	for i, r := range rows {
		if strings.TrimSpace(r[1]) != items[i].Labels["svc"] || r[2] != items[i].IP || r[3] != items[i].Labels["rep"] {
			t.Errorf("row %d %q does not match its item %+v", i, r, items[i])
		}
	}

	// N: by value, the missing label last — and only on its own column.
	s.sorter = nil
	s.layoutSort = false
	s.ensureSorter()
	s.sorter.HandleKey("shift+right", 4)
	s.sorter.HandleKey("shift+right", 4)
	if s.sorter.Column() != 3 {
		t.Fatalf("setup: sorting column %d", s.sorter.Column())
	}
	rows = build(true)
	sortRows(&s, rows, items)
	if got := []string{rows[0][3], rows[1][3], rows[2][3]}; !slices.Equal(got, []string{"1.25", "1.5", ""}) {
		t.Errorf("REP not sorted as numbers: %q", got)
	}
	if s.compare(0, "1.5", "1.25") != cellCompare(0, "1.5", "1.25") {
		t.Error("N reached a column it was not given on")
	}
}

// TestFillWidthPassesOverRightAligned: the room left over goes to the last
// column that is not right-aligned, so R cells stay against their edge;
// with nothing else, the first takes it.
func TestFillWidthPassesOverRightAligned(t *testing.T) {
	fixed := func(title string) bool { return title == "R" }
	cols := fillWidth([]table.Column{{Title: "A", Width: 4}, {Title: "R", Width: 4}}, 20, fixed)
	if cols[0].Width != 12 || cols[1].Width != 4 {
		t.Errorf("widths %d %d, want 12 4", cols[0].Width, cols[1].Width)
	}
	cols = fillWidth([]table.Column{{Title: "R", Width: 4}}, 20, fixed)
	if cols[0].Width != 18 {
		t.Errorf("a lone R column: width %d, want 18", cols[0].Width)
	}
}

// TestWithExprCells: expression cells go after the column set's cells,
// padding a short row, and before any cell past them.
func TestWithExprCells(t *testing.T) {
	got := withExprCells(table.Row{"a", "b", "extra"}, 2, []string{"x"})
	if !slices.Equal(got, table.Row{"a", "b", "x", "extra"}) {
		t.Errorf("got %q", got)
	}
	if got := withExprCells(table.Row{"a"}, 3, []string{"x"}); !slices.Equal(got, table.Row{"a", "", "", "x"}) {
		t.Errorf("short row: %q", got)
	}
}

// FuzzParseExprColumn: no spec panics the parser, and one it accepts reads
// a field the view has, under a usable title, and parses back from its own
// path to the same column.
func FuzzParseExprColumn(f *testing.F) {
	for _, s := range []string{
		`SVC:.Labels.com\.docker\.compose\.service`, `A:.Image|R`, `B:.Created|TW`, `C:.Labels.a\|b|N`,
		`D:.Labels.\\`, `:.`, `X:.Labels.a\`, `X:..`, `X:.Image|RL`, `é:.Labels.ü\.ß|L`, "X:.Labels.\x00",
	} {
		f.Add("containers", s)
		f.Add("images", s)
	}
	f.Fuzz(func(t *testing.T, view, spec string) {
		e, err := ParseExprColumn(view, spec)
		if err != nil {
			return
		}
		if e.Title == "" || checkExprTitle(e.Title) != nil {
			t.Fatalf("%q: accepted title %q", spec, e.Title)
		}
		names, _ := ExprFields(view)
		if !slices.Contains(names, e.Field) || (e.Field == labelsField) != (e.Label != "") {
			t.Fatalf("%q: field %q label %q", spec, e.Field, e.Label)
		}
		again, err := ParseExprColumn(view, e.Title+":"+e.path())
		if err != nil || again.Field != e.Field || again.Label != e.Label {
			t.Fatalf("%q: its own path %q reparses as %+v, %v; was %+v", spec, e.path(), again, err, e)
		}
	})
}
