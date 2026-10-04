// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"
	tktable "github.com/blairham/tuikit/table"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// tableKey handles the keys every table view shares, after the view's own
// keys have passed on them: ctrl+s saves the table, shift+arrows sort it,
// and space, ctrl+space and ctrl+\ mark rows. It reports whether the
// key was one of them.
func (a *App) tableKey(key string) (tea.Cmd, bool) {
	tv, ok := a.activeView().(views.Tabler)
	if !ok {
		return nil, false
	}
	if key == chrome.KeySave {
		a.saveTable(tv)
		return nil, true
	}
	if key == "c" || key == "i" {
		if c, ok := tv.(views.Copier); ok {
			return a.copyRow(c, key == "i"), true
		}
	}
	if s, ok := tv.(views.SortKeyer); ok && s.SortKey(key) {
		return nil, true
	}
	if m, ok := tv.(views.Marker); ok && m.MarkKey(key) {
		if n := m.MarkCount(); n > 0 {
			a.flash = fmt.Sprintf("%d marked", n)
		}
		return nil, true
	}
	return nil, false
}

// copyRow puts the selected row's name — or, with id, its full ID — on the
// clipboard, as k9s's c copies a resource's name (#5). The terminal does
// the copy, through OSC 52, so it works over ssh too.
func (a *App) copyRow(c views.Copier, id bool) tea.Cmd {
	name, full, ok := c.CopyFields()
	if !ok {
		a.errFlash = "nothing selected to copy"
		return nil
	}
	what, text := "", name
	if id {
		if full == "" {
			a.errFlash = "these rows have no ID — c copies the name"
			return nil
		}
		what, text = "ID ", full
	}
	a.flash = "copied " + what + text
	return tea.SetClipboard(text)
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
	path, err := a.saveDump("dumps", a.viewName(), tktable.PlainText(t.Columns(), rows))
	if err != nil {
		a.errFlash = "saving " + a.viewName() + ": " + err.Error()
		return
	}
	a.flash = fmt.Sprintf("saved %d rows to %s", len(rows), path)
}

// dumpRoot is where saves go and :sd looks: screenDumpDir when it is set,
// else the state directory.
func (a *App) dumpRoot() (string, error) {
	if a.dumpDir != "" {
		return a.dumpDir, nil
	}
	return config.StateDir()
}

// saveDump writes content under <root>/<kind> with tuikit's naming:
// <name>-<timestamp>.txt, never overwriting an earlier save.
func (a *App) saveDump(kind, name, content string) (string, error) {
	dir, err := a.dumpRoot()
	if err != nil {
		return "", err
	}
	return chrome.SaveDump(filepath.Join(dir, kind), name, content)
}

// bulkConfirmed are the bulk actions that ask first, each with what it runs
// once confirmed and the noun for the question.
var bulkConfirmed = map[string]struct{ run, noun string }{
	"kill":                     {run: "kill", noun: "container"},
	"confirm_remove_container": {run: "remove_container", noun: "container"},
	"confirm_remove_image":     {run: "remove_image", noun: "image"},
	"confirm_remove_volume":    {run: "remove_volume", noun: "volume"},
	"confirm_remove_network":   {run: "remove_network", noun: "network"},
}

// bulkDirect are the bulk actions that run without asking, as they do on
// one row.
var bulkDirect = map[string]bool{
	"start": true, "stop": true, "restart": true, "pause": true, "unpause": true,
}

func bulkable(name string) bool { return bulkDirect[name] || bulkConfirmed[name].run != "" }

// bulkKey runs key on every marked row of a marked table, as k9s does —
// for the keys that make sense on many rows at once (lifecycle and
// remove). It reports false to leave every other key to the cursor row.
func (a *App) bulkKey(key string) (tea.Cmd, bool) {
	m, ok := a.activeView().(views.Marker)
	if !ok || m.MarkCount() == 0 {
		return nil, false
	}
	var acts, skipped []views.Action
	for _, act := range m.BulkKey(key) {
		if bulkable(act.Name) {
			acts = append(acts, act)
		} else {
			skipped = append(skipped, act)
		}
	}
	if len(acts) == 0 {
		return nil, false
	}
	if a.readonly && mutating[acts[0].Name] {
		a.errFlash = "readonly mode — " + strings.TrimPrefix(acts[0].Name, "confirm_") + " refused"
		return nil, true
	}
	note := ""
	if len(skipped) > 0 {
		note = fmt.Sprintf(" (%d skipped: %s)", len(skipped), labels(skipped))
	}

	if c, ok := bulkConfirmed[acts[0].Name]; ok {
		verb := strings.ReplaceAll(c.run, "_"+c.noun, "")
		if c.run == "kill" {
			verb = "SIGKILL"
		}
		a.confirmDispatch = func(yes bool) (string, tea.Cmd) {
			if !yes {
				return "", nil
			}
			cmds := make([]tea.Cmd, 0, len(acts))
			for _, act := range acts {
				cmds = append(cmds, a.executeConfirmed(pendingAction{action: c.run, param: act.Param}))
			}
			m.ClearMarks()
			return "", tea.Batch(cmds...)
		}
		a.confirm.Open(fmt.Sprintf("%s %d %ss: %s?%s", verb, len(acts), c.noun, labels(acts), note))
		return nil, true
	}

	cmds := make([]tea.Cmd, 0, len(acts))
	for _, act := range acts {
		_, cmd := a.handleAction(act.Name, act.Param)
		cmds = append(cmds, cmd)
	}
	m.ClearMarks()
	a.flash = fmt.Sprintf("%s %d marked%s…", acts[0].Name, len(acts), note)
	return tea.Batch(cmds...), true
}

// labels lists actions' rows for a question or a flash, the first few by
// name.
func labels(acts []views.Action) string {
	const show = 4
	names := make([]string, 0, show)
	for i, act := range acts {
		if i == show {
			names = append(names, fmt.Sprintf("and %d more", len(acts)-show))
			break
		}
		names = append(names, act.Label)
	}
	return strings.Join(names, ", ")
}
