// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package applog

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	for name, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn,
		"WARNING": slog.LevelWarn, " error ": slog.LevelError,
	} {
		if got, err := ParseLevel(name); err != nil || got != want {
			t.Errorf("%q: %v %v, want %v", name, got, err, want)
		}
	}
	if _, err := ParseLevel("loud"); err == nil || !strings.Contains(err.Error(), "debug, info, warn, error") {
		t.Errorf("a bad level: %v", err)
	}
	if _, _, err := Open(filepath.Join(t.TempDir(), "x.log"), "loud"); err == nil {
		t.Error("Open took a bad level")
	}
}

// TestOpenDefaultsUnderTheStateDir: with no --log-file the log is
// <state>/dockmaster.log, made 0600 in a directory made 0700, appended to
// across opens.
func TestOpenDefaultsUnderTheStateDir(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	path, err := Path("")
	if want := filepath.Join(state, "dockmaster", FileName); err != nil || path != want {
		t.Fatalf("Path: %q %v, want %q", path, err, want)
	}
	for _, line := range []string{"first", "second"} {
		l, c, oerr := Open("", "warn")
		if oerr != nil {
			t.Fatal(oerr)
		}
		l.Warn(line)
		if cerr := c.Close(); cerr != nil {
			t.Fatal(cerr)
		}
	}
	b, err := os.ReadFile(path) //nolint:gosec // the test's own temp file
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !strings.Contains(got, "msg=first") || !strings.Contains(got, "msg=second") {
		t.Errorf("not appended:\n%s", got)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("log file mode %v, want 0600", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Dir(path)); st.Mode().Perm() != 0o700 {
		t.Errorf("log dir mode %v, want 0700", st.Mode().Perm())
	}
	if p, _ := Path("/elsewhere/d.log"); p != "/elsewhere/d.log" {
		t.Errorf("--log-file ignored: %q", p)
	}
}

// TestLevelFilters: lines below the level are dropped.
func TestLevelFilters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.log")
	l, c, err := Open(path, "warn")
	if err != nil {
		t.Fatal(err)
	}
	l.Debug("dbg")
	l.Info("inf")
	l.Warn("wrn")
	l.Error("err")
	_ = c.Close()
	b, _ := os.ReadFile(path) //nolint:gosec // the test's own temp file
	got := string(b)
	if strings.Contains(got, "msg=dbg") || strings.Contains(got, "msg=inf") ||
		!strings.Contains(got, "level=WARN msg=wrn") || !strings.Contains(got, "level=ERROR msg=err") {
		t.Errorf("warn level wrote:\n%s", got)
	}
}

// TestRotatesOnceAtTheLimit: a write that would take the file past the
// limit moves it to .1 first, replacing an older .1; nothing is lost from
// the current file, and a file already past the limit rotates on its first
// write.
func TestRotatesOnceAtTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.log")
	f, err := openFile(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"aaaa\n", "bbbb\n", "cccc\n", "dddd\n", "eeeeeeeeeeeeeeee\n"} {
		if _, werr := f.Write([]byte(s)); werr != nil {
			t.Fatal(werr)
		}
	}
	_ = f.Close()
	cur, _ := os.ReadFile(path)        //nolint:gosec // the test's own temp file
	old, _ := os.ReadFile(path + ".1") //nolint:gosec // the test's own temp file
	if string(cur) != "eeeeeeeeeeeeeeee\n" || string(old) != "cccc\ndddd\n" {
		t.Errorf("after rotation: current %q, .1 %q", cur, old)
	}

	if werr := os.WriteFile(path, []byte("0123456789ABC\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	f, err = openFile(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("new\n"))
	_ = f.Close()
	cur, _ = os.ReadFile(path)        //nolint:gosec // the test's own temp file
	old, _ = os.ReadFile(path + ".1") //nolint:gosec // the test's own temp file
	if string(cur) != "new\n" || string(old) != "0123456789ABC\n" {
		t.Errorf("an oversized file at open: current %q, .1 %q", cur, old)
	}
}
