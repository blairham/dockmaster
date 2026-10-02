// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/tui/style"
)

// TestLogKeysAreK9s: a log answers k9s's keys (#3) — s autoscroll, t
// timestamps, f fullscreen, shift-c clear — and the keys they replaced do
// nothing, so a stale habit cannot act by surprise. s no longer shells in:
// that is the container's key, one esc away, as in k9s.
func TestLogKeysAreK9s(t *testing.T) {
	a, lv := openLogs(t)
	execs := 0
	a.execProcess = func(*exec.Cmd, tea.ExecCallback) tea.Cmd { execs++; return nil }

	follow := lv.Follow()
	step(a, key("s"))
	if lv.Follow() == follow || execs != 0 {
		t.Errorf("s: follow %v -> %v, execs %d; want autoscroll toggled and no shell", follow, lv.Follow(), execs)
	}
	step(a, key("t"))
	if !lv.Timestamps() {
		t.Error("t did not turn timestamps on")
	}

	type state struct {
		follow, timestamps, fullscreen bool
		lines                          int
	}
	now := func() state { return state{lv.Follow(), lv.Timestamps(), a.fullscreen, lv.Count()} }
	if now().lines == 0 {
		t.Fatal("setup: the log is empty, so a clear would go unseen")
	}
	for _, k := range []string{"F", "T", "ctrl+k"} {
		before := now()
		step(a, key(k))
		if after := now(); after != before {
			t.Errorf("%s still acts in a log: %+v -> %+v", k, before, after)
		}
	}
}

// TestDescribeAndYamlOpenInspect: k9s's d and y open inspect wherever o
// does, and are inert where o is (#3).
func TestDescribeAndYamlOpenInspect(t *testing.T) {
	for _, k := range []string{"d", "y"} {
		a := newTestApp(t)
		loadContainers(a)
		step(a, key(k))
		if a.view != style.ViewInspect {
			t.Errorf("%s on a container opened %v, want inspect", k, a.view)
		}

		b := newTestApp(t)
		loadImages(b)
		step(b, key("1"))
		loadImages(b)
		step(b, key(k))
		if b.view != style.ViewInspect {
			t.Errorf("%s on an image opened %v, want inspect", k, b.view)
		}
	}
}
