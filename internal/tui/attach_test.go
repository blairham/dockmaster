// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeDockerCLI puts a docker on PATH that prints each argument on its own
// line, so a test can run the real command attach builds and read exactly
// what docker would have been given.
func fakeDockerCLI(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do printf 'arg:%s\\n' \"$a\"; done\n"
	if err := os.WriteFile(
		filepath.Join(dir, "docker"),
		[]byte(script),
		0o700,
	); err != nil { //nolint:gosec // an executable test fake
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestAttach: A on a running container hands the terminal to docker attach
// on the daemon dockmaster shows (#16), with signals not proxied — a ctrl-c
// must not stop the container — and prints the way out first. The container
// ID reaches docker as one argument, whatever it contains.
func TestAttach(t *testing.T) {
	fakeDockerCLI(t)
	a := newTestApp(t)
	a.client = &docker.Client{Host: "unix:///run/fake.sock"}
	var ran *exec.Cmd
	a.execProcess = func(c *exec.Cmd, _ tea.ExecCallback) tea.Cmd { ran = c; return nil }
	odd := "aaaa1111; echo pwned $(id)"
	step(a, views.ContainersRefreshMsg{Containers: []docker.Container{{ID: odd, Name: "web", State: "running"}}})

	step(a, key("A"))
	if ran == nil {
		t.Fatalf("A did not hand over the terminal (err %q)", a.errFlash)
	}
	out, err := ran.Output()
	if err != nil {
		t.Fatalf("running the attach command: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], "ctrl-p ctrl-q detaches") {
		t.Errorf("the detach keys are not printed first:\n%s", out)
	}
	want := []string{"arg:--host", "arg:unix:///run/fake.sock", "arg:attach", "arg:--sig-proxy=false", "arg:" + odd}
	if got := lines[1:]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("docker was given\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestAttachRefused: a stopped container cannot be attached to, and
// --readonly refuses attach as it refuses a shell — both send keystrokes to
// the container.
func TestAttachRefused(t *testing.T) {
	a := newTestApp(t)
	ran := false
	a.execProcess = func(*exec.Cmd, tea.ExecCallback) tea.Cmd { ran = true; return nil }
	step(a, views.ContainersRefreshMsg{Containers: []docker.Container{{ID: "c1", Name: "worker", State: "exited"}}})
	step(a, key("A"))
	if ran || !strings.Contains(a.errFlash, "not running") {
		t.Errorf("A on a stopped container: ran %v, err %q", ran, a.errFlash)
	}

	b := newTestApp(t)
	b.readonly = true
	b.execProcess = func(*exec.Cmd, tea.ExecCallback) tea.Cmd { ran = true; return nil }
	step(b, views.ContainersRefreshMsg{Containers: []docker.Container{{ID: "c1", Name: "web", State: "running"}}})
	step(b, key("A"))
	if ran || !strings.Contains(b.errFlash, "readonly") {
		t.Errorf("A under readonly: ran %v, err %q", ran, b.errFlash)
	}
}
