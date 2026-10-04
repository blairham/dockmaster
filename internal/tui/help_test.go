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

// TestHelpCoversEveryCommand: every `:` command the palette knows is in
// the help as <:name>, or reached by a key the help lists, or a spelling
// of one that is (#53). A command added to knownCommands without either
// fails here.
func TestHelpCoversEveryCommand(t *testing.T) {
	covered := map[string]string{
		"q!": "q", "quit": "q", "exit": "q",
		"containers": "<0>", "ps": "<0>", "images": "<1>", "volumes": "<2>", "networks": "<3>",
		"projects": "<4>", "compose": "<4>", "runtimes": "<5>", "colima": "<5>", "events": "<6>",
		"context": ":ctx", "contexts": ":ctx", "pulses": ":pu", "help": "<?>", "screendump": ":sd",
		"logs": "<l>", "inspect": "<o>", "describe": "<d>", "top": "<T>", "diff": "<D>", "health": "<H>",
		"prune all": ":prune", "prune all volumes": ":prune", "prune cache": ":prune",
		"logo": ":logo", "logoless": ":logo", "stats": "<t>", "all": "<a>",
	}
	a := newTestApp(t)
	keys := map[string]bool{}
	for _, sec := range a.helpPanel().Sections {
		for _, e := range sec.Entries {
			keys[e.Key] = true
		}
	}
	// Keys the sweep found missing that no command stands for.
	for _, k := range []string{"<shift-f>"} {
		if !keys[k] {
			t.Errorf("%s is not in the help", k)
		}
	}
	for _, c := range knownCommands {
		want := "<:" + c + ">"
		if via, ok := covered[c]; ok {
			want = via
			if strings.HasPrefix(via, ":") || !strings.HasPrefix(via, "<") {
				want = "<:" + strings.TrimPrefix(via, ":") + ">"
			}
		}
		if !keys[want] {
			t.Errorf(":%s is not in the help (looked for %s)", c, want)
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
