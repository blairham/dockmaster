// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// TestSaveTable: ctrl+s on a table writes the header and every row the
// filter lets through, styling stripped, to <state>/dumps.
func TestSaveTable(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	a := newTestApp(t)
	loadContainers(a)
	a.filter = "acme"
	a.setActiveFilter("acme")

	step(a, key("ctrl+s"))
	files, _ := filepath.Glob(filepath.Join(state, "dockmaster", "dumps", "containers-*.txt"))
	if len(files) != 1 {
		t.Fatalf("ctrl-s wrote %v (flash %q err %q)", files, a.flash, a.errFlash)
	}
	b, _ := os.ReadFile(files[0]) //nolint:gosec // the test's own temp file
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "NAME") ||
		!strings.HasPrefix(lines[1], "api ") || !strings.HasPrefix(lines[2], "migrate ") {
		t.Errorf("saved the wrong rows:\n%s", b)
	}
	if strings.Contains(string(b), "\x1b") || strings.Contains(string(b), "web") {
		t.Errorf("saved styling, or a row the filter hides:\n%q", b)
	}
	if a.flash != "saved 2 rows to "+files[0] {
		t.Errorf("flash %q", a.flash)
	}

	a.filter = "nothing-matches"
	a.setActiveFilter(a.filter)
	step(a, key("ctrl+s"))
	if a.errFlash != "nothing to save" {
		t.Errorf("an empty table: err %q", a.errFlash)
	}
}

// TestSortTable: shift+→ sorts the table by NAME and marks the header; the
// cursor still names the container drawn under it; shift+→ moves the sort
// column, shift+↓ reverses it, and the order and the mark survive a resize
// and a refresh.
func TestSortTable(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	v := typedView[*views.ContainersView](a, style.ViewContainers)
	names := func() string {
		out := make([]string, 0, len(v.Table().Rows()))
		for _, r := range v.Table().Rows() {
			out = append(out, ansiRe(r[0]))
		}
		return strings.Join(out, ",")
	}
	if got := names(); got != "web,api,migrate,standalone" {
		t.Fatalf("setup: unsorted order %s", got)
	}

	step(a, key("shift+right"))
	if got := names(); got != "api,migrate,standalone,web" {
		t.Errorf("first sort key: %s, want by name", got)
	}
	if !strings.Contains(render(a), "NAME↑") {
		t.Errorf("the header does not mark the sort column:\n%s", render(a))
	}
	if c, _ := v.Selected(); c.Name != "api" {
		t.Errorf("cursor on the first row selects %q, want api", c.Name)
	}

	step(a, key("shift+down"))
	if got := names(); got != "web,standalone,migrate,api" || !strings.Contains(render(a), "NAME↓") {
		t.Errorf("shift+↓: %s", got)
	}
	step(a, key("down"))
	if c, _ := v.Selected(); c.Name != "standalone" {
		t.Errorf("second row selects %q, want standalone", c.Name)
	}

	step(a, tea.WindowSizeMsg{Width: 150, Height: 40})
	loadContainers(a)
	if got := names(); got != "web,standalone,migrate,api" || !strings.Contains(render(a), "NAME↓") {
		t.Errorf("after a resize and a refresh: %s", got)
	}

	step(a, key("shift+right"))
	out := render(a)
	if !strings.Contains(out, "IMAGE↑") || strings.Contains(out, "NAME↓") {
		t.Errorf("shift+→ did not move the sort to IMAGE:\n%s", out)
	}
	if got := names(); !strings.HasPrefix(got, "api,migrate") {
		t.Errorf("by image: %s", got)
	}
}

func ansiRe(s string) string { return ansi.ReplaceAllString(s, "") }
