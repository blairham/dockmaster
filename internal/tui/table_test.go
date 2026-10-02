package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
