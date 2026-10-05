// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/style"
)

func TestParseShortcut(t *testing.T) {
	for in, want := range map[string]string{
		"Shift-0": ")", "shift-1": "!", "Shift-9": "(", "Shift-A": "A", "Shift-z": "Z",
		"Ctrl-U": "ctrl+u", "Alt-X": "alt+x", "F2": "f2", "Ctrl-F5": "ctrl+f5", "x": "x", "%": "%",
	} {
		if got, err := ParseShortcut(in); err != nil || got != want {
			t.Errorf("ParseShortcut(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "Shift-!", "Ctrl-Space2", "Hyper-X", "Fx"} {
		if _, err := ParseShortcut(bad); err == nil {
			t.Errorf("ParseShortcut(%q) accepted it", bad)
		}
	}
}

func TestHotKeysValidation(t *testing.T) {
	hk, err := HotKeys(map[string]config.HotKey{
		"forwards": {ShortCut: "Shift-0", Description: "Port forwards", Command: "pf"},
		"pg":       {ShortCut: "Ctrl-U", Command: "pg"},
	}, map[string]string{"pg": "containers /postgres"})
	if err != nil || len(hk) != 2 {
		t.Fatalf("good hotkeys: %v %v", hk, err)
	}
	if hk[1].Key != "ctrl+u" || hk[1].Desc != ":pg" || hk[1].Label != "<ctrl-u>" {
		t.Errorf("a hotkey with no description: %+v", hk[1])
	}
	for _, tc := range []struct {
		entries map[string]config.HotKey
		want    string
	}{
		{entries: map[string]config.HotKey{"n": {ShortCut: "j", Command: "pf"}}, want: "j is a dockmaster key"},
		{entries: map[string]config.HotKey{"n": {ShortCut: "3", Command: "pf"}}, want: "3 is a dockmaster key"},
		{entries: map[string]config.HotKey{"n": {ShortCut: "Ctrl-S", Command: "pf"}}, want: "is a dockmaster key"},
		{entries: map[string]config.HotKey{"a": {ShortCut: "F2", Command: "pf"}, "b": {ShortCut: "f2", Command: "df"}}, want: `already hotkey "a"`},
		{entries: map[string]config.HotKey{"n": {ShortCut: "F3", Command: "bogus"}}, want: `"bogus" is not a command`},
		{entries: map[string]config.HotKey{"n": {ShortCut: "F3"}}, want: "no command"},
		{entries: map[string]config.HotKey{"n": {ShortCut: "Hyper-Q", Command: "pf"}}, want: "is not a key"},
	} {
		if _, err := HotKeys(tc.entries, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err %v, want %q", tc.entries, err, tc.want)
		}
	}
}

// TestHotKeyRunsItsCommandWhereTheViewLetsIt: a hotkey runs its command —
// except in a view that binds the same key itself, which keeps it.
func TestHotKeyRunsItsCommandWhereTheViewLetsIt(t *testing.T) {
	hk, err := HotKeys(map[string]config.HotKey{
		"df":     {ShortCut: "Shift-0", Command: "df"},
		"images": {ShortCut: "x", Description: "Images", Command: "images"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(nil, Options{Version: "test", HotKeys: hk})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 40})
	loadContainers(a)

	step(a, tea.KeyPressMsg{Code: ')', Text: ")"})
	if a.view != style.ViewDiskUsage {
		t.Errorf("Shift-0 (\")\") opened %v, want disk usage", a.view)
	}

	step(a, key("0"))
	loadContainers(a)
	step(a, key("x")) // the containers view's own x: stop the selected row
	if a.view != style.ViewContainers {
		t.Errorf("x in the containers view went to %v; the view's stop should win", a.view)
	}
	step(a, key("6")) // events: no x of its own
	step(a, key("x"))
	if a.view != style.ViewImages {
		t.Errorf("x in the events view went to %v, want the hotkey's images", a.view)
	}

	step(a, key("?"))
	out := render(a)
	if !strings.Contains(out, "HOTKEYS") || !strings.Contains(out, "<shift-0>") || !strings.Contains(out, "Images") {
		t.Errorf("help has no HOTKEYS column:\n%s", out)
	}
}

// TestHotKeyOverrideBeatsTheViewsKey: an override hotkey is asked before
// the view's own keys (k9s's override, #58); the same key without it
// leaves the view's binding alone.
func TestHotKeyOverrideBeatsTheViewsKey(t *testing.T) {
	for _, override := range []bool{false, true} {
		hk, err := HotKeys(map[string]config.HotKey{
			"images": {ShortCut: "x", Command: "images", Override: override},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if hk[0].Override != override {
			t.Fatalf("Override not carried: %+v", hk[0])
		}
		a := NewApp(nil, Options{Version: "test", HotKeys: hk})
		a.splashActive, a.loading = false, false
		step(a, tea.WindowSizeMsg{Width: 160, Height: 40})
		loadContainers(a)
		step(a, key("x"))
		want := style.ViewContainers // the view's own x: stop
		if override {
			want = style.ViewImages
		}
		if a.view != want {
			t.Errorf("override %v: x went to %s, want %s", override, style.ViewName(a.view), style.ViewName(want))
		}
	}
}

// TestHotKeyKeepHistory: k9s's keepHistory puts the view a hotkey opens on
// top of the one it was pressed in, so esc comes back; without it the
// hotkey switches views as a digit does, and esc has nowhere to go. A
// view already under the stack is switched to, never stacked twice.
func TestHotKeyKeepHistory(t *testing.T) {
	for _, keep := range []bool{false, true} {
		hk, err := HotKeys(map[string]config.HotKey{
			"images": {ShortCut: "Shift-1", Command: "images /nginx", KeepHistory: keep},
			"ctrs":   {ShortCut: "Shift-2", Command: "containers", KeepHistory: keep},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if hk[1].KeepHistory != keep {
			t.Fatalf("KeepHistory not carried: %+v", hk)
		}
		a := NewApp(nil, Options{Version: "test", HotKeys: hk})
		a.splashActive, a.loading = false, false
		step(a, tea.WindowSizeMsg{Width: 160, Height: 40})
		loadContainers(a)

		step(a, key("!"))
		if a.view != style.ViewImages || a.filter != "nginx" {
			t.Fatalf("keep %v: shift-1 opened %s filter %q", keep, style.ViewName(a.view), a.filter)
		}
		if got := len(a.viewStack); got != map[bool]int{false: 0, true: 1}[keep] {
			t.Errorf("keep %v: stack %v", keep, a.viewStack)
		}
		step(a, key("@")) // containers, already under images when kept
		if a.view != style.ViewContainers || len(a.viewStack) != 0 {
			t.Errorf("keep %v: shift-2 went to %s, stack %v", keep, style.ViewName(a.view), a.viewStack)
		}
		step(a, key("!"))
		step(a, key("esc")) // clears the filter
		step(a, key("esc"))
		want := style.ViewImages
		if keep {
			want = style.ViewContainers
		}
		if a.view != want {
			t.Errorf("keep %v: esc from the hotkey's view went to %s, want %s",
				keep, style.ViewName(a.view), style.ViewName(want))
		}
	}
}
