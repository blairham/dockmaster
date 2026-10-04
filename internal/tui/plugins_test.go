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

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

func yes() *bool { b := true; return &b }

func TestPluginsValidation(t *testing.T) {
	good := map[string]config.Plugin{
		"dive": {
			ShortCut:    "Shift-D",
			Description: "Dive",
			Scopes:      []string{"images"},
			Command:     "dive",
			Args:        []string{"$IMAGE"},
		},
		"same":  {ShortCut: "Shift-D", Scopes: []string{"containers"}, Command: "true"},
		"every": {ShortCut: "Ctrl-T", Scopes: []string{"all"}, Command: "true", Confirm: yes()},
	}
	ps, err := Plugins(good, nil)
	if err != nil || len(ps) != 3 {
		t.Fatalf("good plugins: %v %v", ps, err)
	}
	for _, tc := range []struct {
		entries map[string]config.Plugin
		want    string
		hotKeys []HotKey
	}{
		{entries: map[string]config.Plugin{"p": {ShortCut: "j", Scopes: []string{"all"}, Command: "x"}}, want: "j is a dockmaster key"},
		{entries: map[string]config.Plugin{"p": {ShortCut: "F2", Scopes: []string{"all"}}}, want: "no command"},
		{entries: map[string]config.Plugin{"p": {ShortCut: "F2", Command: "x"}}, want: "no scopes"},
		{entries: map[string]config.Plugin{"p": {ShortCut: "F2", Scopes: []string{"pods-ish"}, Command: "x"}}, want: `scope "pods-ish" is not a view`},
		{entries: map[string]config.Plugin{
			"a": {ShortCut: "F2", Scopes: []string{"images"}, Command: "x"},
			"b": {ShortCut: "F2", Scopes: []string{"image", "volumes"}, Command: "x"},
		}, want: `already plugin "a" in images`},
		{entries: map[string]config.Plugin{
			"a": {ShortCut: "F2", Scopes: []string{"all"}, Command: "x"},
			"b": {ShortCut: "F2", Scopes: []string{"volumes"}, Command: "x"},
		}, want: `already plugin "a"`},
		{
			entries: map[string]config.Plugin{"p": {ShortCut: "F2", Scopes: []string{"all"}, Command: "x"}},
			hotKeys: []HotKey{{Key: "f2"}}, want: "already a hotkey",
		},
	} {
		if _, err := Plugins(tc.entries, tc.hotKeys); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err %v, want %q", tc.entries, err, tc.want)
		}
	}
}

func TestExpandPluginVars(t *testing.T) {
	t.Setenv("DM_TEST_HOME", "/home/x")
	vars := map[string]string{"NAME": "web", "COL-IMAGE": "nginx:1.27"}
	for in, want := range map[string]string{
		"$NAME":              "web",
		"${NAME}.log":        "web.log",
		"$COL-IMAGE":         "nginx:1.27",
		"$NAME-backup":       "web-backup",
		"$DM_TEST_HOME/bin":  "/home/x/bin",
		"$NOT_SET_ANYWHERE":  "",
		"--name=$NAME,$NAME": "--name=web,web",
	} {
		if got := expandPluginVars(in, vars); got != want {
			t.Errorf("expandPluginVars(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"CPU%↑": "CPU", "IMAGE ID": "IMAGE_ID", "NAME": "NAME"} {
		if got := colName(in); got != want {
			t.Errorf("colName(%q) = %q, want %q", in, got, want)
		}
	}
}

// pluginApp is the containers view with plugins, running a foreground
// plugin in place of handing it the terminal.
func pluginApp(t *testing.T, opts Options, entries map[string]config.Plugin) *App {
	t.Helper()
	ps, err := Plugins(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.Version, opts.Plugins = "test", ps
	a := NewApp(nil, opts)
	a.splashActive, a.loading = false, false
	a.execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		return func() tea.Msg { return fn(c.Run()) }
	}
	step(a, tea.WindowSizeMsg{Width: 170, Height: 40})
	loadContainers(a)
	return a
}

// recorder is a script that writes its argv and two variables to a file
// and exits with code.
func recorder(t *testing.T, code string) (script, out string) {
	t.Helper()
	dir := t.TempDir()
	script, out = filepath.Join(dir, "rec"), filepath.Join(dir, "out")
	body := "#!/bin/sh\necho \"$@\" > '" + out + "'\necho \"$NAME $COL_IMAGE\" >> '" + out + "'\nexit " + code + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}
	return script, out
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path) //nolint:gosec // the test's own temp file
	return string(b)
}

// TestPluginRunsOnTheSelectedRow: a plugin's args and environment carry
// the row under the cursor; a non-zero exit is reported.
func TestPluginRunsOnTheSelectedRow(t *testing.T) {
	ok, okOut := recorder(t, "0")
	bad, _ := recorder(t, "3")
	a := pluginApp(t, Options{}, map[string]config.Plugin{
		"rec": {
			ShortCut: "F5",
			Scopes:   []string{"containers"},
			Command:  ok,
			Args:     []string{"$NAME", "$COL-IMAGE", "$ID", "$COL_STATE"},
		},
		"fail": {ShortCut: "F6", Scopes: []string{"containers"}, Command: bad},
		"img":  {ShortCut: "F7", Scopes: []string{"images"}, Command: ok},
	})
	runOnce(a, step(a, tea.KeyPressMsg{Code: tea.KeyF5}))
	// $COL_STATE as an argument is expanded by dockmaster, not the shell —
	// a live run once came out empty for it.
	if got := read(t, okOut); got != "web nginx:1.27 aaaaaaaaaaaa1111 running\nweb nginx:1.27\n" {
		t.Errorf("plugin saw %q", got)
	}
	runOnce(a, step(a, tea.KeyPressMsg{Code: tea.KeyF6}))
	if !strings.Contains(a.errFlash, "plugin fail exited 3") {
		t.Errorf("a failing plugin: %q", a.errFlash)
	}
	if cmd := step(a, tea.KeyPressMsg{Code: tea.KeyF7}); cmd != nil {
		t.Error("an images plugin ran in the containers view")
	}
	if out := render(a); !strings.Contains(out, "<f5>") || strings.Contains(out, "<f7>") {
		t.Errorf("header plugins:\n%s", out)
	}
	step(a, key("?"))
	if out := render(a); !strings.Contains(out, "PLUGINS") || !strings.Contains(out, "rec") {
		t.Errorf("help has no PLUGINS column:\n%s", out)
	}
}

func TestPluginInTheBackground(t *testing.T) {
	ok, okOut := recorder(t, "0")
	a := pluginApp(t, Options{}, map[string]config.Plugin{
		"bg": {ShortCut: "F5", Scopes: []string{"all"}, Command: ok, Args: []string{"$NAME"}, Background: true},
	})
	runOnce(a, step(a, tea.KeyPressMsg{Code: tea.KeyF5}))
	if a.flash != "plugin bg done" || !strings.HasPrefix(read(t, okOut), "web\n") {
		t.Errorf("background: flash %q, ran %q", a.flash, read(t, okOut))
	}
}

// TestPluginGuards: override puts a plugin before the view's own key,
// confirm asks first, dangerous is refused in readonly, and a table with
// no row selected runs nothing.
func TestPluginGuards(t *testing.T) {
	ok, okOut := recorder(t, "0")
	a := pluginApp(t, Options{ReadOnly: true}, map[string]config.Plugin{
		"over":  {ShortCut: "x", Scopes: []string{"containers"}, Command: ok, Args: []string{"over"}, Override: true},
		"ask":   {ShortCut: "F5", Scopes: []string{"containers"}, Command: ok, Args: []string{"asked"}, Confirm: yes()},
		"risky": {ShortCut: "F6", Scopes: []string{"containers"}, Command: ok, Dangerous: true},
	})
	runOnce(a, step(a, key("x")))
	if !strings.HasPrefix(read(t, okOut), "over") {
		t.Errorf("an overriding plugin lost x to the view: %q", read(t, okOut))
	}
	step(a, tea.KeyPressMsg{Code: tea.KeyF5})
	if !a.confirm.Active() || !strings.Contains(render(a), "run ask?") {
		t.Fatalf("confirm did not ask:\n%s", render(a))
	}
	runOnce(a, step(a, key("y")))
	if !strings.HasPrefix(read(t, okOut), "asked") {
		t.Errorf("confirmed plugin did not run: %q", read(t, okOut))
	}
	if cmd := step(a, tea.KeyPressMsg{Code: tea.KeyF6}); cmd != nil || !strings.Contains(a.errFlash, "dangerous") {
		t.Errorf("a dangerous plugin in readonly: err %q", a.errFlash)
	}
	step(a, views.ContainersRefreshMsg{})
	if cmd := step(a, key("x")); cmd != nil || !strings.Contains(a.errFlash, "nothing selected") {
		t.Errorf("no row: err %q", a.errFlash)
	}
	if a.view != style.ViewContainers {
		t.Errorf("view moved to %v", a.view)
	}
}
