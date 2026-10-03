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

// openInspectDoc opens inspect on the first container and loads body.
func openInspectDoc(t *testing.T, live bool, body string) *App {
	t.Helper()
	a := NewApp(nil, Options{Version: "test", LiveRefresh: live})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 40})
	loadContainers(a)
	step(a, key("o"))
	if a.view != style.ViewInspect {
		t.Fatalf("o opened %v", a.view)
	}
	a.viewMap[style.ViewInspect].Update(views.InspectRefreshMsg{
		Kind: views.InspectContainer, ID: "aaaaaaaaaaaa1111", Body: []byte(body),
	})
	return a
}

// TestInspectCopyAndSave: c copies and ctrl-s saves what the inspect view
// shows — under the filter, styling stripped — as in a log (#6). Saves go
// with the table dumps, so :sd lists them.
func TestInspectCopyAndSave(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	a := openInspectDoc(t, false, "{\n  \"Name\": \"web\",\n  \"Image\": \"nginx\"\n}")
	a.filter = "Image"
	a.setActiveFilter("Image")

	if got := clipboard(step(a, key("c"))); got != `  "Image": "nginx"` || a.flash != "copied 1 lines" {
		t.Errorf("c copied %q (flash %q); want the one filtered line, unstyled", got, a.flash)
	}

	step(a, key("ctrl+s"))
	files, _ := filepath.Glob(filepath.Join(state, "dockmaster", "dumps", "*.txt"))
	if len(files) != 1 {
		t.Fatalf("ctrl-s wrote %v (flash %q err %q)", files, a.flash, a.errFlash)
	}
	b, _ := os.ReadFile(files[0]) //nolint:gosec // the test's own temp file
	if string(b) != `  "Image": "nginx"`+"\n" && string(b) != `  "Image": "nginx"` {
		t.Errorf("saved %q", b)
	}
	if strings.Contains(string(b), "\x1b") {
		t.Errorf("saved styling: %q", b)
	}
}

// TestInspectFullscreen: f gives the document the whole terminal, and
// leaving the view puts the header back.
func TestInspectFullscreen(t *testing.T) {
	a := openInspectDoc(t, false, "{}")
	step(a, key("f"))
	if !a.fullscreen || !a.chrome.HeaderHidden {
		t.Fatal("f did not go fullscreen")
	}
	step(a, key("esc"))
	if a.fullscreen || a.chrome.HeaderHidden {
		t.Error("leaving inspect kept fullscreen")
	}
}

// TestInspectAutoRefreshToggle: a turns refreshing on the poll on for this
// view when liveViewAutoRefresh is off, and off when it is on.
func TestInspectAutoRefreshToggle(t *testing.T) {
	for _, live := range []bool{false, true} {
		a := openInspectDoc(t, live, "{}")
		step(a, key("a"))
		if got := a.refreshPolledView() != nil; got == live {
			t.Errorf("liveViewAutoRefresh %v, after a: tick refreshes = %v, want %v", live, got, !live)
		}
		if want := "auto-refresh " + onOff(!live); a.flash != want {
			t.Errorf("flash %q, want %q", a.flash, want)
		}
	}
}
