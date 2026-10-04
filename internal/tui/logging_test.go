// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/applog"
	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// logTo opens a log file at level in a temp state directory, as main does,
// and returns its options and what reads it back.
func logTo(t *testing.T, level string) (Options, func() string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	logger, closer, err := applog.Open("", level)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	path, err := applog.Path("")
	if err != nil {
		t.Fatal(err)
	}
	return Options{Logger: logger}, func() string { return read(t, path) }
}

// TestFlashedErrorIsLogged: an error flash reaches the log file once while
// it shows — later messages do not repeat it — and again when it is shown
// again after a key cleared it (#60).
func TestFlashedErrorIsLogged(t *testing.T) {
	opts, log := logTo(t, "warn")
	opts.ReadOnly = true
	a := newSizedApp(t, opts, 160, 44)
	loadContainers(a)
	step(a, key("x"))
	if !strings.Contains(a.errFlash, "readonly mode — stop refused") {
		t.Fatalf("no flash to log: %q", a.errFlash)
	}
	step(a, tea.WindowSizeMsg{Width: 160, Height: 44})
	loadContainers(a)
	got := log()
	if !strings.Contains(got, `level=ERROR msg=flash error="readonly mode — stop refused" view=`) {
		t.Fatalf("the flash is not in the log:\n%s", got)
	}
	if n := strings.Count(got, "msg=flash"); n != 1 {
		t.Errorf("one flash logged %d times while it showed:\n%s", n, got)
	}
	step(a, key("x"))
	if n := strings.Count(log(), "msg=flash"); n != 2 {
		t.Errorf("the flash shown again was logged %d times in all, want 2:\n%s", n, log())
	}
}

// TestPluginInputValueIsNotLogged: a plugin's run and failure are logged
// with its name, input names and exit status — never the value typed into
// its form, though that value is in its arguments, its environment and
// the output the flash shows.
func TestPluginInputValueIsNotLogged(t *testing.T) {
	const secret = "typed-into-the-form"
	dir := t.TempDir()
	script := filepath.Join(dir, "leak")
	if err := os.WriteFile(
		script,
		[]byte("#!/bin/sh\necho \"$1 $INPUT_TOKEN\"\nexit 3\n"),
		0o700,
	); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}
	opts, log := logTo(t, "debug")
	a := pluginApp(t, opts, inputPlugin(config.Plugin{
		Command: script, Args: []string{"--token=$INPUT_TOKEN"}, Background: true, Confirm: no(),
	}, config.PluginInput{Name: "token"}))
	step(a, tea.KeyPressMsg{Code: tea.KeyF5})
	if a.view != style.ViewPluginForm {
		t.Fatalf("no inputs form: %v", a.view)
	}
	typeText(a, secret)
	runOnce(a, step(a, key("enter")))
	// The value did reach the plugin and the screen: the log's silence
	// about it is a choice, not an accident of a run that never happened.
	if !strings.Contains(a.errFlash, "exited 3") || !strings.Contains(a.errFlash, secret) {
		t.Fatalf("the plugin did not run with the value: flash %q", a.errFlash)
	}
	got := log()
	for _, want := range []string{
		`msg="plugin run" plugin=p command=` + script + " background=true inputs=[token]\n", `level=WARN msg="plugin failed" plugin=p exit=3 reason="exited 3"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, secret) {
		t.Errorf("the input's value is in the log:\n%s", got)
	}
}

// TestLogLevelFilters: at the default warn level the log has errors but
// not the info lines — plugin and hotkey runs, context switches; at info it
// has those too.
func TestLogLevelFilters(t *testing.T) {
	ok, _ := recorder(t, "0")
	run := func(level string) string {
		opts, log := logTo(t, level)
		opts.HotKeys = []HotKey{{Key: "f2", Label: "<f2>", Desc: "images", Command: "images"}}
		a := pluginApp(t, opts, map[string]config.Plugin{
			"bg": {ShortCut: "F5", Scopes: []string{"all"}, Command: ok, Background: true},
		})
		runOnce(a, step(a, tea.KeyPressMsg{Code: tea.KeyF5}))
		step(a, tea.KeyPressMsg{Code: tea.KeyF2})
		if a.view != style.ViewImages {
			t.Fatalf("the hotkey did not run: %v", a.view)
		}
		step(a, switchContextMsg{name: "colima", client: &docker.Client{Host: "unix:///run/colima.sock"}})
		step(a, switchContextMsg{name: "remote", err: errors.New("dial tcp: refused")})
		return log()
	}

	warn := run("warn")
	for _, want := range []string{`msg="context switch failed"`, "msg=flash", "refused"} {
		if !strings.Contains(warn, want) {
			t.Errorf("warn: log lacks %q:\n%s", want, warn)
		}
	}
	for _, not := range []string{"level=INFO", `msg="plugin run"`, "msg=hotkey", `msg="context switch" `} {
		if strings.Contains(warn, not) {
			t.Errorf("warn: log has %q:\n%s", not, warn)
		}
	}

	info := run("info")
	for _, want := range []string{
		`msg="plugin run" plugin=bg`, `msg="plugin done" plugin=bg exit=0`,
		`msg=hotkey key=<f2> command=images`,
		`msg="context switch" from="" to=colima endpoint=unix:///run/colima.sock`,
		`msg="context switch failed"`,
	} {
		if !strings.Contains(info, want) {
			t.Errorf("info: log lacks %q:\n%s", want, info)
		}
	}
}
