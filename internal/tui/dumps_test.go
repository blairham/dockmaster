// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// writeDumps puts a table dump and a saved log under a fresh state
// directory, the log the newer, and returns the state's dockmaster dir.
func writeDumps(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := filepath.Join(state, "dockmaster")
	files := []struct {
		kind, name, body string
		age              time.Duration
	}{
		{kind: "dumps", name: "containers-1.txt", body: "NAME  STATE\nweb   running\n", age: 2 * time.Hour},
		{kind: "logs", name: "web-2.txt", body: "GET / 200\nGET /health 200\n", age: time.Minute},
	}
	for _, f := range files {
		dir := filepath.Join(root, f.kind)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, []byte(f.body), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-f.age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// openDumps runs :sd and folds its listing in.
func openDumps(t *testing.T, a *App) {
	t.Helper()
	errMsg, cmd := a.dispatchCommand("sd")
	if errMsg != "" {
		t.Fatalf(":sd: %s", errMsg)
	}
	runCmd(a, cmd)
	if a.view != style.ViewDumps {
		t.Fatalf(":sd opened view %v", a.view)
	}
}

// TestScreenDumps: :sd lists the table dumps and saved logs, newest first,
// with their kind; enter shows one in the text viewer and esc comes back.
func TestScreenDumps(t *testing.T) {
	writeDumps(t)
	a := newTestApp(t)
	openDumps(t, a)

	out := render(a)
	logRow, dumpRow := strings.Index(out, "web-2.txt"), strings.Index(out, "containers-1.txt")
	if logRow < 0 || dumpRow < 0 || logRow > dumpRow {
		t.Fatalf("want both saves, newest first:\n%s", out)
	}
	for _, want := range []string{"logs", "dumps", "<ctrl-d>"} {
		if !strings.Contains(out, want) {
			t.Errorf("frame lacks %q:\n%s", want, out)
		}
	}

	runCmd(a, step(a, key("enter")))
	if a.view != style.ViewInspect || !strings.Contains(render(a), "GET /health 200") {
		t.Fatalf("enter did not show the log's text:\n%s", render(a))
	}
	step(a, key("esc"))
	if a.view != style.ViewDumps {
		t.Errorf("esc went to %v, not back to the dumps", a.view)
	}
}

// TestRemoveDump: ctrl+d asks, naming the file; yes deletes that file and
// only that one, and the list follows.
func TestRemoveDump(t *testing.T) {
	root := writeDumps(t)
	a := newTestApp(t)
	openDumps(t, a)

	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "web-2.txt") {
		t.Fatalf("ctrl+d should ask about web-2.txt, prompt %q", a.confirm.Prompt())
	}
	runCmd(a, step(a, key("y")))
	if _, err := os.Stat(filepath.Join(root, "logs", "web-2.txt")); !os.IsNotExist(err) {
		t.Errorf("the log is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dumps", "containers-1.txt")); err != nil {
		t.Errorf("the other save went too: %v", err)
	}
	if a.flash != "deleted web-2.txt" || a.errFlash != "" {
		t.Errorf("flash %q err %q", a.flash, a.errFlash)
	}
	// The view, not the frame: the flash names the deleted file too.
	v := typedView[*views.DumpsView](a, style.ViewDumps)
	if d, ok := v.Selected(); v.Count() != 1 || !ok || d.Name != "containers-1.txt" {
		t.Errorf("the list did not follow the delete: %d rows, cursor on %q", v.Count(), d.Name)
	}
}

// TestRemoveDumpReadonly: under readonly ctrl+d is refused before it asks.
func TestRemoveDumpReadonly(t *testing.T) {
	root := writeDumps(t)
	a := newTestApp(t)
	a.readonly = true
	openDumps(t, a)

	step(a, key("ctrl+d"))
	if a.confirm.Active() || !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("readonly: confirm %v, err %q", a.confirm.Active(), a.errFlash)
	}
	if _, err := os.Stat(filepath.Join(root, "logs", "web-2.txt")); err != nil {
		t.Errorf("readonly deleted a save: %v", err)
	}
}

// TestRemoveDumpStaysInside: the delete refuses a path outside the dump
// directories, whatever reaches it.
func TestRemoveDumpStaysInside(t *testing.T) {
	root := writeDumps(t)
	outside := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg, ok := removeDump(root, outside)().(actionDoneMsg)
	if !ok || msg.err == nil {
		t.Errorf("deleting %s was not refused: %+v", outside, msg)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the file outside went: %v", err)
	}
	if !views.IsDumpPath(root, filepath.Join(root, "logs", "web-2.txt")) {
		t.Error("a saved log is not a dump path")
	}
}

// TestScreenDumpsEmpty: with nothing saved, :sd says how to save one.
func TestScreenDumpsEmpty(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a := newTestApp(t)
	openDumps(t, a)
	if out := render(a); !strings.Contains(out, "nothing saved yet") {
		t.Errorf("empty :sd:\n%s", out)
	}
}
