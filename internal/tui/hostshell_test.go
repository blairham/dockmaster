// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// hostShellArgv is what follows the endpoint flags for image: the helper,
// then nsenter into PID 1, each a separate argument.
func hostShellArgv(image string) []string {
	return []string{
		"run", "--rm", "-it", "--privileged", "--pid=host", "--net=host",
		"--label", "dockmaster.helper=hostshell", image,
		"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", "sh",
	}
}

// fakeCLIs puts each named binary on PATH as a script that prints every
// argument on its own line, as fakeDockerCLI does for docker.
func fakeCLIs(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do printf 'arg:%s\\n' \"$a\"; done\n"
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(script), 0o700); err != nil { //nolint:gosec // test fake
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// capture swaps the terminal hand-over for a recorder.
func capture(a *App) *[]*exec.Cmd {
	var ran []*exec.Cmd
	a.execProcess = func(c *exec.Cmd, _ tea.ExecCallback) tea.Cmd { ran = append(ran, c); return nil }
	return &ran
}

// argsOf runs the captured command against the fakes and returns what the
// binary was given, one argument per element.
func argsOf(t *testing.T, c *exec.Cmd) []string {
	t.Helper()
	out, err := c.Output()
	if err != nil {
		t.Fatalf("running %v: %v\n%s", c.Args, err, out)
	}
	var got []string
	for l := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
		got = append(got, strings.TrimPrefix(l, "arg:"))
	}
	return got
}

func sameArgs(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("%s: the CLI was given\n%q\nwant\n%q", what, got, want)
	}
}

// TestHostShellArgv: :hostshell, confirmed, runs docker with the daemon's
// endpoint flags first, then the privileged helper and nsenter — and an
// image name full of shell metacharacters arrives as one argument (#59).
func TestHostShellArgv(t *testing.T) {
	fakeDockerCLI(t)
	a := newTestApp(t)
	a.client = &docker.Client{Host: "unix:///run/fake.sock"}
	odd := `img;rm -rf / $(id) "q" 'x' ` + "`w`" + ` && echo|cat >f`
	a.hostShellImage = odd
	ran := capture(a)

	a.dispatchCommand("hostshell")
	if !a.confirm.Active() || len(*ran) != 0 {
		t.Fatalf(":hostshell ran before a confirm (err %q)", a.errFlash)
	}
	step(a, key("y"))
	if len(*ran) != 1 {
		t.Fatalf("yes handed over %d commands (err %q)", len(*ran), a.errFlash)
	}
	if base := filepath.Base((*ran)[0].Path); base != "docker" {
		t.Errorf("ran %s, want docker", (*ran)[0].Path)
	}
	sameArgs(
		t,
		":hostshell",
		argsOf(t, (*ran)[0]),
		append([]string{"--host", "unix:///run/fake.sock"}, hostShellArgv(odd)...),
	)
}

// TestHostShellDefaultImage: with no hostShell.image the helper is the
// default, which has nsenter.
func TestHostShellDefaultImage(t *testing.T) {
	fakeDockerCLI(t)
	a := newTestApp(t)
	a.client = &docker.Client{Host: "tcp://10.0.0.5:2376"}
	ran := capture(a)
	a.dispatchCommand("hostshell")
	if !strings.Contains(a.confirm.Prompt(), " "+config.DefaultHostShellImage+" ") {
		t.Errorf("confirm %q does not name %s", a.confirm.Prompt(), config.DefaultHostShellImage)
	}
	step(a, key("y"))
	if len(*ran) != 1 {
		t.Fatalf("nothing ran (err %q)", a.errFlash)
	}
	sameArgs(t, "default image", argsOf(t, (*ran)[0]),
		append([]string{"--host", "tcp://10.0.0.5:2376"}, hostShellArgv("alpine:3")...))
}

// TestHostShellConfirm: the question names the daemon (context and
// endpoint), the image, that it is pulled if missing, and that it is root
// on the host; anything but yes runs nothing.
func TestHostShellConfirm(t *testing.T) {
	fakeDockerCLI(t)
	a := newTestApp(t)
	a.client = &docker.Client{Host: "ssh://admin@build-01", ContextName: "build"}
	a.hostShellImage = "registry.local/tools:1"
	ran := capture(a)

	a.dispatchCommand("hostshell")
	want := "open a ROOT shell on the host of build (ssh://admin@build-01)? a privileged registry.local/tools:1 " +
		"helper (pulled if missing) enters the host's namespaces; every command runs as root there"
	if got := a.confirm.Prompt(); got != want {
		t.Errorf("confirm\n%q\nwant\n%q", got, want)
	}
	for _, no := range []string{"n", "esc"} {
		if !a.confirm.Active() {
			a.dispatchCommand("hostshell")
		}
		step(a, key(no))
		if a.confirm.Active() || len(*ran) != 0 {
			t.Errorf("%s: confirm still up %v, ran %d", no, a.confirm.Active(), len(*ran))
		}
	}
	a.dispatchCommand("hostshell")
	step(a, key("y"))
	if len(*ran) != 1 {
		t.Errorf("yes ran %d commands", len(*ran))
	}

	// No context: the endpoint alone names the daemon.
	b := newTestApp(t)
	b.client = &docker.Client{Host: "unix:///var/run/docker.sock"}
	b.dispatchCommand("hostshell")
	if !strings.Contains(b.confirm.Prompt(), "host of unix:///var/run/docker.sock?") {
		t.Errorf("confirm %q", b.confirm.Prompt())
	}
}

// TestHostShellReadonly: --readonly, the top-level readOnly and a context's
// readOnly all refuse it before a confirm; readonly turned on while the
// confirm is up refuses the yes.
func TestHostShellReadonly(t *testing.T) {
	fakeDockerCLI(t)
	cases := map[string]Options{
		"--readonly":       {ReadOnly: true, ForceReadOnly: true},
		"readOnly":         {ReadOnly: true},
		"context readOnly": {Contexts: map[string]ContextSettings{"prod": {ReadOnly: boolp(true)}}},
	}
	for name, opts := range cases {
		a := ctxApp(t, "prod", opts)
		ran := capture(a)
		a.dispatchCommand("hostshell")
		if a.confirm.Active() || len(*ran) != 0 || !strings.Contains(a.errFlash, "readonly mode — host shell refused") {
			t.Errorf("%s: confirm %v ran %d flash %q", name, a.confirm.Active(), len(*ran), a.errFlash)
		}
	}

	a := ctxApp(t, "dev", Options{Contexts: map[string]ContextSettings{"prod": {ReadOnly: boolp(true)}}})
	ran := capture(a)
	a.dispatchCommand("hostshell")
	if !a.confirm.Active() {
		t.Fatalf("a writable context refused: %q", a.errFlash)
	}
	a.readonly = true // a reload while the bar is up
	step(a, key("y"))
	if len(*ran) != 0 || !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("yes under a fresh readonly: ran %d, flash %q", len(*ran), a.errFlash)
	}
}

// TestHostShellRefusals: no daemon, a rootless one, and an image docker
// would read as a flag are refused with a reason, before any confirm.
func TestHostShellRefusals(t *testing.T) {
	fakeDockerCLI(t)
	for name, tc := range map[string]struct {
		client *docker.Client
		image  string
		want   string
	}{
		"no daemon": {want: "no daemon"},
		"rootless": {
			client: &docker.Client{Host: "unix:///run/user/501/podman/podman.sock", Rootless: true},
			want:   "runs rootless — a container there cannot enter its host's namespaces",
		},
		"flag image": {
			client: &docker.Client{Host: "unix:///run/fake.sock"},
			image:  "--volume=/:/host",
			want:   "cannot start with '-'",
		},
	} {
		a := newTestApp(t)
		a.client, a.hostShellImage = tc.client, tc.image
		ran := capture(a)
		a.dispatchCommand("hostshell")
		if a.confirm.Active() || len(*ran) != 0 || !strings.Contains(a.errFlash, tc.want) {
			t.Errorf("%s: confirm %v ran %d flash %q", name, a.confirm.Active(), len(*ran), a.errFlash)
		}
	}
}

// TestHostShellIsACommand: -c and defaultView take :hostshell, which
// still confirms before anything runs.
func TestHostShellIsACommand(t *testing.T) {
	if err := ValidateCommand("hostshell", nil); err != nil {
		t.Errorf("-c hostshell: %v", err)
	}
}

// TestRuntimeShellPerKind: s on a Docker Desktop row opens the host shell
// on that runtime's own endpoint, after a confirm naming it; on a Podman
// or Colima row it is still the runtime's ssh, with no confirm; on a
// stopped single-engine row it is refused; under readonly it is refused.
func TestRuntimeShellPerKind(t *testing.T) {
	fakeCLIs(t, "docker", "podman", "colima")

	a, _ := newEnginesApp(t)
	// dockmaster shows another daemon: the shell must go to the row's.
	a.client = &docker.Client{Host: "unix:///elsewhere.sock"}
	ran := capture(a)
	step(a, tea.KeyPressMsg{Code: tea.KeyDown}) // docker-desktop
	step(a, key("s"))
	if !strings.Contains(a.confirm.Prompt(), "ROOT shell on the host of docker-desktop?") || len(*ran) != 0 {
		t.Fatalf("s on docker-desktop: confirm %q ran %d flash %q", a.confirm.Prompt(), len(*ran), a.errFlash)
	}
	step(a, key("y"))
	if len(*ran) != 1 {
		t.Fatalf("yes ran %d", len(*ran))
	}
	sameArgs(t, "docker-desktop", argsOf(t, (*ran)[0]),
		append([]string{"--host", "unix:///h/.docker/run/docker.sock"}, hostShellArgv("alpine:3")...))

	// Podman: a running machine keeps podman machine ssh, no confirm.
	f := &cliFake{out: map[string]string{
		"podman machine list --format json": `[{"Name":"pm","Running":true,"VMType":"applehv","CPUs":2,` +
			`"Memory":"2147483648","DiskSize":"107374182400"}]`,
	}, fail: map[string]bool{}}
	p := NewApp(nil, Options{Version: "test", Engines: []engines.Provider{engines.Podman{Run: f.run}}})
	p.splashActive, p.loading = false, false
	step(p, tea.WindowSizeMsg{Width: 200, Height: 30})
	runCmd(p, p.switchView(style.ViewRuntimes))
	pran := capture(p)
	step(p, key("s"))
	if p.confirm.Active() || len(*pran) != 1 {
		t.Fatalf("s on podman: confirm %v ran %d flash %q", p.confirm.Active(), len(*pran), p.errFlash)
	}
	if filepath.Base((*pran)[0].Path) != "podman" {
		t.Errorf("s on podman ran %s", (*pran)[0].Path)
	}
	sameArgs(t, "podman", argsOf(t, (*pran)[0]), []string{"machine", "ssh", "pm"})

	// Colima: colima ssh, no confirm.
	c, _ := newColimaApp(t, Options{})
	cran := capture(c)
	step(c, key("s"))
	if c.confirm.Active() || len(*cran) != 1 {
		t.Fatalf("s on colima: confirm %v ran %d flash %q", c.confirm.Active(), len(*cran), c.errFlash)
	}
	if filepath.Base((*cran)[0].Path) != "colima" {
		t.Errorf("s on colima ran %s", (*cran)[0].Path)
	}
	sameArgs(t, "colima", argsOf(t, (*cran)[0]), []string{"ssh", "--profile", "default"})

	// A stopped single-engine runtime.
	stopped := engines.Single{
		Run: f.run, Engine: engines.OrbStackName, Context: "orbstack", Host: "unix:///h/.orbstack/run/docker.sock",
		Ping: func(context.Context, string) bool { return false },
	}
	o := NewApp(nil, Options{Version: "test", Engines: []engines.Provider{stopped}})
	o.splashActive, o.loading = false, false
	step(o, tea.WindowSizeMsg{Width: 200, Height: 30})
	runCmd(o, o.switchView(style.ViewRuntimes))
	oran := capture(o)
	step(o, key("s"))
	if o.confirm.Active() || len(*oran) != 0 || !strings.Contains(o.errFlash, "orbstack is stopped") {
		t.Errorf("s on stopped orbstack: confirm %v ran %d flash %q", o.confirm.Active(), len(*oran), o.errFlash)
	}

	// Readonly refuses s on every kind.
	r, _ := newEnginesApp(t)
	r.readonly = true
	rran := capture(r)
	for _, down := range []bool{false, true} {
		if down {
			step(r, tea.KeyPressMsg{Code: tea.KeyDown})
		}
		r.errFlash = ""
		step(r, key("s"))
		if r.confirm.Active() || len(*rran) != 0 || !strings.Contains(r.errFlash, "readonly mode — runtime shell refused") {
			t.Errorf("s under readonly (row %v): confirm %v ran %d flash %q", down, r.confirm.Active(), len(*rran), r.errFlash)
		}
	}
}
