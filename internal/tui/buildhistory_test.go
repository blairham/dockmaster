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
)

// emptyHistory is a builder with no records.
func emptyHistory(context.Context, []string, ...string) ([]byte, error) { return nil, nil }

// fakeHistory stands in for `docker buildx history`: ls answers with the
// captured listing, inspect --format=json with the captured details (the
// failure's for its ID, else the success's, its context moved to ctxDir),
// pretty inspect with a line naming the ID, and rm records what it removed.
type fakeHistory struct {
	t       *testing.T
	ctxDir  string
	calls   []string
	removed []string
	mu      sync.Mutex
}

func buildxFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "docker", "testdata", "buildx", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *fakeHistory) run(_ context.Context, _ []string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.Join(args, " ")
	f.calls = append(f.calls, line)
	last := args[len(args)-1]
	switch {
	case strings.Contains(line, "history ls"):
		return buildxFixture(f.t, "ls.jsonl"), nil
	case strings.Contains(line, "history inspect --format=json"):
		if last == "bp3jjedj9bki9bw5snjenx5vq" {
			return buildxFixture(f.t, "inspect-error.json"), nil
		}
		return []byte(strings.ReplaceAll(string(buildxFixture(f.t, "inspect-ok.json")), "/src/app", f.ctxDir)), nil
	case strings.Contains(line, "history inspect"):
		return []byte("Name:\t" + last + "\nDockerfile:\tDockerfile.dev\n"), nil
	case strings.Contains(line, "history rm"):
		f.removed = append(f.removed, last)
		return nil, nil
	}
	return nil, errors.New("unexpected " + line)
}

func newBuildsApp(t *testing.T) (*App, *fakeHistory) {
	t.Helper()
	a := newBuildApp(t, &fakeStream{})
	h := &fakeHistory{t: t, ctxDir: t.TempDir()}
	a.historyRunner = h.run
	_, cmd := a.dispatchCommand("builds")
	runCmd(a, cmd)
	if a.view != style.ViewBuilds {
		t.Fatalf(":builds opened %v", a.view)
	}
	return a, h
}

// TestBuildsViewListsHistory: :builds lists the builder's records newest
// first with steps, cache hits and status, and a failed build's reason —
// read by inspect, once.
func TestBuildsViewListsHistory(t *testing.T) {
	a, h := newBuildsApp(t)
	out := render(a)
	for _, w := range []string{
		"app/Dockerfile.dev (base)", "Completed", "4/4", "0%",
		"broken", "Error", "3/3", `[1/1] RUN false: process "/bin/sh -c false"`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("builds view is missing %q:\n%s", w, out)
		}
	}
	if strings.Index(out, "app/Dockerfile.dev") > strings.Index(out, "broken") {
		t.Errorf("not newest first:\n%s", out)
	}
	runCmd(a, a.refreshPolledView())
	inspects := 0
	for _, c := range h.calls {
		if strings.Contains(c, "inspect") {
			inspects++
		}
	}
	if inspects != 1 {
		t.Errorf("the failed record was inspected %d times over two listings, want 1: %q", inspects, h.calls)
	}

	step(a, key("ctrl+z"))
	if out := render(a); strings.Contains(out, "app/Dockerfile.dev") || !strings.Contains(out, "broken") {
		t.Errorf("ctrl-z did not list failures only:\n%s", out)
	}
}

// TestBuildsViewRemove: ctrl-d removes a record after a confirm; marked
// rows go with one confirm; --readonly refuses it.
func TestBuildsViewRemove(t *testing.T) {
	a, h := newBuildsApp(t)
	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(render(a), "app/Dockerfile.dev (base)") {
		t.Fatalf("ctrl-d did not ask:\n%s", render(a))
	}
	runCmd(a, step(a, key("y")))
	if strings.Join(h.removed, " ") != "pzk1kndfayz6pb67ixyla74lz" {
		t.Errorf("removed %q", h.removed)
	}

	h.removed = nil
	step(a, key("space"))
	step(a, key("j"))
	step(a, key("space"))
	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(render(a), "2 builds") {
		t.Fatalf("ctrl-d on marked rows:\n%s", render(a))
	}
	runCmd(a, step(a, key("y")))
	if len(h.removed) != 2 {
		t.Errorf("removed %q, want both marked", h.removed)
	}

	r, rh := newBuildsApp(t)
	r.readonly = true
	step(r, key("ctrl+d"))
	if r.confirm.Active() || !strings.Contains(r.errFlash, "readonly") || len(rh.removed) != 0 {
		t.Errorf("readonly ctrl-d: confirm %v, err %q", r.confirm.Active(), r.errFlash)
	}
}

// TestBuildsDigit: 7 opens the builds view as a top-level view, the way
// the other digits open theirs — nothing to esc back to — where :builds
// drills in over the view it was typed in.
func TestBuildsDigit(t *testing.T) {
	a := newBuildApp(t, &fakeStream{})
	h := &fakeHistory{t: t, ctxDir: t.TempDir()}
	a.historyRunner = h.run
	runCmd(a, step(a, key("7")))
	if a.view != style.ViewBuilds || len(a.viewStack) != 0 {
		t.Fatalf("7 opened %v over %v", a.view, a.viewStack)
	}
	if !strings.Contains(render(a), "app/Dockerfile.dev (base)") {
		t.Fatalf("7 did not list the history:\n%s", render(a))
	}
	step(a, key("esc"))
	if a.view != style.ViewBuilds {
		t.Errorf("esc left the top-level builds view for %v", a.view)
	}
	step(a, key("0"))
	_, cmd := a.dispatchCommand("builds")
	runCmd(a, cmd)
	if a.view != style.ViewBuilds || len(a.viewStack) != 1 {
		t.Errorf(":builds opened %v over %v, want a drill-in", a.view, a.viewStack)
	}
}

// TestBuildsDeleteAll: :delete all in the builds view asks, then removes
// every record in one `history rm --all`; readonly refuses it.
func TestBuildsDeleteAll(t *testing.T) {
	a, h := newBuildsApp(t)
	_, cmd := a.dispatchCommand("delete all")
	runCmd(a, cmd)
	if !a.confirm.Active() || !strings.Contains(render(a), "EVERY build record") {
		t.Fatalf(":delete all did not ask:\n%s", render(a))
	}
	runCmd(a, step(a, key("y")))
	if strings.Join(h.removed, " ") != "--all" {
		t.Errorf("removed %q, want one rm --all", h.removed)
	}
	if !strings.Contains(a.flash, "deleted 2 builds") {
		t.Errorf("flash %q", a.flash)
	}

	r, rh := newBuildsApp(t)
	r.readonly = true
	_, cmd = r.dispatchCommand("delete all builds")
	runCmd(r, cmd)
	if r.confirm.Active() || !strings.Contains(r.errFlash, "readonly") || len(rh.removed) != 0 {
		t.Errorf("readonly: confirm %v, err %q, removed %q", r.confirm.Active(), r.errFlash, rh.removed)
	}
}

// TestBuildsViewRebuild: R opens the build form on the record's context,
// Dockerfile, target and args; a context not on this machine is refused.
func TestBuildsViewRebuild(t *testing.T) {
	a, h := newBuildsApp(t)
	if err := os.WriteFile(filepath.Join(h.ctxDir, "Dockerfile.dev"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runCmd(a, step(a, key("R")))
	if a.view != style.ViewBuildForm {
		t.Fatalf("R opened %v (err %q)", a.view, a.errFlash)
	}
	out := render(a)
	for _, w := range []string{h.ctxDir, "Dockerfile.dev", "base", "GREETING=hello"} {
		if !strings.Contains(out, w) {
			t.Errorf("the form is missing %q:\n%s", w, out)
		}
	}

	b, _ := newBuildsApp(t)
	step(b, key("j")) // the failed build, whose context is /src/broken
	runCmd(b, step(b, key("R")))
	if b.view == style.ViewBuildForm || !strings.Contains(b.errFlash, "cannot build broken again") {
		t.Errorf("R on a context not here: view %v, err %q", b.view, b.errFlash)
	}
}

// TestBuildsViewLogsAndInspect: enter replays a record's log into the
// output view — not a build: it never counts as one running — and o
// inspects it.
func TestBuildsViewLogsAndInspect(t *testing.T) {
	a, _ := newBuildsApp(t)
	f := &fakeStream{lines: []string{"#1 [internal] load build definition from Dockerfile.dev"}}
	a.streamFn = f.run
	runCmd(a, step(a, key("enter")))
	if got := strings.Join(f.last().Args, " "); !strings.HasSuffix(got, "buildx history logs pzk1kndfayz6pb67ixyla74lz") {
		t.Errorf("enter ran %q", got)
	}
	if a.view != style.ViewOutput || !strings.Contains(render(a), "load build definition") {
		t.Fatalf("enter: view %v\n%s", a.view, render(a))
	}
	if len(a.runningBuilds()) != 0 || len(a.builds) != 0 || strings.Contains(a.flash, "build finished") {
		t.Errorf("a log replay counted as a build: %d running, %d kept, flash %q",
			len(a.runningBuilds()), len(a.builds), a.flash)
	}
	step(a, key("esc"))
	runCmd(a, step(a, key("o")))
	if a.view != style.ViewInspect || !strings.Contains(render(a), "pzk1kndfayz6pb67ixyla74lz") {
		t.Errorf("o: view %v\n%s", a.view, render(a))
	}
}

// TestBuildsViewWithoutHistory: a buildx without `history` is one message
// naming the version needed, not an empty table.
func TestBuildsViewWithoutHistory(t *testing.T) {
	a := newBuildApp(t, &fakeStream{})
	a.historyRunner = func(context.Context, []string, ...string) ([]byte, error) {
		return nil, &docker.HistoryError{Stderr: `ERROR: unknown command: "history"`, Err: errors.New("exit status 1")}
	}
	_, cmd := a.dispatchCommand("bh")
	runCmd(a, cmd)
	if out := render(a); !strings.Contains(out, "needs buildx v0.20.0") {
		t.Errorf("no buildx history:\n%s", out)
	}
}

// TestBuildsViewShowsRunningBuilds: dockmaster's running build is the top
// row, RUNNING, and leaves the list when it ends.
func TestBuildsViewShowsRunningBuilds(t *testing.T) {
	dir := buildTree(t)
	f := &fakeStream{hold: true}
	a := newBuildApp(t, f)
	next := startHeldBuild(t, a, dir)
	step(a, key("esc"))
	a.historyRunner = (&fakeHistory{t: t}).run
	_, cmd := a.dispatchCommand("builds")
	runCmd(a, cmd)
	out := render(a)
	if !strings.Contains(out, "build app:dev") || !strings.Contains(out, "RUNNING") ||
		strings.Index(out, "build app:dev") > strings.Index(out, "app/Dockerfile.dev") {
		t.Errorf("the running build is not on top:\n%s", out)
	}
	f.end <- nil
	drainToEnd(t, a, next)
	if out := render(a); strings.Contains(out, "RUNNING") {
		t.Errorf("a finished build still shows running:\n%s", out)
	}
}
