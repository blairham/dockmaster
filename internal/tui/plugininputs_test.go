// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

func no() *bool { b := false; return &b }

// inputPlugin is a containers plugin with inputs, other fields from base.
func inputPlugin(base config.Plugin, inputs ...config.PluginInput) map[string]config.Plugin {
	if base.ShortCut == "" {
		base.ShortCut = "F5"
	}
	if base.Command == "" {
		base.Command = "true"
	}
	base.Scopes = []string{"containers"}
	base.Inputs = inputs
	return map[string]config.Plugin{"p": base}
}

func TestPluginInputsAndPipesValidation(t *testing.T) {
	for _, tc := range []struct {
		entries map[string]config.Plugin
		want    string
	}{
		{entries: inputPlugin(config.Plugin{Pipes: []string{"less"}}), want: `pipe "less": k9s skips a pipe of fewer than two words`},
		{entries: inputPlugin(config.Plugin{Pipes: []string{"grep -i x", "  "}}), want: `pipe "  ": k9s skips`},
		{entries: inputPlugin(config.Plugin{Pipes: []string{`grep -e "open`}}), want: `pipe "grep -e \"open": EOF found when expecting closing quote`},
		{
			entries: inputPlugin(config.Plugin{Pipes: []string{"sort -r"}, Background: true}),
			want:    "background and pipes together",
		},
		{entries: inputPlugin(config.Plugin{}, config.PluginInput{}), want: `input 1: name "" must be letters`},
		{entries: inputPlugin(config.Plugin{}, config.PluginInput{Name: "a-b"}), want: `name "a-b" must be`},
		{entries: inputPlugin(config.Plugin{}, config.PluginInput{Name: "x"}, config.PluginInput{Name: "9x"}), want: `input 2: name "9x"`},
		{entries: inputPlugin(config.Plugin{}, config.PluginInput{Name: "has space"}), want: `name "has space"`},
		{
			entries: inputPlugin(config.Plugin{}, config.PluginInput{Name: "env"}, config.PluginInput{Name: "ENV"}),
			want:    `inputs "env" and "ENV" are both $INPUT_ENV`,
		},
		{
			entries: inputPlugin(config.Plugin{}, config.PluginInput{Name: "a"}, config.PluginInput{Name: "a"}),
			want:    `inputs "a" and "a" are both $INPUT_A`,
		},
		{entries: inputPlugin(config.Plugin{}, config.PluginInput{Name: "a", Type: "boolean"}), want: `type "boolean" is not string, number, bool or dropdown`},
		{entries: inputPlugin(config.Plugin{}, config.PluginInput{Name: "a", Type: "dropdown"}), want: `input "a": a dropdown needs options`},
		{
			entries: inputPlugin(config.Plugin{}, config.PluginInput{Name: "a", Type: "dropdown", Options: []string{"x"}, Default: "y"}),
			want:    `input "a": default "y" is not one of its options`,
		},
		{entries: inputPlugin(config.Plugin{}, config.PluginInput{Name: "a", Type: "bool", Default: "yes"}), want: `a bool's default is true or false, not "yes"`},
		{entries: inputPlugin(config.Plugin{}, config.PluginInput{Name: "a", Type: "number", Default: "ten"}), want: `default "ten" is not a number`},
	} {
		if _, err := Plugins(tc.entries, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err %v, want %q", tc.entries["p"], err, tc.want)
		}
	}

	ps, err := Plugins(inputPlugin(config.Plugin{Pipes: []string{`grep -i 'a b'`, "sort -r"}},
		config.PluginInput{Name: "text"},
		config.PluginInput{Name: "n", Label: "Count", Type: "number", Default: "1.5"},
		config.PluginInput{Name: "n2", Type: "number", Default: "-3e2"},
		config.PluginInput{Name: "b", Type: "bool", Default: "true"},
		config.PluginInput{Name: "b2", Type: "bool", Default: "false"},
		config.PluginInput{Name: "d", Type: "dropdown", Options: []string{"x", "y"}, Default: "y"},
		config.PluginInput{Name: "_d2", Type: "dropdown", Options: []string{"x"}},
		config.PluginInput{Name: "s", Type: "string", Default: "anything at all", Required: true},
	), nil)
	if err != nil {
		t.Fatalf("good inputs and pipes: %v", err)
	}
	p := ps[0]
	if want := [][]string{{"grep", "-i", "a b"}, {"sort", "-r"}}; !reflect.DeepEqual(p.Pipes, want) {
		t.Errorf("pipes split to %q, want %q", p.Pipes, want)
	}
	if p.Inputs[0].Type != views.InputString || p.Inputs[0].Label != "text" || p.Inputs[1].Label != "Count" {
		t.Errorf("an untyped input is a string, labeled by its name: %+v", p.Inputs[:2])
	}
	if !p.Inputs[7].Required || p.Inputs[5].Default != "y" || len(p.Inputs[5].Options) != 2 {
		t.Errorf("inputs lost a field: %+v", p.Inputs)
	}
	if !p.Confirm {
		t.Error("a plugin with inputs confirms unless told not to (k9s's ShouldConfirm)")
	}
	for _, tc := range []struct {
		confirm *bool
		inputs  []config.PluginInput
		want    bool
	}{
		{confirm: nil, inputs: nil, want: false},
		{confirm: yes(), inputs: nil, want: true},
		{confirm: no(), inputs: []config.PluginInput{{Name: "a"}}, want: false},
		{confirm: nil, inputs: []config.PluginInput{{Name: "a"}}, want: true},
	} {
		ps, err := Plugins(inputPlugin(config.Plugin{Confirm: tc.confirm}, tc.inputs...), nil)
		if err != nil || ps[0].Confirm != tc.want {
			t.Errorf("confirm %v with %d inputs: got %v, want %v (%v)", tc.confirm, len(tc.inputs), ps[0].Confirm, tc.want, err)
		}
	}
}

// envRecorder is a script that writes its argv, then every INPUT_
// variable, one per line, to a file.
func envRecorder(t *testing.T) (script, out string) {
	t.Helper()
	dir := t.TempDir()
	script, out = filepath.Join(dir, "rec"), filepath.Join(dir, "out")
	body := "#!/bin/sh\nfor a in \"$@\"; do printf '[%s]' \"$a\"; done > '" + out + "'\necho >> '" + out + "'\n" +
		"env | grep '^INPUT_' | sort >> '" + out + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}
	return script, out
}

// deployInputs is the form the form tests drive: a required string, a
// number with a default, a bool, a dropdown with a default and one
// without.
func deployInputs() []config.PluginInput {
	return []config.PluginInput{
		{Name: "tag", Label: "Image tag", Required: true},
		{Name: "replicas", Type: "number", Default: "3"},
		{Name: "verbose", Type: "bool"},
		{Name: "env", Type: "dropdown", Options: []string{"dev", "stage", "prod"}, Default: "prod"},
		{Name: "region", Type: "dropdown", Options: []string{"eu", "us"}},
		{Name: "dry", Type: "bool", Default: "true"},
	}
}

// TestPluginInputsForm: the form opens prefilled with defaults, refuses a
// missing required value and a number that is not one, takes y/n on a bool
// and cycles a dropdown, and on submit the values reach the plugin's args
// and environment — after the confirm k9s asks by default.
func TestPluginInputsForm(t *testing.T) {
	rec, out := envRecorder(t)
	a := pluginApp(t, Options{}, inputPlugin(config.Plugin{
		Command: rec, Description: "Deploy",
		Args: []string{"$NAME", "--tag=$INPUT_TAG", "$INPUT_REPLICAS", "$INPUT_VERBOSE", "$INPUT_ENV", "${INPUT_REGION}"},
	}, deployInputs()...))

	if cmd := step(a, tea.KeyPressMsg{Code: tea.KeyF5}); cmd != nil {
		runOnce(a, cmd)
	}
	if a.view != style.ViewPluginForm {
		t.Fatalf("F5 did not open the inputs form: view %v", a.view)
	}
	frame := render(a)
	t.Logf("the form:\n%s", frame)
	for _, want := range []string{
		"plugin Deploy", "Image tag", "required", "replicas", "[3", "verbose", "‹ false ›",
		"env", "‹ prod ›", "region", "‹  ›", "enter runs it", "<enter>", "Run",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("form is missing %q:\n%s", want, frame)
		}
	}

	step(a, tab) // away from the missing field: the refusal brings focus back
	runOnce(a, step(a, key("enter")))
	if a.view != style.ViewPluginForm || !strings.Contains(render(a), "Image tag is required") {
		t.Fatalf("a missing required value was not refused:\n%s", render(a))
	}
	if read(t, out) != "" {
		t.Fatal("the plugin ran from an incomplete form")
	}
	typeText(a, "v1 $NAME") // a value is substituted once, never re-expanded
	step(a, tab)
	clearField(a, 40)
	typeText(a, "three")
	runOnce(a, step(a, key("enter")))
	if !strings.Contains(render(a), `replicas: "three" is not a number`) || a.confirm.Active() {
		t.Fatalf("a bad number was not refused:\n%s", render(a))
	}
	clearField(a, 40)
	typeText(a, "2.5")
	step(a, tab)
	step(a, key("y"))
	step(a, tab)
	step(a, tea.KeyPressMsg{Code: tea.KeyRight}) // prod → dev, wrapping
	step(a, tab)
	step(a, key("space")) // "" → eu
	step(a, key("space")) // eu → us
	if f := typedView[*views.PluginFormView](a, style.ViewPluginForm); f != nil {
		want := map[string]string{
			"tag": "v1 $NAME", "replicas": "2.5", "verbose": "true", "env": "dev", "region": "us", "dry": "true",
		}
		if got := f.Values(); !reflect.DeepEqual(got, want) {
			t.Errorf("form values %v, want %v", got, want)
		}
	}

	runOnce(a, step(a, key("enter")))
	if a.view != style.ViewContainers {
		t.Errorf("the form did not close on submit: view %v", a.view)
	}
	// The prompt, not the frame: the dialog wraps it, and where depends on
	// how long the recorder's temp path is.
	if p := a.confirm.Prompt(); !a.confirm.Active() || !strings.HasPrefix(p, "run Deploy?") ||
		!strings.Contains(p, "--tag=v1 $NAME 2.5 true dev us") {
		t.Fatalf("no confirm with the expanded command: %q", p)
	}
	runOnce(a, step(a, key("y")))
	want := "[web][--tag=v1 $NAME][2.5][true][dev][us]\n" +
		"INPUT_DRY=true\nINPUT_ENV=dev\nINPUT_REGION=us\nINPUT_REPLICAS=2.5\nINPUT_TAG=v1 $NAME\nINPUT_VERBOSE=true\n"
	if got := read(t, out); got != want {
		t.Errorf("plugin saw\n%s\nwant\n%s", got, want)
	}
}

// TestPluginInputsCancel: esc on the form, and no on its confirm, run
// nothing; a second press opens a fresh form.
func TestPluginInputsCancel(t *testing.T) {
	rec, out := envRecorder(t)
	a := pluginApp(t, Options{}, inputPlugin(config.Plugin{Command: rec},
		config.PluginInput{Name: "tag", Default: "latest"}))
	step(a, tea.KeyPressMsg{Code: tea.KeyF5})
	typeText(a, "-typed")
	step(a, key("esc"))
	if a.view != style.ViewContainers || a.confirm.Active() || read(t, out) != "" {
		t.Fatalf("esc: view %v, confirm %v, ran %q", a.view, a.confirm.Active(), read(t, out))
	}
	step(a, tea.KeyPressMsg{Code: tea.KeyF5})
	if !strings.Contains(render(a), "[latest") || strings.Contains(render(a), "typed") {
		t.Fatalf("the form did not open fresh:\n%s", render(a))
	}
	runOnce(a, step(a, key("enter")))
	runOnce(a, step(a, key("n")))
	if read(t, out) != "" || a.confirm.Active() {
		t.Errorf("no at the confirm ran it: %q", read(t, out))
	}
	// A submission for a plugin other than the one waiting runs nothing.
	step(a, tea.KeyPressMsg{Code: tea.KeyF5})
	if cmd := a.submitPluginInputs(`{"plugin":"other","values":{"tag":"x"}}`); cmd != nil || read(t, out) != "" ||
		!strings.Contains(a.errFlash, "plugin other is no longer waiting") || a.view != style.ViewContainers {
		t.Errorf("a mismatched submission: flash %q, ran %q, view %v", a.errFlash, read(t, out), a.view)
	}
	// Nor does one when no plugin is waiting at all.
	if cmd := a.submitPluginInputs(`{"plugin":"p","values":{"tag":"x"}}`); cmd != nil || read(t, out) != "" ||
		!strings.Contains(a.errFlash, "no longer waiting") {
		t.Errorf("a stale submission: flash %q, ran %q", a.errFlash, read(t, out))
	}
}

// TestPluginInputsWithoutConfirm: confirm: false runs straight from the
// form, with the row the plugin was started on — not the form's.
func TestPluginInputsWithoutConfirm(t *testing.T) {
	rec, out := envRecorder(t)
	a := pluginApp(t, Options{}, inputPlugin(config.Plugin{
		Command: rec, Confirm: no(), Args: []string{"$NAME", "$INPUT_TAG"},
	}, config.PluginInput{Name: "tag", Default: "latest"}))
	step(a, tea.KeyPressMsg{Code: tea.KeyF5})
	runOnce(a, step(a, key("enter")))
	if a.confirm.Active() || read(t, out) != "[web][latest]\nINPUT_TAG=latest\n" {
		t.Errorf("confirm %v, ran %q", a.confirm.Active(), read(t, out))
	}
}

// TestPluginInputsReadonly: readonly refuses a dangerous plugin before its
// form opens, and leaves any other one alone — as without inputs.
func TestPluginInputsReadonly(t *testing.T) {
	rec, out := envRecorder(t)
	entries := inputPlugin(config.Plugin{Command: rec, Dangerous: true, Confirm: no()},
		config.PluginInput{Name: "tag", Default: "x"})
	entries["safe"] = config.Plugin{
		ShortCut: "F6", Scopes: []string{"containers"}, Command: rec, Confirm: no(),
		Inputs: []config.PluginInput{{Name: "tag", Default: "y"}},
	}
	a := pluginApp(t, Options{ReadOnly: true}, entries)
	if cmd := step(a, tea.KeyPressMsg{Code: tea.KeyF5}); cmd != nil || a.view != style.ViewContainers ||
		!strings.Contains(a.errFlash, "dangerous") {
		t.Fatalf("readonly opened a dangerous plugin's form: view %v, flash %q", a.view, a.errFlash)
	}
	step(a, tea.KeyPressMsg{Code: tea.KeyF6})
	runOnce(a, step(a, key("enter")))
	if read(t, out) != "\nINPUT_TAG=y\n" {
		t.Errorf("readonly refused a plain plugin with inputs: %q", read(t, out))
	}
}

// fakeBin writes an executable script into dir.
func fakeBin(t *testing.T, dir, name, body string) {
	t.Helper()
	path, script := filepath.Join(dir, name), []byte("#!/bin/sh\n"+body)
	if err := os.WriteFile(path, script, 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}
}

// TestPluginPipesRunEndToEnd: command | pipe | pipe runs, with the last
// stdout and everyone's stderr on the terminal. Args are expanded, pipes
// run as written (k9s), every command has the plugin's environment, and a
// value full of shell syntax reaches its command as one plain argument.
func TestPluginPipesRunEndToEnd(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pwned")
	// gen prints each argument on its own line; tagger prefixes each line
	// it reads with its first argument and says on stderr what $NAME is.
	fakeBin(t, bin, "gen", `for a in "$@"; do printf '%s\n' "$a"; done`+"\n")
	tagger := `while IFS= read -r l; do printf '%s:%s\n' "$1" "$l"; done` + "\n" + `echo "tagger NAME=$NAME" >&2` + "\n"
	fakeBin(t, bin, "tagger", tagger)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	a := pluginApp(t, Options{}, inputPlugin(config.Plugin{
		Command: "gen", Confirm: no(),
		Args:  []string{"a", "$NAME", "$INPUT_Q"},
		Pipes: []string{"sort -r", "tagger $NAME"},
	}, config.PluginInput{Name: "q"}))
	var stdout, stderr bytes.Buffer
	var ran tea.ExecCommand
	a.execPipelineFn = func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd {
		ran = c
		c.SetStdin(strings.NewReader(""))
		c.SetStdout(&stdout)
		c.SetStderr(&stderr)
		return func() tea.Msg { return fn(c.Run()) }
	}
	step(a, tea.KeyPressMsg{Code: tea.KeyF5})
	typeText(a, "$(touch "+marker+"); `touch "+marker+"` | x")
	runOnce(a, step(a, key("enter")))
	if ran == nil {
		t.Fatalf("no pipeline ran; flash %q", a.errFlash)
	}
	want := "$NAME:web\n$NAME:a\n$NAME:$(touch " + marker + "); `touch " + marker + "` | x\n"
	if stdout.String() != want {
		t.Errorf("pipeline output\n%q\nwant\n%q", stdout.String(), want)
	}
	if stderr.String() != "tagger NAME=web\n" {
		t.Errorf("pipeline stderr %q", stderr.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a shell parsed an input value")
	}
	if a.errFlash != "" {
		t.Errorf("a clean pipeline flashed %q", a.errFlash)
	}
}

// TestPluginPipeConfirmAndFailures: the confirm shows the whole pipeline;
// a pipe not on PATH is reported, and the last command's exit is the
// pipeline's.
func TestPluginPipeConfirmAndFailures(t *testing.T) {
	bin := t.TempDir()
	fakeBin(t, bin, "gen", "echo x\n")
	fakeBin(t, bin, "failing", "cat >/dev/null; exit 4\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	a := pluginApp(t, Options{}, map[string]config.Plugin{
		"ask":     {ShortCut: "F5", Scopes: []string{"all"}, Command: "gen", Pipes: []string{"sort -r"}, Confirm: yes()},
		"missing": {ShortCut: "F6", Scopes: []string{"all"}, Command: "gen", Pipes: []string{"no-such-pipe-bin -x"}},
		"fails":   {ShortCut: "F7", Scopes: []string{"all"}, Command: "gen", Pipes: []string{"failing now"}},
	})
	a.execPipelineFn = func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd {
		c.SetStdout(io.Discard)
		c.SetStderr(io.Discard)
		return func() tea.Msg { return fn(c.Run()) }
	}
	step(a, tea.KeyPressMsg{Code: tea.KeyF5})
	if !strings.Contains(render(a), "run ask? gen | sort -r") {
		t.Errorf("confirm does not show the pipeline:\n%s", render(a))
	}
	step(a, key("n"))
	cmd := step(a, tea.KeyPressMsg{Code: tea.KeyF6})
	if cmd != nil || !strings.Contains(a.errFlash, "pipe no-such-pipe-bin is not on PATH") {
		t.Errorf("a missing pipe: flash %q", a.errFlash)
	}
	a.errFlash = ""
	runOnce(a, step(a, tea.KeyPressMsg{Code: tea.KeyF7}))
	if !strings.Contains(a.errFlash, "plugin fails exited 4") {
		t.Errorf("a failing last pipe: flash %q", a.errFlash)
	}
}

func shCmd(script string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", script) //nolint:gosec // fixed test scripts
}

// TestPipeline drives the pipeline type directly: data flows through every
// stage, only the last command's exit counts, an upstream that never ends
// is stopped by its reader leaving, and a command that cannot start stops
// the ones already running.
func TestPipeline(t *testing.T) {
	run := func(cmds ...*exec.Cmd) (string, error) {
		var out bytes.Buffer
		p := &pipeline{cmds: cmds}
		p.SetStdin(strings.NewReader("in\n"))
		p.SetStdout(&out)
		p.SetStderr(io.Discard)
		done := make(chan error, 1)
		go func() { done <- p.Run() }()
		select {
		case err := <-done:
			return out.String(), err
		case <-time.After(20 * time.Second):
			t.Fatal("pipeline hung")
			return "", nil
		}
	}
	out, err := run(shCmd("cat; echo b; echo a"), shCmd("sort"), shCmd("tr a-z A-Z"))
	if err != nil || out != "A\nB\nIN\n" {
		t.Errorf("three stages: %q %v", out, err)
	}
	if out, err = run(shCmd("echo x; exit 3"), shCmd("cat")); err != nil || out != "x\n" {
		t.Errorf("an upstream failure is not the pipeline's: %q %v", out, err)
	}
	var exitErr *exec.ExitError
	_, err = run(shCmd("echo x"), shCmd("cat >/dev/null; exit 5"))
	if !asExitError(err, &exitErr) || exitErr.ExitCode() != 5 {
		t.Errorf("the last command's exit: %v", err)
	}
	if out, err = run(exec.Command("yes"), shCmd("head -n 2")); err != nil || out != "y\ny\n" { //nolint:gosec // fixed
		t.Errorf("yes | head: %q %v", out, err)
	}
	absent := exec.Command(filepath.Join(t.TempDir(), "absent")) //nolint:gosec // a fixed test path
	if _, err = run(exec.Command("sleep", "30"), absent); err == nil {
		t.Error("a command that cannot start was not reported")
	}
	if _, err = run(); err == nil {
		t.Error("an empty pipeline ran")
	}
}
