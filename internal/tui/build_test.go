// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeStream stands in for running a CLI command: it records each command
// and plays back lines and an exit. With hold set, its output stays open
// until the command is canceled or the test ends it — as a long build's
// would — and the test can print more through out meanwhile.
type fakeStream struct {
	exit     error
	canceled chan struct{}
	out      chan string
	end      chan error
	specs    []docker.CommandSpec
	lines    []string
	mu       sync.Mutex
	hold     bool
}

func (f *fakeStream) run(ctx context.Context, spec docker.CommandSpec) (*docker.CommandStream, error) {
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	f.mu.Unlock()
	lines := make(chan string, len(f.lines)+16)
	done := make(chan error, 1)
	for _, l := range f.lines {
		lines <- l
	}
	if !f.hold {
		close(lines)
		done <- f.exit
		return &docker.CommandStream{Lines: lines, Done: done}, nil
	}
	f.out, f.end, f.canceled = lines, make(chan error, 1), make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			close(f.canceled)
			close(lines)
			done <- ctx.Err()
		case err := <-f.end:
			close(lines)
			done <- err
		}
	}()
	return &docker.CommandStream{Lines: lines, Done: done}, nil
}

func (f *fakeStream) last() docker.CommandSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.specs) == 0 {
		return docker.CommandSpec{}
	}
	return f.specs[len(f.specs)-1]
}

// buildTree is a project with a Dockerfile, a second one for another
// target, and a README.
func buildTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"Dockerfile":     "FROM scratch\n",
		"Dockerfile.dev": "FROM scratch\n",
		"README.md":      "hi\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newBuildApp(t *testing.T, f *fakeStream) *App {
	t.Helper()
	a := newTestApp(t)
	a.streamFn = f.run
	a.builderFn = func(context.Context, string) docker.Builder { return docker.BuilderBuildx }
	a.dumpDir = t.TempDir()
	return a
}

// TestBuildCommandRunsBuildx: :build <dir> opens the form on the directory;
// a tag typed and enter runs `docker buildx build` from it, streaming the
// output into the output view, which says it finished.
func TestBuildCommandRunsBuildx(t *testing.T) {
	dir := buildTree(t)
	f := &fakeStream{lines: []string{"#1 [internal] load build definition", "#2 DONE 0.1s"}}
	a := newBuildApp(t, f)
	if msg, cmd := a.dispatchCommand("build " + dir); msg != "" {
		t.Fatalf(":build: %s", msg)
	} else {
		runCmd(a, cmd)
	}
	if a.view != style.ViewBuildForm {
		t.Fatalf(":build opened %v", a.view)
	}
	if out := render(a); !strings.Contains(out, dir) {
		t.Errorf("the form does not show the context:\n%s", out)
	}
	typeText(a, "app:dev")
	runCmd(a, step(a, key("enter")))

	got := f.last()
	want := []string{"docker", "buildx", "build", "--progress=plain", "--load", "--tag=app:dev", dir}
	if strings.Join(got.Args, " ") != strings.Join(want, " ") || got.Dir != dir {
		t.Errorf("ran %q in %s, want %q in %s", got.Args, got.Dir, want, dir)
	}
	if a.view != style.ViewOutput {
		t.Fatalf("after enter the view is %v, want the output", a.view)
	}
	out := render(a)
	for _, w := range []string{"load build definition", "#2 DONE", "done in"} {
		if !strings.Contains(out, w) {
			t.Errorf("output view is missing %q:\n%s", w, out)
		}
	}
	if !strings.Contains(a.flash, "[app:dev] build finished") {
		t.Errorf("flash %q", a.flash)
	}
	// The form closed when the build started: esc goes back past it.
	step(a, key("esc"))
	if a.view == style.ViewBuildForm {
		t.Error("esc from the output went back to the form")
	}
}

// TestBuildFailureStaysOnScreen: a failed build leaves its output and the
// reason in the view, not only in a flash.
func TestBuildFailureStaysOnScreen(t *testing.T) {
	dir := buildTree(t)
	f := &fakeStream{
		lines: []string{"#5 ERROR: failed to solve: unknown instruction: FORM"},
		exit:  errors.New("exit status 1"),
	}
	a := newBuildApp(t, f)
	_, cmd := a.dispatchCommand("build " + dir)
	runCmd(a, cmd)
	runCmd(a, step(a, key("enter")))
	if a.view != style.ViewOutput {
		t.Fatalf("view %v, want the output", a.view)
	}
	out := render(a)
	for _, w := range []string{"unknown instruction: FORM", "failed after", "exit status 1"} {
		if !strings.Contains(out, w) {
			t.Errorf("failed build output is missing %q:\n%s", w, out)
		}
	}
	if !strings.Contains(a.errFlash, "failed") {
		t.Errorf("errFlash %q", a.errFlash)
	}
}

// TestBuildFormChecksBeforeRunning: a build arg without a key and a tag
// that docker would read as a flag stay in the form, with the reason, and
// nothing runs.
func TestBuildFormChecksBeforeRunning(t *testing.T) {
	dir := buildTree(t)
	f := &fakeStream{}
	a := newBuildApp(t, f)
	_, cmd := a.dispatchCommand("build " + dir)
	runCmd(a, cmd)
	typeText(a, "-oops")
	runCmd(a, step(a, key("enter")))
	if a.view != style.ViewBuildForm || len(f.specs) != 0 {
		t.Fatalf("a flag-shaped tag ran: view %v, %d runs", a.view, len(f.specs))
	}
	if out := render(a); !strings.Contains(out, "starts with -") {
		t.Errorf("the form does not say why:\n%s", out)
	}
}

// TestBuildFromDockerfile: :build on a Dockerfile builds its directory with
// that file, and b in :dir on a Dockerfile row does the same.
func TestBuildFromDockerfile(t *testing.T) {
	dir := buildTree(t)
	f := &fakeStream{}
	a := newBuildApp(t, f)
	_, cmd := a.dispatchCommand("build " + filepath.Join(dir, "Dockerfile.dev"))
	runCmd(a, cmd)
	runCmd(a, step(a, key("enter")))
	got := strings.Join(f.last().Args, " ")
	if !strings.Contains(got, "--file="+filepath.Join(dir, "Dockerfile.dev")) || !strings.HasSuffix(got, " "+dir) {
		t.Errorf(":build on a Dockerfile ran %q", got)
	}

	b := newBuildApp(t, f)
	openDir(t, b, dir)
	step(b, key("j")) // Dockerfile.dev: Dockerfiles list first, by name
	runCmd(b, step(b, key("b")))
	if b.view != style.ViewBuildForm {
		t.Fatalf("b on a Dockerfile opened %v", b.view)
	}
	runCmd(b, step(b, key("enter")))
	if got := strings.Join(f.last().Args, " "); !strings.Contains(got, "--file="+filepath.Join(dir, "Dockerfile.dev")) {
		t.Errorf("b on Dockerfile.dev ran %q", got)
	}

	c := newBuildApp(t, f)
	openDir(t, c, dir)
	step(c, key("G")) // README.md
	step(c, key("b"))
	if !strings.Contains(c.errFlash, "not a Dockerfile") {
		t.Errorf("b on README.md: %q", c.errFlash)
	}
}

// TestBuildReadonly: --readonly refuses the form and so the build.
func TestBuildReadonly(t *testing.T) {
	dir := buildTree(t)
	f := &fakeStream{}
	a := newBuildApp(t, f)
	a.readonly = true
	_, cmd := a.dispatchCommand("build " + dir)
	runCmd(a, cmd)
	if a.view == style.ViewBuildForm || !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("readonly: view %v, err %q", a.view, a.errFlash)
	}
	_, cmd = a.handleAction("build_run", `{"context":"`+dir+`"}`)
	runCmd(a, cmd)
	if len(f.specs) != 0 {
		t.Errorf("readonly ran a build: %v", f.specs)
	}
}

// startHeldBuild starts a build of dir on a held fake stream and drains
// its first batch: what it returns reads the next one. The drain is never
// run to the end here — a held stream would keep it polling for ever.
func startHeldBuild(t *testing.T, a *App, dir string) tea.Cmd {
	t.Helper()
	_, cmd := a.dispatchCommand("build " + dir)
	runCmd(a, cmd)
	typeText(a, "app:dev")
	start := step(a, key("enter"))
	drain := step(a, start())
	return step(a, drain())
}

// drainToEnd reads a build's batches until its end has been handled, and
// returns what the end asked to run next.
func drainToEnd(t *testing.T, a *App, next tea.Cmd) tea.Cmd {
	t.Helper()
	for range 1000 {
		if next == nil {
			t.Fatal("the drain stopped before the build ended")
		}
		msg := next()
		next = step(a, msg)
		if _, ok := msg.(views.OutputDoneMsg); ok {
			return next
		}
	}
	t.Fatal("the build never ended")
	return nil
}

// TestBuildEscDetaches (#121): leaving the output view leaves the build
// running; what it prints meanwhile is there when :build reattaches; x
// stops it, after a confirm, keeping the output.
func TestBuildEscDetaches(t *testing.T) {
	dir := buildTree(t)
	f := &fakeStream{hold: true, lines: []string{"#1 building"}}
	a := newBuildApp(t, f)
	next := startHeldBuild(t, a, dir)
	if out := render(a); !strings.Contains(out, "#1 building") || !strings.Contains(out, "esc leaves it running") {
		t.Errorf("the running view:\n%s", out)
	}
	step(a, key("esc"))
	if a.view == style.ViewOutput {
		t.Fatal("esc stayed in the output")
	}
	select {
	case <-f.canceled:
		t.Fatal("esc stopped the build")
	default:
	}
	b := a.latestRunningBuild()
	if b == nil {
		t.Fatal("no build is running after esc")
	}

	f.out <- "#2 while you were away"
	next = step(a, next())
	if _, cmd := a.dispatchCommand("build"); cmd != nil {
		runCmd(a, cmd)
	}
	if a.view != style.ViewOutput {
		t.Fatalf(":build with a build running opened %v", a.view)
	}
	out := render(a)
	for _, w := range []string{"#1 building", "#2 while you were away"} {
		if !strings.Contains(out, w) {
			t.Errorf("reattached output is missing %q:\n%s", w, out)
		}
	}

	step(a, key("x"))
	if !a.confirm.Active() {
		t.Fatal("x did not ask before stopping")
	}
	step(a, key("y"))
	<-f.canceled
	drainToEnd(t, a, next)
	if b.Running() || b.Status() != "canceled" {
		t.Errorf("after x: running %v, status %q", b.Running(), b.Status())
	}
	if out := render(a); !strings.Contains(out, "#2 while you were away") || !strings.Contains(out, "stopped after") {
		t.Errorf("the stopped build's output:\n%s", out)
	}
	if !strings.Contains(a.flash, "[app:dev] build stopped") {
		t.Errorf("flash %q", a.flash)
	}
}

// TestBuildStopReadonly: x on a running build is refused under readonly,
// as every other change is.
func TestBuildStopReadonly(t *testing.T) {
	dir := buildTree(t)
	f := &fakeStream{hold: true}
	a := newBuildApp(t, f)
	startHeldBuild(t, a, dir)
	a.readonly = true
	step(a, key("x"))
	if a.confirm.Active() || !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("readonly x: confirm %v, err %q", a.confirm.Active(), a.errFlash)
	}
	a.stopAllBuilds()
	<-f.canceled
}

// TestBuildEndsElsewhere: a build that ends while its output is closed
// says so in the flash — finished, or failed with its exit status.
func TestBuildEndsElsewhere(t *testing.T) {
	exit3 := exec.Command("sh", "-c", "exit 3").Run()
	for _, tc := range []struct {
		exit    error
		name    string
		want    string
		wantErr bool
	}{
		{name: "finished", want: "[app:dev] build finished"},
		{name: "failed", exit: exit3, want: "[app:dev] build failed (exit 3)", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := buildTree(t)
			f := &fakeStream{hold: true}
			a := newBuildApp(t, f)
			next := startHeldBuild(t, a, dir)
			step(a, key("esc"))
			f.end <- tc.exit
			drainToEnd(t, a, next)
			got := a.flash
			if tc.wantErr {
				got = a.errFlash
			}
			if got != tc.want {
				t.Errorf("flash %q / %q, want %q", a.flash, a.errFlash, tc.want)
			}
			if a.latestRunningBuild() != nil {
				t.Error("the build still counts as running")
			}
		})
	}
}

// TestQuitWithBuildRunningAsks: quitting with a build running asks first;
// ctrl-c again answers it, and :q! does not ask. Either way the build is
// stopped with the app.
func TestQuitWithBuildRunningAsks(t *testing.T) {
	dir := buildTree(t)
	f := &fakeStream{hold: true}
	a := newBuildApp(t, f)
	startHeldBuild(t, a, dir)
	if cmd := step(a, key("ctrl+c")); cmd != nil || !a.confirm.Active() {
		t.Fatalf("ctrl-c with a build running: cmd %v, confirm %v", cmd != nil, a.confirm.Active())
	}
	if !strings.Contains(render(a), "1 build is still running") {
		t.Errorf("the question:\n%s", render(a))
	}
	cmd := step(a, key("ctrl+c"))
	if cmd == nil {
		t.Fatal("a second ctrl-c did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("a second ctrl-c did not quit")
	}
	<-f.canceled

	g := &fakeStream{hold: true}
	b := newBuildApp(t, g)
	startHeldBuild(t, b, dir)
	_, cmd = b.dispatchCommand("q!")
	if cmd == nil {
		t.Fatal(":q! asked")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error(":q! did not quit")
	}
	<-g.canceled
}

// TestBuildOutputSave: ctrl-s saves the output, styling stripped, under the
// saved logs.
func TestBuildOutputSave(t *testing.T) {
	dir := buildTree(t)
	f := &fakeStream{lines: []string{"\x1b[31mred step\x1b[0m"}}
	a := newBuildApp(t, f)
	_, cmd := a.dispatchCommand("build " + dir)
	runCmd(a, cmd)
	runCmd(a, step(a, key("enter")))
	runCmd(a, step(a, key("ctrl+s")))
	matches, err := filepath.Glob(filepath.Join(a.dumpDir, "logs", "*"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("saved %v (%v); flash %q %q", matches, err, a.flash, a.errFlash)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "red step") || strings.Contains(string(b), "\x1b") {
		t.Errorf("saved %q", b)
	}
}

// TestBuildCommandMissingPath: :build on a path that is not there says so.
func TestBuildCommandMissingPath(t *testing.T) {
	a := newBuildApp(t, &fakeStream{})
	if msg, _ := a.dispatchCommand(
		"build " + filepath.Join(t.TempDir(), "nope"),
	); !strings.Contains(
		msg,
		"not a directory",
	) {
		t.Errorf(":build on a missing path: %q", msg)
	}
}
