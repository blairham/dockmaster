// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/table"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// layoutApp is a test app with views.yaml's layouts already validated, and
// the sample containers loaded.
func layoutApp(t *testing.T, entries map[string]config.ViewColumns) (*App, *views.ContainersView) {
	t.Helper()
	l, err := ColumnLayouts(entries)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { views.SetColumnLayouts(nil) })
	a := optsApp(t, Options{ColumnLayouts: l})
	loadContainers(a)
	return a, typedView[*views.ContainersView](a, style.ViewContainers)
}

// titles is a table's column titles, as drawn.
func titles(t *table.Model) []string {
	out := make([]string, 0, len(t.Columns()))
	for _, c := range t.Columns() {
		out = append(out, c.Title)
	}
	return out
}

// cells is a table's rows with styling stripped and padding trimmed.
func cells(t *table.Model) [][]string {
	out := make([][]string, 0, len(t.Rows()))
	for _, r := range t.Rows() {
		var row []string
		for _, c := range r {
			row = append(row, strings.TrimSpace(xansi.Strip(c)))
		}
		out = append(out, row)
	}
	return out
}

// column is the named column's cells, top to bottom.
func column(t *table.Model, title string) []string {
	i := slices.IndexFunc(t.Columns(), func(c table.Column) bool {
		return strings.TrimRight(c.Title, "↑↓") == title
	})
	if i < 0 {
		return nil
	}
	var out []string
	for _, r := range cells(t) {
		out = append(out, r[i])
	}
	return out
}

// TestColumnLayoutPicksAndOrders: views.yaml chooses a view's columns and
// their order, by title, and every row's cells follow its columns.
func TestColumnLayoutPicksAndOrders(t *testing.T) {
	a, v := layoutApp(t, map[string]config.ViewColumns{
		"containers": {Columns: []string{"NAME", "STATE", "image", "AGE"}},
		"images":     {Columns: []string{"TAG", "REPOSITORY", "SIZE"}},
	})
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME", "STATE", "IMAGE", "AGE"}) {
		t.Fatalf("containers columns %q", got)
	}
	if got := cells(v.Table())[0][:3]; !slices.Equal(got, []string{"web", "running", "nginx:1.27"}) {
		t.Errorf("containers first row %q", got)
	}
	if out := render(a); strings.Contains(out, "HEALTH") || strings.Contains(out, "PORTS") {
		t.Errorf("a column left out of the layout is still drawn:\n%s", out)
	}
	assertFrameFits(t, a, "containers with four columns")

	step(a, key("1"))
	step(a, views.ImagesRefreshMsg{Images: []docker.Image{
		{ID: "sha256:1111111111111111", Repo: "nginx", Tag: "1.27", Size: 2_000_000, Created: time.Now()},
	}})
	iv := typedView[*views.ImagesView](a, style.ViewImages)
	if got := titles(iv.Table()); !slices.Equal(got, []string{"TAG", "REPOSITORY", "SIZE"}) {
		t.Fatalf("images columns %q", got)
	}
	if got := cells(iv.Table())[0]; !slices.Equal(got, []string{"1.27", "nginx", "2.00MB"}) {
		t.Errorf("images row %q", got)
	}
}

// TestColumnLayoutFillsTheWidth: a layout with no free-text column to grow
// still spans the screen.
func TestColumnLayoutFillsTheWidth(t *testing.T) {
	a, v := layoutApp(t, map[string]config.ViewColumns{"containers": {Columns: []string{"STATE", "AGE"}}})
	total := 0
	for _, c := range v.Table().Columns() {
		total += c.Width + 2
	}
	if w := a.innerWidth(); total != w {
		t.Errorf("columns span %d of %d", total, w)
	}
	assertFrameFits(t, a, "two fixed columns")
}

// TestColumnLayoutSortColumn: a sortColumn opens the view sorted, its
// header marked, and the selection is the row drawn under the cursor.
func TestColumnLayoutSortColumn(t *testing.T) {
	_, v := layoutApp(t, map[string]config.ViewColumns{
		"containers": {Columns: []string{"STATE", "NAME"}, SortColumn: "NAME:desc"},
	})
	if got := titles(v.Table()); !slices.Equal(got, []string{"STATE", "NAME↓"}) {
		t.Errorf("columns %q", got)
	}
	if got := column(v.Table(), "NAME"); !slices.Equal(got, []string{"web", "standalone", "migrate", "api"}) {
		t.Errorf("not opened sorted by NAME descending: %q", got)
	}
	if c, _ := v.Selected(); c.Name != "web" {
		t.Errorf("selected %q, want the top row, web", c.Name)
	}
}

// TestColumnLayoutSortKeysCountShownColumns: shift+←/→ sort by the column
// the user sees, not the one at that place before the layout moved it.
func TestColumnLayoutSortKeysCountShownColumns(t *testing.T) {
	a, v := layoutApp(t, map[string]config.ViewColumns{"containers": {Columns: []string{"STATE", "NAME"}}})
	step(a, key("shift+right"))
	if got := titles(v.Table()); !slices.Equal(got, []string{"STATE↑", "NAME"}) {
		t.Errorf("first sort key marks %q", got)
	}
	if got := column(v.Table(), "STATE"); !slices.Equal(got, []string{"exited", "paused", "running", "running"}) {
		t.Errorf("not sorted by STATE: %q", got)
	}
	step(a, key("shift+right"))
	if got := column(v.Table(), "NAME"); !slices.Equal(got, []string{"api", "migrate", "standalone", "web"}) {
		t.Errorf("not sorted by NAME: %q", got)
	}
}

// TestColumnLayoutActsOnTheRowShown: after projection and sort, a key on
// the selected row acts on the container drawn there — not the one that
// was at that index before the sort.
func TestColumnLayoutActsOnTheRowShown(t *testing.T) {
	a, v := layoutApp(t, map[string]config.ViewColumns{
		"containers": {Columns: []string{"STATE", "NAME"}, SortColumn: "NAME:desc"},
	})
	step(a, key("down")) // standalone, drawn second; api is second in the listing
	if got := cells(v.Table())[v.Table().Cursor()][1]; got != "standalone" {
		t.Fatalf("setup: cursor on %q", got)
	}
	if c, _ := v.Selected(); c.ID != "dddddddddddd4444" {
		t.Errorf("Selected is %s %q, want standalone", c.ID, c.Name)
	}
	step(a, key("ctrl+d"))
	if p := a.confirm.Prompt(); !strings.Contains(p, "standalone") || strings.Contains(p, "api") {
		t.Errorf("remove asks about %q, want standalone", p)
	}
}

// TestColumnLayoutWideMode: a layout may name the wide columns; they
// appear in their place with ctrl+w and are skipped without it, and a
// sortColumn among them sorts only while they are shown.
func TestColumnLayoutWideMode(t *testing.T) {
	cs := sampleContainers()
	cs[0].IP, cs[1].IP = "172.18.0.9", "172.18.0.2"
	a, v := layoutApp(t, map[string]config.ViewColumns{
		"containers": {Columns: []string{"NAME", "IP", "STATE"}, SortColumn: "IP"},
	})
	step(a, views.ContainersRefreshMsg{Containers: cs})
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME", "STATE"}) {
		t.Errorf("narrow columns %q", got)
	}
	if got := column(v.Table(), "NAME"); got[0] != "web" {
		t.Errorf("sorted by a column not shown: %q", got)
	}
	step(a, key("ctrl+w"))
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME", "IP↑", "STATE"}) {
		t.Errorf("wide columns %q", got)
	}
	if got := column(v.Table(), "IP"); !slices.Equal(got, []string{"", "", "172.18.0.2", "172.18.0.9"}) {
		t.Errorf("IP cells %q", got)
	}
	if c, _ := v.Selected(); c.Name != "migrate" {
		t.Errorf("selected %q after the wide sort, want migrate", c.Name)
	}
	step(a, key("ctrl+w"))
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME", "STATE"}) {
		t.Errorf("narrow again %q", got)
	}
}

// TestColumnLayoutsValidate: every way views.yaml can be wrong is refused
// with the names that would have been right.
func TestColumnLayoutsValidate(t *testing.T) {
	for _, tc := range []struct {
		entries map[string]config.ViewColumns
		want    string
	}{
		{
			entries: map[string]config.ViewColumns{"pdos": {Columns: []string{"NAME"}}},
			want: `views.yaml: unknown view "pdos" (views are aliases, containers, contexts, dir, diskusage, ` +
				`dumps, files, images, layers, lint, networks, node, pods, portforwards, projects, runtimes, scan, volumes)`,
		},
		{
			entries: map[string]config.ViewColumns{"images": {Columns: []string{"NAME"}}},
			want: `views.yaml: images: unknown column "NAME" ` +
				`(columns are REPOSITORY, TAG, IMAGE ID, VULN, SIZE, USED BY, AGE)`,
		},
		{
			entries: map[string]config.ViewColumns{"images": {Columns: []string{"TAG", "tag"}}},
			want:    `images: column "tag" is listed twice`,
		},
		{
			entries: map[string]config.ViewColumns{"images": {SortColumn: "AGE:sideways"}},
			want:    `images: sortColumn "AGE:sideways": the direction is asc or desc`,
		},
		{
			entries: map[string]config.ViewColumns{"images": {SortColumn: ":desc"}},
			want:    `images: sortColumn ":desc": no column`,
		},
		{
			entries: map[string]config.ViewColumns{"images": {SortColumn: "AGE:"}},
			want:    `images: sortColumn "AGE:": the direction is asc or desc`,
		},
		{
			entries: map[string]config.ViewColumns{"images": {SortColumn: "CREATED:desc"}},
			want:    `images: sortColumn "CREATED:desc": unknown column "CREATED" (columns are REPOSITORY,`,
		},
		{
			entries: map[string]config.ViewColumns{"images": {Columns: []string{"TAG"}, SortColumn: "SIZE"}},
			want:    `images: sortColumn "SIZE": SIZE is not among the columns shown (TAG)`,
		},
	} {
		if _, err := ColumnLayouts(tc.entries); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: got %v, want %q", tc.entries, err, tc.want)
		}
	}

	l, err := ColumnLayouts(map[string]config.ViewColumns{
		"containers": {Columns: []string{"ip", "Name", "cpu%"}, SortColumn: "cpu%:DESC"},
		"images":     {SortColumn: "IMAGE ID"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c := l["containers"]; !slices.Equal(c.Columns, []string{"IP", "NAME", "CPU%"}) || c.SortColumn != "CPU%" ||
		!c.SortDesc {
		t.Errorf("containers %+v", c)
	}
	if i := l["images"]; i.Columns != nil || i.SortColumn != "IMAGE ID" || i.SortDesc {
		t.Errorf("images %+v", i)
	}
}

// TestNoViewsFileChangesNothing: no views.yaml, or an empty one, is no
// layout, and the views draw their own columns.
func TestNoViewsFileChangesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvDir, dir)
	for _, body := range []string{"", "views:\n", "views: {}\n"} {
		if body != "" {
			write(t, filepath.Join(dir, config.ViewsFileName), body)
		}
		entries, err := config.LoadViews()
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		l, err := ColumnLayouts(entries)
		if err != nil || l != nil {
			t.Fatalf("%q: %v %v", body, l, err)
		}
		_, v := layoutApp(t, entries)
		want := []string{"NAME", "IMAGE", "STATE", "HEALTH", "CPU%", "MEM", "PORTS", "AGE"}
		if got := titles(v.Table()); !slices.Equal(got, want) {
			t.Errorf("%q: columns %q", body, got)
		}
		if got := column(v.Table(), "NAME"); !slices.Equal(got, []string{"web", "api", "migrate", "standalone"}) {
			t.Errorf("%q: rows reordered %q", body, got)
		}
	}
}

// TestReloadAppliesColumnLayouts: a saved views.yaml takes effect without
// a restart (ui.reactive) — on the view shown and on one waiting under it.
func TestReloadAppliesColumnLayouts(t *testing.T) {
	next, err := ColumnLayouts(map[string]config.ViewColumns{
		"containers": {Columns: []string{"AGE", "NAME"}, SortColumn: "NAME:desc"},
		"images":     {Columns: []string{"SIZE", "REPOSITORY"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { views.SetColumnLayouts(nil) })
	a, dir := reactiveApp(t, func() (Reloaded, error) {
		return Reloaded{Options: Options{ColumnLayouts: next}}, nil
	})
	step(a, key("1"))
	step(a, views.ImagesRefreshMsg{Images: []docker.Image{
		{ID: "sha256:1111111111111111", Repo: "nginx", Tag: "1.27", Size: 2_000_000, Created: time.Now()},
	}})
	step(a, key("0"))
	loadContainers(a)
	v := typedView[*views.ContainersView](a, style.ViewContainers)
	if got := titles(v.Table()); len(got) != 8 {
		t.Fatalf("setup: columns %q", got)
	}

	write(t, filepath.Join(dir, config.ViewsFileName), "views: {}\n")
	reloadNow(t, a)
	if got := titles(v.Table()); !slices.Equal(got, []string{"AGE", "NAME↓"}) {
		t.Errorf("containers after reload %q", got)
	}
	if got := column(v.Table(), "NAME"); !slices.Equal(got, []string{"web", "standalone", "migrate", "api"}) {
		t.Errorf("containers rows after reload %q", got)
	}
	if c, _ := v.Selected(); c.Name != "web" {
		t.Errorf("selected %q after reload", c.Name)
	}
	iv := typedView[*views.ImagesView](a, style.ViewImages)
	if got := cells(iv.Table()); len(got) != 1 || !slices.Equal(got[0], []string{"2.00MB", "nginx"}) {
		t.Errorf("images rows after reload %q (columns %q)", got, titles(iv.Table()))
	}
	assertFrameFits(t, a, "containers after reload")
}
