// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// TestEventFaults: ctrl-z in the events view shows only what failed or was
// destroyed, as it does in the containers view, and ctrl-z again brings the
// rest back — nothing was dropped (#18).
func TestEventFaults(t *testing.T) {
	a := newTestApp(t)
	a.dispatchCommand("events")
	ev := typedView[*views.EventsView](a, style.ViewEvents)
	now := time.Now()
	step(a, views.EventsBatchMsg{Events: []docker.Event{
		{Time: now, Type: "container", Action: "start", Name: "web"},
		{Time: now, Type: "container", Action: "die", Name: "worker", Attrs: map[string]string{"exitCode": "137"}},
		{Time: now, Type: "container", Action: "die", Name: "job", Attrs: map[string]string{"exitCode": "0"}},
	}})
	if ev.Count() != 3 {
		t.Fatalf("setup: %d events", ev.Count())
	}
	step(a, key("ctrl+z"))
	if ev.Count() != 1 || !strings.Contains(render(a), "worker") || !strings.Contains(render(a), "faults") {
		t.Errorf("faults: %d shown\n%s", ev.Count(), render(a))
	}
	step(a, key("ctrl+z"))
	if ev.Count() != 3 {
		t.Errorf("after ctrl-z again: %d events, want all 3 back", ev.Count())
	}
}

// TestHelpCommand: :help opens the help overlay, as ? does.
func TestHelpCommand(t *testing.T) {
	a := newTestApp(t)
	if errMsg, _ := a.dispatchCommand("help"); errMsg != "" || !a.showHelp {
		t.Errorf(":help: err %q, help shown %v", errMsg, a.showHelp)
	}
}

// TestConfiguredShell: with a shell configured, s asks the container for it
// first; the probe runs it when present and falls back to bash-then-sh when
// not, and the name reaches the shell as an argument, never as script.
func TestConfiguredShell(t *testing.T) {
	a := newTestApp(t)
	a.shell = "zsh"
	var ran *exec.Cmd
	a.execProcess = func(c *exec.Cmd, _ tea.ExecCallback) tea.Cmd { ran = c; return nil }
	loadContainers(a)
	step(a, key("s"))
	if ran == nil {
		t.Fatalf("s handed over nothing (err %q)", a.errFlash)
	}
	args := ran.Args
	if n := len(
		args,
	); n < 4 || args[n-4] != "sh" || args[n-3] != "-c" || args[n-2] != preferredShellProbe ||
		args[n-1] != "zsh" {
		t.Fatalf("exec args %q", args)
	}

	// The probe itself, run here with fake shells on PATH.
	dir := t.TempDir()
	for _, name := range []string{"zsh", "bash"} {
		if err := os.WriteFile(
			filepath.Join(dir, name),
			[]byte("#!/bin/sh\necho "+name+"\n"),
			0o700,
		); err != nil { //nolint:gosec // test fake
			t.Fatal(err)
		}
	}
	run := func(shell string) string {
		cmd := exec.Command("/bin/sh", "-c", preferredShellProbe, shell) //nolint:gosec // the probe under test
		cmd.Env = append(os.Environ(), "PATH="+dir+":/bin:/usr/bin")
		out, _ := cmd.Output()
		return strings.TrimSpace(string(out))
	}
	if got := run("zsh"); got != "zsh" {
		t.Errorf("configured zsh, present: ran %q", got)
	}
	if got := run("fish"); got != "bash" {
		t.Errorf("configured fish, absent: ran %q, want the bash fallback", got)
	}
	if got := run("x; echo pwned"); got != "bash" {
		t.Errorf("a hostile shell name ran %q; it must be one argument, not script", got)
	}
}
