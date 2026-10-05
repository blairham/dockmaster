// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/config"
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
		w, h     int
		logs     bool
		projects bool
	}{
		{w: 120, h: 40},
		{w: 160, h: 44},
		{w: 220, h: 50},
		{w: 120, h: 40, logs: true},     // help over a log has its own LOGS column
		{w: 120, h: 40, projects: true}, // and over projects, a PROJECT column
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
		if tc.projects {
			a.switchView(style.ViewProjects)
		}
		step(a, key("?"))
		out := render(a)
		// The whole frame fits the terminal: a column taller than the
		// screen once pushed help's bottom border off it.
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) > size.h {
			t.Errorf("%dx%d: the frame is %d lines tall", size.w, size.h, len(lines))
		}
		if !strings.Contains(out, "╰") {
			t.Errorf("%dx%d: help's bottom border is not on screen", size.w, size.h)
		}
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

// TestHelpPointsAtTheCommands: help lists keys, and points at ctrl-a for
// the : commands, which TestAliasesViewCoversEveryCommand keeps complete;
// keys the gap sweep found missing are pinned here.
func TestHelpPointsAtTheCommands(t *testing.T) {
	a := newTestApp(t)
	keys := map[string]bool{}
	for _, sec := range a.helpPanel().Sections {
		for _, e := range sec.Entries {
			keys[e.Key] = true
			if strings.HasPrefix(e.Key, "<:") && e.Key != "<:cmd>" && e.Key != "<:logo>" && e.Key != "<:q>" {
				t.Errorf("help lists the command %s; commands belong in ctrl-a's list", e.Key)
			}
		}
	}
	for _, k := range []string{"<ctrl-a>", "<shift-f>", "<i>", "<I>"} {
		if !keys[k] {
			t.Errorf("%s is not in the help", k)
		}
	}
}

// TestHelpOverProjectsShowsProjectKeys: the projects view's keys replace
// the CONTAINER column, as a log's do.
func TestHelpOverProjectsShowsProjectKeys(t *testing.T) {
	a := newTestApp(t)
	a.switchView(style.ViewProjects)
	sections := a.helpPanel().Sections
	titles := make([]string, 0, len(sections))
	for _, s := range sections {
		titles = append(titles, s.Title)
	}
	if titles[1] != "PROJECT" {
		t.Errorf("sections over projects: %v", titles)
	}
}

// TestViewCommandNamesCoverEveryView: -c's list (its usage and its error)
// names every view the palette can open, and each name opens one (#54).
func TestViewCommandNamesCoverEveryView(t *testing.T) {
	named := map[style.ViewType]bool{}
	for _, n := range ViewCommandNames() {
		vt, ok := ViewForCommand(n)
		if !ok {
			t.Errorf("%q is listed but opens nothing", n)
		}
		named[vt] = true
	}
	for name, vt := range viewCommands {
		if !named[vt] {
			t.Errorf("-c %s opens %s, which ViewCommandNames leaves out", name, style.ViewName(vt))
		}
	}
}

// TestHelpFitsWithPluginsAndHotkeys: a user's PLUGINS and HOTKEYS columns
// stack under the shortest columns rather than widening help past a
// 120-column screen, every entry stays whole, and the frame fits (#84).
func TestHelpFitsWithPluginsAndHotkeys(t *testing.T) {
	ps, err := Plugins(map[string]config.Plugin{
		"dive": {
			ShortCut:    "Shift-D",
			Description: "Dive into image",
			Command:     "dive",
			Args:        []string{"$IMAGE"},
			Scopes:      []string{"all"},
		},
		"ctop": {ShortCut: "Ctrl-T", Description: "Container top", Command: "ctop", Scopes: []string{"all"}},
		"lazy": {ShortCut: "Shift-L", Description: "Lazydocker", Command: "lazydocker", Scopes: []string{"all"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	hk, err := HotKeys(map[string]config.HotKey{
		"pf": {ShortCut: "Shift-0", Description: "Port forwards", Command: "pf"},
		"df": {ShortCut: "Shift-1", Description: "Disk usage", Command: "df"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(nil, Options{Version: "test", Plugins: ps, HotKeys: hk})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 120, Height: 40})
	loadContainers(a)
	step(a, key("?"))
	out := render(a)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > 40 || !strings.Contains(out, "╰") {
		t.Errorf("help with plugins and hotkeys is %d lines, bottom border shown %v", len(lines), strings.Contains(out, "╰"))
	}
	if len(a.helpPanel().Sections) < 6 {
		t.Fatalf("only %d sections: the plugins and hotkeys columns were not built", len(a.helpPanel().Sections))
	}
	for _, sec := range a.helpPanel().Sections {
		for _, e := range sec.Entries {
			found := false
			for _, l := range lines {
				if i := strings.Index(l, e.Key); i >= 0 && strings.Contains(l[i:], e.Desc) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: %s %q is cut or missing", sec.Title, e.Key, e.Desc)
			}
		}
	}
}
