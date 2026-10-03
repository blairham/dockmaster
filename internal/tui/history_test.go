// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// historyApp is a sized app whose bar history lives in path.
func historyApp(t *testing.T, path string) *App {
	t.Helper()
	a := NewApp(nil, Options{Version: "test", HistoryFile: path})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 40})
	return a
}

// TestBarHistorySurvivesARestart: what was typed in the command and filter
// bars comes back with up/down after a quit and a fresh start (#13) — all
// but the quit itself, which would otherwise be the first thing up recalls.
func TestBarHistorySurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "history.json")
	a := historyApp(t, path)
	step(a, key(":"))
	typeText(a, "images")
	step(a, key("enter"))
	step(a, key("/"))
	typeText(a, "nginx")
	step(a, key("enter"))
	step(a, key(":"))
	typeText(a, "q")
	step(a, key("enter"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf(":q did not save the history: %v", err)
	}

	b := historyApp(t, path)
	if got := strings.Join(b.commandBar.History(), ","); got != "images" {
		t.Errorf("command history after restart = %q, want images (and no q)", got)
	}
	if got := strings.Join(b.filterBar.History(), ","); got != "nginx" {
		t.Errorf("filter history after restart = %q, want nginx", got)
	}
	step(b, key(":"))
	step(b, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := b.commandBar.Value(); got != "images" {
		t.Errorf("up in the command bar recalled %q", got)
	}
}

// TestBarHistoryWithoutAFile: no history file — tests, or no state directory —
// means nothing is read or written, and a corrupt file is an empty history.
func TestBarHistoryWithoutAFile(t *testing.T) {
	a := historyApp(t, "")
	if err := a.saveHistory(""); err != nil {
		t.Errorf("saving with no file: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := historyApp(t, bad); len(b.commandBar.History()) != 0 {
		t.Errorf("a corrupt file loaded %v", b.commandBar.History())
	}
}
