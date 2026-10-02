// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// TestHelpShowsEveryEntryInFull renders the help overlay and checks that
// every entry the panel defines — key and description — is on screen
// whole. The overlay cuts a description to its column without a mark:
// "Containers in a kind node" once rendered as "Containers in a kind", and
// nothing noticed until it was looked at.
func TestHelpShowsEveryEntryInFull(t *testing.T) {
	for _, tc := range []struct {
		w, h int
		logs bool
	}{
		{w: 120, h: 40},
		{w: 160, h: 44},
		{w: 220, h: 50},
		{w: 120, h: 40, logs: true}, // help over a log has its own LOGS column
	} {
		size := tc
		a := NewApp(nil, Options{Version: "test"})
		a.splashActive, a.loading = false, false
		step(a, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		loadContainers(a)
		if tc.logs {
			a.setView(style.ViewLogs, views.NewLogsView(nil, "abc", "web"))
			a.pushView(style.ViewLogs)
		}
		step(a, key("?"))
		out := render(a)
		entries := 0
		for _, sec := range a.helpPanel().Sections {
			for _, e := range sec.Entries {
				entries++
				// Key and description sit on one line, the key padded to
				// its column: find a line holding both, description whole.
				found := false
				for _, l := range strings.Split(out, "\n") {
					if i := strings.Index(l, e.Key); i >= 0 && strings.Contains(l[i:], e.Desc) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%dx%d: help entry %s %q is cut or missing", size.w, size.h, e.Key, e.Desc)
				}
			}
		}
		if entries < 30 {
			t.Fatalf("%dx%d: only %d help entries — the panel was not read", size.w, size.h, entries)
		}
	}
}
