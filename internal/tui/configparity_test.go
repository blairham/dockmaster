// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

func optsApp(t *testing.T, opts Options) *App {
	t.Helper()
	opts.Version = "test"
	a := NewApp(nil, opts)
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 40})
	return a
}

// TestNoExitOnCtrlC: with noExitOnCtrlC, ctrl-c does nothing but say how to
// quit; without it, it quits (#17).
func TestNoExitOnCtrlC(t *testing.T) {
	a := optsApp(t, Options{NoExitOnCtrlC: true})
	if cmd := step(a, key("ctrl+c")); cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("ctrl-c quit with noExitOnCtrlC")
		}
	}
	if !strings.Contains(a.flash, ":q quits") {
		t.Errorf("flash %q", a.flash)
	}
	b := optsApp(t, Options{})
	cmd := step(b, key("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl-c did nothing by default")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("ctrl-c did not quit by default")
	}
}

// TestScreenDumpDir: with screenDumpDir set, ctrl-s saves go there and :sd
// lists them there.
func TestScreenDumpDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // must not be used
	dir := t.TempDir()
	a := optsApp(t, Options{DumpDir: dir})
	loadContainers(a)
	step(a, key("ctrl+s"))
	files, _ := filepath.Glob(filepath.Join(dir, "dumps", "containers-*.txt"))
	if len(files) != 1 {
		t.Fatalf("ctrl-s wrote %v to %s (flash %q err %q)", files, dir, a.flash, a.errFlash)
	}
	_, cmd := a.dispatchCommand("sd")
	runCmd(a, cmd)
	if dv := typedView[*views.DumpsView](a, style.ViewDumps); dv == nil || dv.Count() != 1 {
		t.Errorf(":sd does not list the save in screenDumpDir")
	}
}

// TestLogOpenDefaults: textWrap, disableAutoscroll and defaultsToFullScreen
// set how every log view opens.
func TestLogOpenDefaults(t *testing.T) {
	a := optsApp(t, Options{LogWrap: true, LogPaused: true, LogFullscreen: true})
	loadContainers(a)
	step(a, key("l"))
	lv := typedView[*views.LogsView](a, style.ViewLogs)
	if a.view != style.ViewLogs || lv == nil {
		t.Fatalf("l opened %v", a.view)
	}
	if !lv.Wrap() || lv.Follow() || !a.fullscreen {
		t.Errorf(
			"log opened wrap %v follow %v fullscreen %v; want wrapped, paused, fullscreen",
			lv.Wrap(),
			lv.Follow(),
			a.fullscreen,
		)
	}
	b := optsApp(t, Options{})
	loadContainers(b)
	step(b, key("l"))
	if lv := typedView[*views.LogsView](b, style.ViewLogs); lv.Wrap() || !lv.Follow() || b.fullscreen {
		t.Errorf("defaults changed: wrap %v follow %v fullscreen %v", lv.Wrap(), lv.Follow(), b.fullscreen)
	}
}

// TestEnableMouse: enableMouse false leaves mouse reporting off, so a plain
// drag selects text; it is on by default.
func TestEnableMouse(t *testing.T) {
	if m := optsApp(t, Options{NoMouse: true}).View().MouseMode; m != tea.MouseModeNone {
		t.Errorf("enableMouse false: mouse mode %v", m)
	}
	if m := optsApp(t, Options{}).View().MouseMode; m != tea.MouseModeCellMotion {
		t.Errorf("default: mouse mode %v", m)
	}
}
