// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/theme"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// redLogo is a context skin's color, distinct from every default one.
var redLogo = lipgloss.Color("#FE0102")

// redSGR is how redLogo appears in a rendered frame.
const redSGR = "254;1;2"

// redTheme is the default theme with the logo — the header art, the
// command prompt and style.Logo — in redLogo.
func redTheme() *theme.Theme {
	t := theme.Default()
	t.Logo = redLogo
	t.Rebuild()
	return &t
}

func boolp(b bool) *bool { return &b }

// ctxClient is a client for context name on a socket nothing listens on;
// nothing here dials it.
func ctxClient(t *testing.T, name string) *docker.Client {
	t.Helper()
	c, err := docker.New("unix:///nonexistent/" + name + ".sock")
	if err != nil {
		t.Fatal(err)
	}
	c.ContextName = name
	return c
}

// ctxApp starts an app on context name with opts, restoring the style base
// the app may change when the test ends.
func ctxApp(t *testing.T, name string, opts Options) *App {
	t.Helper()
	base := style.Base()
	t.Cleanup(func() { style.SetBase(base) })
	opts.Version = "test"
	a := NewApp(ctxClient(t, name), opts)
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 44})
	return a
}

// switchTo is a :ctx switch to name landing as a dial would.
func switchTo(t *testing.T, a *App, name string) {
	t.Helper()
	a.applySwitchContext(switchContextMsg{name: name, client: ctxClient(t, name)})
}

func isRed() bool {
	return style.Base().Logo == redLogo && style.Logo.GetForeground() == redLogo
}

// TestReadOnlyPrecedence: --readonly forces read-only on every context;
// otherwise a context's readOnly, when set, replaces the top-level one; a
// context with no entry, or an entry without readOnly, follows the top
// level. Checked at startup on the context and after a switch onto it.
func TestReadOnlyPrecedence(t *testing.T) {
	for _, tc := range []struct {
		ctx         *ContextSettings
		name        string
		force       bool
		global      bool
		want        bool
		fromContext bool
	}{
		{name: "no entry, global off", want: false},
		{name: "no entry, global on", global: true, want: true},
		{name: "entry without readOnly follows global on", ctx: &ContextSettings{Theme: redTheme()}, global: true, want: true},
		{name: "entry without readOnly follows global off", ctx: &ContextSettings{Theme: redTheme()}, want: false},
		{name: "context on beats global off", ctx: &ContextSettings{ReadOnly: boolp(true)}, want: true, fromContext: true},
		{name: "context off with global off", ctx: &ContextSettings{ReadOnly: boolp(false)}, want: false},
		{name: "--readonly alone beats context on", ctx: &ContextSettings{ReadOnly: boolp(true)}, force: true, want: true},
		{name: "context on with global on", ctx: &ContextSettings{ReadOnly: boolp(true)}, global: true, want: true},
		{name: "context on under --readonly", ctx: &ContextSettings{ReadOnly: boolp(true)}, force: true, global: true, want: true},
		{name: "context off beats global on", ctx: &ContextSettings{ReadOnly: boolp(false)}, global: true, want: false},
		{name: "--readonly beats context off", ctx: &ContextSettings{ReadOnly: boolp(false)}, force: true, global: true, want: true},
		{name: "--readonly with no entry", force: true, global: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{ReadOnly: tc.global, ForceReadOnly: tc.force, Contexts: map[string]ContextSettings{}}
			if tc.ctx != nil {
				opts.Contexts["prod"] = *tc.ctx
			}
			if a := ctxApp(t, "prod", opts); a.readonly != tc.want {
				t.Errorf("starting on prod: readonly %v, want %v", a.readonly, tc.want)
			}
			a := ctxApp(t, "elsewhere", opts)
			switchTo(t, a, "prod")
			if a.readonly != tc.want {
				t.Errorf("switched to prod: readonly %v, want %v", a.readonly, tc.want)
			}
			// The switch credits prod only when prod's own readOnly is what
			// turned the session read-only.
			if credited := strings.Contains(a.flash, "contexts: setting"); credited != tc.fromContext {
				t.Errorf("switch flash %q credits the context: %v, want %v", a.flash, credited, tc.fromContext)
			}
		})
	}
}

// TestLeavingAContextRestoresTheGlobals: from a context with a skin and
// readOnly to one with no entry, the top-level skin and readOnly are back
// — and going back puts the context's on again.
func TestLeavingAContextRestoresTheGlobals(t *testing.T) {
	a := ctxApp(t, "prod", Options{Contexts: map[string]ContextSettings{
		"prod": {Theme: redTheme(), ReadOnly: boolp(true)},
	}})
	if !a.readonly || !isRed() {
		t.Fatalf("starting on prod: readonly %v, red %v", a.readonly, isRed())
	}
	if !strings.Contains(renderStyled(a), redSGR) {
		t.Error("the frame was not drawn in prod's skin")
	}

	switchTo(t, a, "dev")
	if a.readonly || isRed() || style.Base().Logo != theme.Default().Logo {
		t.Errorf("on dev, which has no entry: readonly %v, logo %v", a.readonly, style.Base().Logo)
	}
	if strings.Contains(renderStyled(a), redSGR) {
		t.Error("the frame on dev still carries prod's skin")
	}
	if strings.Contains(a.flash, "readonly") {
		t.Errorf("the switch to dev, which is not read-only, says it is: %q", a.flash)
	}

	switchTo(t, a, "prod")
	if !a.readonly || !isRed() {
		t.Errorf("back on prod: readonly %v, red %v", a.readonly, isRed())
	}
	if !strings.Contains(a.flash, "readonly") {
		t.Errorf("the switch does not say prod made the session read-only: %q", a.flash)
	}
}

// TestSwitchReskinsTheChrome: the switch rebuilds the frame and bars on
// the context's theme, not just the package styles — the command bar's
// prompt is drawn in the logo color.
func TestSwitchReskinsTheChrome(t *testing.T) {
	a := ctxApp(t, "dev", Options{Contexts: map[string]ContextSettings{"prod": {Theme: redTheme()}}})
	step(a, key(":"))
	if strings.Contains(renderStyled(a), redSGR) {
		t.Fatal("red before the switch")
	}
	step(a, key("esc"))
	switchTo(t, a, "prod")
	step(a, key(":"))
	if !strings.Contains(renderStyled(a), redSGR) {
		t.Error("the command bar was not rebuilt on prod's skin")
	}
}

// TestReadonlyToggleLastsUntilTheNextSwitch: :readonly overrides the
// context's setting for the session — until a switch, which applies the
// context's again.
func TestReadonlyToggleLastsUntilTheNextSwitch(t *testing.T) {
	a := ctxApp(t, "prod", Options{Contexts: map[string]ContextSettings{"prod": {ReadOnly: boolp(true)}}})
	a.dispatchCommand("readonly")
	if a.readonly {
		t.Fatal(":readonly did not turn it off")
	}
	step(a, tickMsg{})
	if a.readonly {
		t.Error("the toggle did not last")
	}
	switchTo(t, a, "prod")
	if !a.readonly {
		t.Error("a switch did not apply the context's readOnly again")
	}

	a = ctxApp(t, "dev", Options{Contexts: map[string]ContextSettings{"prod": {ReadOnly: boolp(true)}}})
	a.dispatchCommand("readonly")
	switchTo(t, a, "staging")
	if a.readonly {
		t.Error("the toggle outlived the switch to a context with no entry")
	}
}

// TestForcedReadOnlyRefusesAfterSwitch: with --readonly, a stop on a
// context whose readOnly is false is still refused.
func TestForcedReadOnlyRefusesAfterSwitch(t *testing.T) {
	a := ctxApp(t, "prod", Options{
		ReadOnly: true, ForceReadOnly: true,
		Contexts: map[string]ContextSettings{"dev": {ReadOnly: boolp(false)}},
	})
	switchTo(t, a, "dev")
	loadContainers(a)
	step(a, key("x"))
	if !strings.Contains(a.errFlash, "readonly mode — stop refused") || a.confirm.Active() {
		t.Errorf("stop on dev under --readonly: flash %q", a.errFlash)
	}
}

// TestReloadKeepsTheContextSkin: a reload on a context with its own skin
// keeps it, takes the new top-level skin for the next switch, and applies a
// changed context readOnly.
func TestReloadKeepsTheContextSkin(t *testing.T) {
	blue := theme.Default()
	blue.Logo = lipgloss.Color("#0102FE")
	blue.Rebuild()
	prod := map[string]ContextSettings{"prod": {Theme: redTheme(), ReadOnly: boolp(true)}}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "config.yaml"), "dockmaster: {}\n")
	a := ctxApp(t, "prod", Options{
		Contexts: prod, WatchDir: dir,
		Reload: func() (Reloaded, error) {
			return Reloaded{Theme: blue, Options: Options{Contexts: map[string]ContextSettings{
				"prod": {Theme: redTheme(), ReadOnly: boolp(false)},
			}}}, nil
		},
	})
	write(t, filepath.Join(dir, "config.yaml"), "dockmaster: {ui: {skin: blue}}\n")
	reloadNow(t, a)
	if !isRed() {
		t.Errorf("the reload replaced prod's skin with the top-level one: %v", style.Base().Logo)
	}
	if a.readonly {
		t.Error("the reload did not apply prod's changed readOnly")
	}
	switchTo(t, a, "dev")
	if style.Base().Logo != blue.Logo {
		t.Errorf("leaving prod after the reload: logo %v, want the reloaded top-level skin", style.Base().Logo)
	}
}

// TestReloadLeavesTheToggleAlone: a reload that does not change what
// readOnly comes to on this context leaves a :readonly toggle as it is.
func TestReloadLeavesTheToggleAlone(t *testing.T) {
	prod := map[string]ContextSettings{"prod": {ReadOnly: boolp(true)}}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "config.yaml"), "dockmaster: {}\n")
	a := ctxApp(t, "prod", Options{
		Contexts: prod, WatchDir: dir,
		Reload: func() (Reloaded, error) {
			// The top-level readOnly changed; prod's, which decides, did not.
			return Reloaded{Theme: theme.Default(), Options: Options{ReadOnly: true, Contexts: prod}}, nil
		},
	})
	a.dispatchCommand("readonly")
	write(t, filepath.Join(dir, "config.yaml"), "dockmaster: {readOnly: true}\n")
	reloadNow(t, a)
	if a.readonly {
		t.Error("a reload that left prod's readOnly alone undid the toggle")
	}
}

// stateFile is a contexts.json in a fresh temporary state directory.
func stateFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state", "contexts.json")
}

func readState(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st contextState
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	return st.LastView
}

// TestLastViewPerContext: the top-level view is remembered per context —
// not a drill-in — written to the state file, restored by a :ctx switch,
// and by a new app started on the context with the same file. config.yaml
// is never written.
func TestLastViewPerContext(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("DOCKMASTER_CONFIG_DIR", cfgDir)
	path := stateFile(t)
	a := ctxApp(t, "prod", Options{ContextStateFile: path})
	a.switchView(style.ViewImages)
	a.pushView(style.ViewInspect) // a drill-in is not a view switched to
	switchTo(t, a, "dev")
	if a.view != style.ViewContainers {
		t.Errorf("dev, never visited, opened on %s", style.ViewName(a.view))
	}
	a.switchView(style.ViewVolumes)

	want := map[string]string{"prod": "images", "dev": "volumes"}
	if got := readState(t, path); len(got) != 2 || got["prod"] != want["prod"] || got["dev"] != want["dev"] {
		t.Errorf("state file %v, want %v", got, want)
	}

	switchTo(t, a, "prod")
	if a.view != style.ViewImages {
		t.Errorf(":ctx prod opened %s, want its last view, images", style.ViewName(a.view))
	}

	b := ctxApp(t, "dev", Options{ContextStateFile: path})
	if b.view != style.ViewVolumes {
		t.Errorf("a new app on dev opened %s, want volumes", style.ViewName(b.view))
	}

	entries, err := os.ReadDir(cfgDir)
	if err != nil || len(entries) != 0 {
		t.Errorf("the config directory was written to: %v %v", entries, err)
	}
}

// TestLastViewHistoryKeys: going back with [ is a top-level view change too.
func TestLastViewHistoryKeys(t *testing.T) {
	path := stateFile(t)
	a := ctxApp(t, "prod", Options{ContextStateFile: path})
	a.switchView(style.ViewImages)
	a.switchView(style.ViewNetworks)
	step(a, key("["))
	if a.view != style.ViewImages || readState(t, path)["prod"] != "images" {
		t.Errorf("after [: view %s, saved %q", style.ViewName(a.view), readState(t, path)["prod"])
	}
}

// TestStartingCommand: -c beats the context's defaultView, which beats
// config.yaml's defaultView, which beats the view last open on the context.
func TestStartingCommand(t *testing.T) {
	last := map[string]string{"prod": "volumes"}
	ctx := map[string]ContextSettings{"prod": {DefaultView: "networks"}}
	for _, tc := range []struct {
		name string
		want string
		opts Options
	}{
		{name: "remembered", want: "volumes"},
		{name: "config defaultView", opts: Options{Command: "images"}, want: "images"},
		{name: "context defaultView", opts: Options{Command: "images", Contexts: ctx}, want: "networks"},
		{name: "-c", opts: Options{Command: "events", CommandFromFlag: true, Contexts: ctx}, want: "events"},
		{name: "-c empty falls through", opts: Options{CommandFromFlag: true}, want: "volumes"},
	} {
		if got := startingCommand(tc.opts, "prod", last); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := startingCommand(Options{Contexts: ctx}, "dev", last); got != "" {
		t.Errorf("another context's settings applied to dev: %q", got)
	}
}

// TestExplicitLandingBeatsLastView: -c at startup, @context and a
// context's defaultView on a switch all win over the remembered view.
func TestExplicitLandingBeatsLastView(t *testing.T) {
	path := stateFile(t)
	if err := writeLastView(path, "prod", "volumes"); err != nil {
		t.Fatal(err)
	}
	if a := ctxApp(t, "prod", Options{ContextStateFile: path}); a.view != style.ViewVolumes {
		t.Fatalf("no restore to test against: %s", style.ViewName(a.view))
	}
	if a := ctxApp(
		t,
		"prod",
		Options{ContextStateFile: path, Command: "images", CommandFromFlag: true},
	); a.view != style.ViewImages {
		t.Errorf("-c images opened %s", style.ViewName(a.view))
	}

	a := ctxApp(t, "dev", Options{ContextStateFile: path})
	a.applySwitchContext(
		switchContextMsg{name: "prod", client: ctxClient(t, "prod"), land: &landing{view: style.ViewNetworks}},
	)
	if a.view != style.ViewNetworks {
		t.Errorf("networks @prod opened %s", style.ViewName(a.view))
	}

	a = ctxApp(
		t,
		"dev",
		Options{ContextStateFile: path, Contexts: map[string]ContextSettings{"prod": {DefaultView: "events"}}},
	)
	switchTo(t, a, "prod")
	if a.view != style.ViewEvents {
		t.Errorf("prod's defaultView events: opened %s", style.ViewName(a.view))
	}
}

// TestContextDefaultViewCommand: a defaultView that is a command rather
// than a view runs after the switch.
func TestContextDefaultViewCommand(t *testing.T) {
	a := ctxApp(t, "dev", Options{Contexts: map[string]ContextSettings{"prod": {DefaultView: "images /nginx"}}})
	switchTo(t, a, "prod")
	if a.view != style.ViewImages || a.filter != "nginx" {
		t.Errorf("opened %s filtered %q", style.ViewName(a.view), a.filter)
	}
}

// TestRuntimesReconnectKeepsTheView: a reconnect from the runtimes view
// applies the context's settings and stays on runtimes.
func TestRuntimesReconnectKeepsTheView(t *testing.T) {
	path := stateFile(t)
	if err := writeLastView(path, "prod", "volumes"); err != nil {
		t.Fatal(err)
	}
	a := ctxApp(t, "dev", Options{ContextStateFile: path, Contexts: map[string]ContextSettings{
		"prod": {Theme: redTheme(), ReadOnly: boolp(true)},
	}})
	a.switchView(style.ViewRuntimes)
	a.applySwitchContext(switchContextMsg{name: "prod", client: ctxClient(t, "prod"), keepView: true})
	if a.view != style.ViewRuntimes || !a.readonly || !isRed() {
		t.Errorf("reconnect: view %s readonly %v red %v", style.ViewName(a.view), a.readonly, isRed())
	}
}

// TestContextStateWriteFailureIsLogged: a state file that cannot be
// written is a log line, not a flash.
func TestContextStateWriteFailureIsLogged(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	a := ctxApp(t, "prod", Options{
		ContextStateFile: filepath.Join(blocker, "contexts.json"), // under a file: cannot be created
		Logger:           slog.New(slog.NewTextHandler(&buf, nil)),
	})
	a.switchView(style.ViewImages)
	if a.errFlash != "" {
		t.Errorf("flashed: %q", a.errFlash)
	}
	if !strings.Contains(buf.String(), "context state not saved") {
		t.Errorf("not logged: %q", buf.String())
	}
	if a.lastViews["prod"] != "images" {
		t.Error("the view is not remembered for the session either")
	}
}

// TestContextStateFileTolerance: a missing, corrupt or stale file is no
// reason not to start, a view name it does not know is dropped, and
// writing one context keeps the others.
func TestContextStateFileTolerance(t *testing.T) {
	if got := readContextState(filepath.Join(t.TempDir(), "none.json")); len(got) != 0 {
		t.Errorf("missing file: %v", got)
	}
	path := stateFile(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	write(t, path, "{not json")
	if got := readContextState(path); len(got) != 0 {
		t.Errorf("corrupt file: %v", got)
	}
	write(t, path, `{"lastView": {"prod": "images", "dev": "bogus"}}`)
	if got := readContextState(path); got["prod"] != "images" || len(got) != 1 {
		t.Errorf("a bad view name was kept: %v", got)
	}
	if err := writeLastView(path, "dev", "events"); err != nil {
		t.Fatal(err)
	}
	if got := readState(t, path); got["prod"] != "images" || got["dev"] != "events" {
		t.Errorf("writing dev lost prod: %v", got)
	}
}

// TestValidateContextCommand: a context's defaultView is any -c command,
// but not one that names a context.
func TestValidateContextCommand(t *testing.T) {
	for in, ok := range map[string]bool{
		"images": true, "images /nginx": true, "xray": true, "pg": true,
		"images @prod": false, "pgprod": false, "bogus": false,
	} {
		err := ValidateContextCommand(in, map[string]string{"pg": "containers /postgres", "pgprod": "containers @prod"})
		if (err == nil) != ok {
			t.Errorf("ValidateContextCommand(%q) = %v", in, err)
		}
	}
}
