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

// TestInspectCopyAndSave: c copies and ctrl-s saves the inspected document,
// styling stripped (#6). A search highlights rather than hides, so they take
// the whole document while one is active. Saves go with the table dumps, so
// :sd lists them.
func TestInspectCopyAndSave(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	doc := "{\n  \"Name\": \"web\",\n  \"Image\": \"nginx\"\n}"
	a := openInspectDoc(t, false, doc)
	a.filter = "Image"
	a.setActiveFilter("Image")

	if got := clipboard(step(a, key("c"))); got != doc || a.flash != "copied 4 lines" {
		t.Errorf("c copied %q (flash %q); want the whole document, unstyled", got, a.flash)
	}

	step(a, key("ctrl+s"))
	files, _ := filepath.Glob(filepath.Join(state, "dockmaster", "dumps", "*.txt"))
	if len(files) != 1 {
		t.Fatalf("ctrl-s wrote %v (flash %q err %q)", files, a.flash, a.errFlash)
	}
	b, _ := os.ReadFile(files[0]) //nolint:gosec // the test's own temp file
	if strings.TrimRight(string(b), "\n") != doc {
		t.Errorf("saved %q", b)
	}
	if strings.Contains(string(b), "\x1b") {
		t.Errorf("saved styling: %q", b)
	}
}

// TestInspectSearch: / in inspect is k9s's describe search (#6): every line
// stays, the title counts the matches, and n / N step through them with
// wraparound; n with nothing matching says so.
func TestInspectSearch(t *testing.T) {
	doc := "{\n  \"Name\": \"web\",\n  \"Image\": \"nginx\",\n  \"ImageID\": \"sha256:a\",\n  \"Labels\": {\"image\": \"x\"}\n}"
	a := openInspectDoc(t, false, doc)
	a.filter = "image"
	a.setActiveFilter("image")
	iv := typedView[*views.InspectView](a, style.ViewInspect)
	if iv.Count() != 6 {
		t.Errorf("a search hid lines: %d of 6 shown", iv.Count())
	}
	if got := iv.Status(); got != "1/3" {
		t.Fatalf("after /image the counter reads %q, want 1/3", got)
	}
	if out := render(a); !strings.Contains(out, "1/3") {
		t.Errorf("the title does not show the counter:\n%s", out)
	}
	// Three matches, so forward and back are told apart.
	for _, c := range []struct{ key, want string }{
		{key: "n", want: "2/3"},
		{key: "n", want: "3/3"},
		{key: "n", want: "1/3"}, // wraps past the last
		{key: "N", want: "3/3"}, // and back past the first
		{key: "N", want: "2/3"},
	} {
		step(a, key(c.key))
		if got := iv.Status(); got != c.want {
			t.Errorf("%s: %q, want %s", c.key, got, c.want)
		}
	}

	a.setActiveFilter("nothing-matches")
	step(a, key("n"))
	if iv.Status() != "0/0" || !strings.Contains(a.errFlash, "no matches") {
		t.Errorf("n with no matches: counter %q, err %q", iv.Status(), a.errFlash)
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
