// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// svcColumn is the compose service label as an expression column.
const svcColumn = `SVC:.Labels.com\.docker\.compose\.service`

// labeledContainers are the sample containers with their compose labels,
// in an order that differs from the service order: web, api, migrate,
// standalone (no label).
func labeledContainers() []docker.Container {
	cs := sampleContainers()
	for i := range cs {
		if cs[i].Service != "" {
			cs[i].Labels = map[string]string{
				"com.docker.compose.service": cs[i].Service,
				"com.docker.compose.project": cs[i].Project,
			}
		}
		cs[i].Command = "cmd-" + cs[i].Name
	}
	return cs
}

// exprApp is layoutApp with the labeled containers loaded.
func exprApp(t *testing.T, entries map[string]config.ViewColumns) (*App, *views.ContainersView) {
	t.Helper()
	a, v := layoutApp(t, entries)
	step(a, views.ContainersRefreshMsg{Containers: labeledContainers()})
	return a, v
}

// selectedID is the full ID i copies from the selected row — what reaches
// an action on it.
func selectedID(t *testing.T, a *App) string {
	t.Helper()
	return clipboard(step(a, key("i")))
}

// TestExprColumnValidate: an expression column's mistakes are refused at
// load, naming the view and the column; a good one comes back parsed, and
// sortColumn may name it.
func TestExprColumnValidate(t *testing.T) {
	for _, tc := range []struct {
		entries map[string]config.ViewColumns
		want    string
	}{
		{
			entries: map[string]config.ViewColumns{"containers": {Columns: []string{"NAME", "SVC:.Labels.a.b"}}},
			want:    `views.yaml: containers: column "SVC:.Labels.a.b": path ".Labels.a.b": labels are one level deep`,
		},
		{
			entries: map[string]config.ViewColumns{"images": {Columns: []string{"X:.Name"}}},
			want:    `views.yaml: images: column "X:.Name": path ".Name": images rows have no field "Name" (fields are`,
		},
		{
			entries: map[string]config.ViewColumns{"containers": {Columns: []string{"name:.Image"}}},
			want:    `containers: column "name:.Image": title "name" is the containers view's own column NAME`,
		},
		{
			entries: map[string]config.ViewColumns{"containers": {Columns: []string{"ip:.Image"}}},
			want:    `title "ip" is the containers view's own column IP`,
		},
		{
			entries: map[string]config.ViewColumns{"containers": {Columns: []string{"S:.Image", "s:.Command"}}},
			want:    `containers: column "s:.Command": title "s" is listed twice`,
		},
		{
			entries: map[string]config.ViewColumns{"containers": {Columns: []string{"NAME|R"}}},
			want:    `containers: column "NAME|R": attributes go only on an expression column`,
		},
		{
			entries: map[string]config.ViewColumns{"projects": {Columns: []string{"X:.Labels.a"}}},
			want:    `projects: column "X:.Labels.a": the projects view takes no expression columns`,
		},
		{
			entries: map[string]config.ViewColumns{"containers": {Columns: []string{"X:.Image|H"}}},
			want:    `containers: column "X:.Image|H": attribute H is not supported`,
		},
		{
			entries: map[string]config.ViewColumns{"containers": {Columns: []string{"X:.Image"}, SortColumn: "Y"}},
			want:    `sortColumn "Y": unknown column "Y" (columns are NAME, IMAGE, STATE, HEALTH, CPU%, MEM, PORTS, AGE, ID, COMMAND, NETWORKS, IP, X)`,
		},
	} {
		if _, err := ColumnLayouts(tc.entries); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v:\n got %v\nwant %q", tc.entries, err, tc.want)
		}
	}

	l, err := ColumnLayouts(map[string]config.ViewColumns{
		"containers": {Columns: []string{"NAME", svcColumn + "|R"}, SortColumn: "svc:desc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := l["containers"]
	want := views.ExprColumn{Title: "SVC", Field: "Labels", Label: "com.docker.compose.service", Right: true}
	if !slices.Equal(c.Columns, []string{"NAME", "SVC"}) || len(c.Exprs) != 1 || c.Exprs[0] != want ||
		c.SortColumn != "SVC" || !c.SortDesc {
		t.Errorf("containers %+v", c)
	}
}

// TestExprColumnRendersSortsAndActs: a label column renders under its
// title, opens sorted by it, follows the sort keys, and the row an action
// reaches is the one drawn under the cursor.
func TestExprColumnRendersSortsAndActs(t *testing.T) {
	a, v := exprApp(t, map[string]config.ViewColumns{
		"containers": {Columns: []string{"NAME", svcColumn, "STATE"}, SortColumn: "SVC"},
	})
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME", "SVC↑", "STATE"}) {
		t.Fatalf("columns %q", got)
	}
	if out := render(a); !strings.Contains(out, "SVC↑") {
		t.Errorf("SVC not drawn:\n%s", out)
	}
	if got := column(v.Table(), "SVC"); !slices.Equal(got, []string{"", "api", "migrate", "web"}) {
		t.Errorf("SVC cells %q", got)
	}
	if got := column(v.Table(), "NAME"); !slices.Equal(got, []string{"standalone", "api", "migrate", "web"}) {
		t.Errorf("rows did not move with their SVC cells: %q", got)
	}
	step(a, key("down"))
	step(a, key("down")) // migrate: third drawn, third listed too — so go one more
	step(a, key("down")) // web: drawn last, listed first
	if got := selectedID(t, a); got != "aaaaaaaaaaaa1111" {
		t.Errorf("i on the row drawn as web copied %q", got)
	}
	step(a, key("ctrl+d"))
	if p := a.confirm.Prompt(); !strings.Contains(p, "web") {
		t.Errorf("remove asks about %q, want web", p)
	}
	step(a, key("esc"))

	// shift+↓ reverses the expression column's sort.
	step(a, key("shift+down"))
	if got := column(v.Table(), "SVC"); !slices.Equal(got, []string{"web", "migrate", "api", ""}) {
		t.Errorf("SVC descending %q", got)
	}
	step(a, key("up"))
	step(a, key("up"))
	step(a, key("up"))
	if c, _ := v.Selected(); c.Name != "web" {
		t.Errorf("selected %q at the top of the reversed sort", c.Name)
	}
	assertFrameFits(t, a, "an expression column")
}

// TestExprColumnSortKeysReachIt: with no sortColumn, shift+←/→ count the
// expression column like any other.
func TestExprColumnSortKeysReachIt(t *testing.T) {
	a, v := exprApp(t, map[string]config.ViewColumns{
		"containers": {Columns: []string{"NAME", "CMD:.Command"}},
	})
	step(a, key("shift+right"))
	step(a, key("shift+right"))
	step(a, key("shift+down"))
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME", "CMD↓"}) {
		t.Fatalf("columns %q", got)
	}
	want := []string{"cmd-web", "cmd-standalone", "cmd-migrate", "cmd-api"}
	if got := column(v.Table(), "CMD"); !slices.Equal(got, want) {
		t.Errorf("CMD descending %q", got)
	}
	if got := column(v.Table(), "NAME"); !slices.Equal(got, []string{"web", "standalone", "migrate", "api"}) {
		t.Errorf("NAME did not follow %q", got)
	}
}

// TestExprColumnWideMode: wide mode inserts columns in the middle of the
// set, and an expression column keeps its own cells either side of it; a
// W column shows only in wide mode, and a sort on it falls back without it.
func TestExprColumnWideMode(t *testing.T) {
	a, v := exprApp(t, map[string]config.ViewColumns{
		"containers": {Columns: []string{"NAME", "IP", svcColumn, "CMD:.Command|W"}, SortColumn: "SVC:desc"},
	})
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME", "SVC↓"}) {
		t.Fatalf("narrow columns %q", got)
	}
	step(a, key("ctrl+w"))
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME", "IP", "SVC↓", "CMD"}) {
		t.Fatalf("wide columns %q", got)
	}
	for _, r := range cells(v.Table()) {
		if want := "cmd-" + r[0]; r[3] != want {
			t.Errorf("row %q: CMD %q, want %q", r, r[3], want)
		}
	}
	if got := column(v.Table(), "SVC"); !slices.Equal(got, []string{"web", "migrate", "api", ""}) {
		t.Errorf("SVC sort lost in wide mode %q", got)
	}
	step(a, key("down"))
	if got := selectedID(t, a); got != "cccccccccccc3333" {
		t.Errorf("wide: second row (migrate) copied %q", got)
	}

	// The user's sort on the W column falls back to the first column
	// when wide mode takes it away.
	step(a, key("shift+right"))
	step(a, key("shift+right"))
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME", "IP", "SVC", "CMD↑"}) {
		t.Fatalf("sorting by CMD: %q", got)
	}
	step(a, key("ctrl+w"))
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME↑", "SVC"}) {
		t.Errorf("narrow again %q", got)
	}
	for _, r := range cells(v.Table()) {
		if len(r) != 2 {
			t.Errorf("a row kept a hidden column's cell: %q", r)
		}
	}
	if got := column(v.Table(), "SVC"); !slices.Equal(got, []string{"api", "migrate", "", "web"}) {
		t.Errorf("SVC cells not with their rows (by NAME): %q", got)
	}
}

// TestExprColumnFilters: the filter keeps the rows it keeps, and each one's
// expression cells with it; a label filter matches the label the column
// shows.
func TestExprColumnFilters(t *testing.T) {
	a, v := exprApp(t, map[string]config.ViewColumns{
		"containers": {Columns: []string{"NAME", svcColumn}},
	})
	a.filter = "-l com.docker.compose.service=api"
	a.setActiveFilter(a.filter)
	if got := cells(v.Table()); len(got) != 1 || !slices.Equal(got[0], []string{"api", "api"}) {
		t.Errorf("label filter rows %q", got)
	}
	a.filter = "migrate|standalone"
	a.setActiveFilter(a.filter)
	if got := cells(v.Table()); len(got) != 2 || !slices.Equal(got[0], []string{"migrate", "migrate"}) ||
		!slices.Equal(got[1], []string{"standalone", ""}) {
		t.Errorf("regex filter rows %q", got)
	}
}

// TestExprColumnRightAlignAndAge: R pads cells to the column's edge, and T
// draws a time as an age.
func TestExprColumnRightAlignAndAge(t *testing.T) {
	_, v := exprApp(t, map[string]config.ViewColumns{
		"containers": {Columns: []string{"NAME", svcColumn + "|R", "BORN:.Created|T"}},
	})
	cols := v.Table().Columns()
	raw := v.Table().Rows()[0]
	if w := cols[1].Width; len(raw[1]) != w || !strings.HasSuffix(raw[1], " web") {
		t.Errorf("SVC cell %q is not right-aligned to %d", raw[1], w)
	}
	if got := column(v.Table(), "BORN"); !slices.Equal(got, []string{"3h", "3h", "3h", "3h"}) {
		t.Errorf("BORN %q", got)
	}
}

// TestExprColumnSave: ctrl+s saves the expression column with the rest.
func TestExprColumnSave(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	a, _ := exprApp(t, map[string]config.ViewColumns{
		"containers": {Columns: []string{"NAME", svcColumn}, SortColumn: "SVC"},
	})
	step(a, key("ctrl+s"))
	files, _ := filepath.Glob(filepath.Join(state, "dockmaster", "dumps", "containers-*.txt"))
	if len(files) != 1 {
		t.Fatalf("ctrl-s wrote %v (err %q)", files, a.errFlash)
	}
	b, _ := os.ReadFile(files[0]) //nolint:gosec // the test's own temp file
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 5 || !strings.Contains(lines[0], "SVC") || strings.Fields(lines[4])[1] != "web" {
		t.Errorf("saved:\n%s", b)
	}
}

// TestExprColumnsInEveryView: images, volumes and networks take label and
// field columns too.
func TestExprColumnsInEveryView(t *testing.T) {
	lbl := map[string]string{"org.example.team": "core"}
	a, _ := layoutApp(t, map[string]config.ViewColumns{
		"images":   {Columns: []string{"REPOSITORY", `TEAM:.Labels.org\.example\.team`, "N:.Containers"}},
		"volumes":  {Columns: []string{"NAME", `TEAM:.Labels.org\.example\.team`, "MP:.Mountpoint"}},
		"networks": {Columns: []string{"NAME", `TEAM:.Labels.org\.example\.team`, "SC:.Scope", "V6:.IPv6"}},
	})
	a.dispatchCommand("images")
	step(a, views.ImagesRefreshMsg{Images: []docker.Image{
		{ID: "sha256:aaaa", Repo: "nginx", Tag: "1", Labels: lbl, Containers: 2, Created: time.Now()},
		{ID: "sha256:bbbb", Repo: "redis", Tag: "7", Containers: -1, Created: time.Now()},
	}})
	iv := typedView[*views.ImagesView](a, style.ViewImages)
	if got := cells(iv.Table()); len(got) != 2 || !slices.Equal(got[0], []string{"nginx", "core", "2"}) ||
		!slices.Equal(got[1], []string{"redis", "", ""}) {
		t.Errorf("images %q", got)
	}
	a.dispatchCommand("volumes")
	step(a, views.VolumesRefreshMsg{Volumes: []docker.Volume{
		{Name: "data", Labels: lbl, Mountpoint: "/var/lib/docker/volumes/data/_data", Size: -1, Refs: 1},
	}})
	vv := typedView[*views.VolumesView](a, style.ViewVolumes)
	if got := cells(vv.Table()); len(got) != 1 ||
		!slices.Equal(got[0], []string{"data", "core", "/var/lib/docker/volumes/data/_data"}) {
		t.Errorf("volumes %q", got)
	}
	a.dispatchCommand("networks")
	step(a, views.NetworksRefreshMsg{Networks: []docker.Network{
		{ID: "n1", Name: "front", Scope: "local", IPv6: true, Labels: lbl},
	}})
	nv := typedView[*views.NetworksView](a, style.ViewNetworks)
	if got := cells(nv.Table()); len(got) != 1 || !slices.Equal(got[0], []string{"front", "core", "local", "true"}) {
		t.Errorf("networks %q", got)
	}
}

// TestReloadAppliesExprColumns: a reload that changes an expression column
// redraws the open view with the new one.
func TestReloadAppliesExprColumns(t *testing.T) {
	next, err := ColumnLayouts(map[string]config.ViewColumns{
		"containers": {Columns: []string{"NAME", `PROJ:.Labels.com\.docker\.compose\.project`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ColumnLayouts(map[string]config.ViewColumns{
		"containers": {Columns: []string{"NAME", svcColumn}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { views.SetColumnLayouts(nil) })
	a, dir := reactiveApp(t, func() (Reloaded, error) {
		return Reloaded{Options: Options{ColumnLayouts: next}}, nil
	})
	a.applyColumnLayouts(first)
	step(a, views.ContainersRefreshMsg{Containers: labeledContainers()})
	v := typedView[*views.ContainersView](a, style.ViewContainers)
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME", "SVC"}) {
		t.Fatalf("setup: columns %q", got)
	}
	write(t, filepath.Join(dir, config.ViewsFileName), "views: {}\n")
	reloadNow(t, a)
	if got := titles(v.Table()); !slices.Equal(got, []string{"NAME", "PROJ"}) {
		t.Errorf("columns after reload %q", got)
	}
	if got := column(v.Table(), "PROJ"); !slices.Equal(got, []string{"shop", "shop", "shop", ""}) {
		t.Errorf("PROJ after reload %q", got)
	}
}
