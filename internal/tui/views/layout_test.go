// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"maps"
	"slices"
	"testing"

	"charm.land/bubbles/v2/table"
)

// TestColumnTitles: a view's titles include every mode's columns, and
// leave out the untitled marker column, which is always shown.
func TestColumnTitles(t *testing.T) {
	got, ok := ColumnTitles("containers")
	want := []string{
		"NAME",
		"IMAGE",
		"VULN",
		"STATE",
		"HEALTH",
		"CPU%",
		"MEM",
		"PORTS",
		"AGE",
		"ID",
		"COMMAND",
		"NETWORKS",
		"IP",
	}
	if !ok || !slices.Equal(got, want) {
		t.Errorf("containers: %q %v", got, ok)
	}
	if got, _ := ColumnTitles("runtimes"); slices.Contains(got, "") || got[0] != "PROVIDER" {
		t.Errorf("runtimes: %q", got)
	}
	if _, ok := ColumnTitles("top"); ok {
		t.Error("top's columns are the process listing's own, and cannot be laid out")
	}
	for _, name := range LayoutViews() {
		if titles, ok := ColumnTitles(name); !ok || len(titles) == 0 {
			t.Errorf("%s has no titles", name)
		}
	}
}

// TestProjectionKeepsTheMarkerColumn: the untitled marker column leads a
// projected view, and a cell past the columns rides along at the end.
func TestProjectionKeepsTheMarkerColumn(t *testing.T) {
	t.Cleanup(func() { SetColumnLayouts(nil) })
	SetColumnLayouts(map[string]ColumnLayout{"runtimes": {Columns: []string{"STATUS", "NAME"}}})
	s := tableSort{layoutKey: "runtimes"}
	src := runtimeColumns()
	cols := s.layout(src, 0)
	if got := columnTitles(cols); !slices.Equal(got, []string{"", "STATUS", "NAME"}) {
		t.Errorf("columns %q", got)
	}
	row := make(table.Row, len(src)+1)
	for i := range row {
		row[i] = columnTitlesOr(src, i)
	}
	rows := []table.Row{row}
	sortRows[struct{}](&s, rows, nil)
	if !slices.Equal(rows[0], table.Row{"marker", "STATUS", "NAME", "extra"}) {
		t.Errorf("row %q", rows[0])
	}
}

// columnTitlesOr names cell i of a row built from src by its column, the
// marker as "marker" and a cell past the columns as "extra".
func columnTitlesOr(src []table.Column, i int) string {
	switch {
	case i >= len(src):
		return "extra"
	case src[i].Title == "":
		return "marker"
	}
	return src[i].Title
}

// TestEveryLayoutViewNamesItself: each view views.yaml can name carries
// that name, or its layout would silently never apply.
func TestEveryLayoutViewNamesItself(t *testing.T) {
	got := map[string]string{
		"containers":   NewContainersView(nil, false, false).layoutKey,
		"images":       NewImagesView(nil, false).layoutKey,
		"volumes":      NewVolumesView(nil).layoutKey,
		"networks":     NewNetworksView(nil).layoutKey,
		"projects":     NewProjectsView(nil).layoutKey,
		"runtimes":     NewRuntimesView(nil, "").layoutKey,
		"pods":         NewPodsView(nil).layoutKey,
		"portforwards": NewPortForwardsView(nil).layoutKey,
		"diskusage":    NewDiskUsageView(nil).layoutKey,
		"contexts":     NewContextsView("").layoutKey,
		"lint":         NewLintView(nil).layoutKey,
		"dir":          NewDirView(t.TempDir()).layoutKey,
		"dumps":        NewDumpsView(t.TempDir()).layoutKey,
		"node":         NewNodeView(nil, "", "").layoutKey,
		"layers":       NewLayersView(nil, "", "").layoutKey,
		"files":        NewVolumeBrowseView(nil, "").layoutKey,
		"scan":         NewScanView("", "").layoutKey,
	}
	if !slices.Equal(LayoutViews(), slices.Sorted(maps.Keys(got))) {
		t.Errorf("layout views %q, constructors checked %q", LayoutViews(), slices.Sorted(maps.Keys(got)))
	}
	for want, name := range got {
		if name != want {
			t.Errorf("the %s view calls itself %q", want, name)
		}
	}
}
