// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeStream stands in for running a CLI command: it records each command
// and plays back lines and an exit. With hold set, its output stays open
// until the command is canceled, as a long build's would.
type fakeStream struct {
	exit     error
	canceled chan struct{}
	specs    []docker.CommandSpec
	lines    []string
	mu       sync.Mutex
	hold     bool
}

func (f *fakeStream) run(ctx context.Context, spec docker.CommandSpec) (*docker.CommandStream, error) {
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	f.mu.Unlock()
	lines := make(chan string, len(f.lines))
	done := make(chan error, 1)
	for _, l := range f.lines {
		lines <- l
	}
	if !f.hold {
		close(lines)
		done <- f.exit
		return &docker.CommandStream{Lines: lines, Done: done}, nil
	}
	f.canceled = make(chan struct{})
	go func() {
		<-ctx.Done()
		close(f.canceled)
		close(lines)
		done <- ctx.Err()
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
	if !strings.Contains(a.flash, "build app:dev done") {
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

// TestBuildEscStops: leaving the output view while the build runs stops
// it — nothing else would read its output.
func TestBuildEscStops(t *testing.T) {
	dir := buildTree(t)
	f := &fakeStream{hold: true, lines: []string{"#1 building"}}
	a := newBuildApp(t, f)
	_, cmd := a.dispatchCommand("build " + dir)
	runCmd(a, cmd)
	start := step(a, key("enter"))
	// Start it, but leave the drain unrun: it would wait on the output.
	step(a, start())
	ov := typedView[*views.OutputView](a, style.ViewOutput)
	if ov == nil || !ov.Running() {
		t.Fatal("the build is not running")
	}
	if out := render(a); !strings.Contains(out, "esc stops it") {
		t.Errorf("the running view does not say esc stops it:\n%s", out)
	}
	step(a, key("esc"))
	<-f.canceled
	if ov.Running() || ov.Status() != "canceled" {
		t.Errorf("after esc: running %v, status %q", ov.Running(), ov.Status())
	}
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
