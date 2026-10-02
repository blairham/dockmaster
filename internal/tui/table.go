package tui

import (
	"fmt"
	"path/filepath"

	"github.com/blairham/tuikit/chrome"
	tktable "github.com/blairham/tuikit/table"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// tableKey handles the keys every table view shares, after the view's own
// keys have passed on them: ctrl+s saves the table, shift+arrows sort it. It reports whether the
// key was one of them.
func (a *App) tableKey(key string) bool {
	tv, ok := a.activeView().(views.Tabler)
	if !ok {
		return false
	}
	if key == chrome.KeySave {
		a.saveTable(tv)
		return true
	}
	if s, ok := tv.(views.SortKeyer); ok && s.SortKey(key) {
		return true
	}
	return false
}

// saveTable writes every row of the table — the filtered set, not only
// what is scrolled into view — as plain text to <state>/dumps, as k9s's
// ctrl+s saves a screen dump.
func (a *App) saveTable(tv views.Tabler) {
	t := tv.Table()
	rows := t.Rows()
	if len(rows) == 0 {
		a.errFlash = "nothing to save"
		return
	}
	path, err := saveDump("dumps", a.viewName(), tktable.PlainText(t.Columns(), rows))
	if err != nil {
		a.errFlash = "saving " + a.viewName() + ": " + err.Error()
		return
	}
	a.flash = fmt.Sprintf("saved %d rows to %s", len(rows), path)
}

// saveDump writes content under <state>/<kind> with tuikit's naming:
// <name>-<timestamp>.txt, never overwriting an earlier save.
func saveDump(kind, name, content string) (string, error) {
	dir, err := config.StateDir()
	if err != nil {
		return "", err
	}
	return chrome.SaveDump(filepath.Join(dir, kind), name, content)
}
