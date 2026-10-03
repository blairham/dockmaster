// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// barHistory is what the command and filter bars remember between runs (#13):
// the bars recall with up/down, and this file carries that across a quit.
type barHistory struct {
	Command []string `json:"command"`
	Filter  []string `json:"filter"`
}

// quitCommands are left out of the saved command history: the bar records
// a command before running it, so the last entry of every session would be
// :q, and up-then-enter in the next one would quit.
var quitCommands = []string{"q", "q!", "quit", "exit"}

// loadHistory reads path into the bars. A missing or unreadable file is an
// empty history — it is a convenience, never a reason not to start.
func (a *App) loadHistory(path string) {
	if path == "" {
		return
	}
	b, err := os.ReadFile(path) //nolint:gosec // the app's own state file
	if err != nil {
		return
	}
	var h barHistory
	if json.Unmarshal(b, &h) != nil {
		return
	}
	a.commandBar.SetHistory(h.Command)
	a.filterBar.SetHistory(h.Filter)
}

// saveHistory writes the bars' history to path, through a temporary file so
// a crash mid-write cannot leave half a history behind.
func (a *App) saveHistory(path string) error {
	if path == "" {
		return nil
	}
	h := barHistory{
		Command: slices.DeleteFunc(a.commandBar.History(), func(c string) bool {
			return slices.Contains(quitCommands, c)
		}),
		Filter: a.filterBar.History(),
	}
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
