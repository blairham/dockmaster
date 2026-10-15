// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/tui/style"
)

// TestComposeBuildStreams: b on a project opens the build form; enter on
// "(all)" runs `compose … build` against the project's files, streamed
// into the output view, and runs nothing after it.
func TestComposeBuildStreams(t *testing.T) {
	a, fc, dir := newComposeApp(t, Options{}, true)
	fs := &fakeStream{lines: []string{"#3 [web] exporting to image", "web Built"}}
	a.streamFn = fs.run
	step(a, key("b"))
	if a.view != style.ViewComposeBuild {
		t.Fatalf("b opened %v", a.view)
	}
	if out := render(a); !strings.Contains(out, "(all)") {
		t.Errorf("the form does not offer every service:\n%s", out)
	}
	runCmd(a, step(a, key("enter")))
	want := "docker compose -p shop --project-directory " + dir + " -f " + filepath.Join(dir, "compose.yaml") + " build"
	if got := strings.Join(fs.last().Args, " "); got != want {
		t.Errorf("ran %q\nwant %q", got, want)
	}
	if a.view != style.ViewOutput {
		t.Fatalf("view %v, want the output", a.view)
	}
	if out := render(a); !strings.Contains(out, "web Built") || !strings.Contains(out, "done in") {
		t.Errorf("output view:\n%s", out)
	}
	if len(fc.calls) != 0 {
		t.Errorf("without then-up, compose also ran %q", fc.calls)
	}
	step(a, key("esc"))
	if a.view != style.ViewProjects {
		t.Errorf("esc went to %v, want the projects (the form closed when the build started)", a.view)
	}
}

// TestComposeBuildServiceThenUp: picking a service builds only it, and
// "then up" runs compose up once the build succeeds — and not when it
// fails.
func TestComposeBuildServiceThenUp(t *testing.T) {
	for _, tc := range []struct {
		exit   error
		name   string
		wantUp bool
	}{
		{name: "success", wantUp: true},
		{name: "failure", exit: errors.New("exit status 17")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, fc, _ := newComposeApp(t, Options{}, true)
			fs := &fakeStream{exit: tc.exit}
			a.streamFn = fs.run
			step(a, key("b"))
			step(a, key("right")) // web
			step(a, key("tab"))
			step(a, key("right")) // then up: yes
			runCmd(a, step(a, key("enter")))
			if got := strings.Join(fs.last().Args, " "); !strings.HasSuffix(got, " build web") {
				t.Errorf("ran %q, want a build of web", got)
			}
			if up := strings.HasSuffix(fc.last(), " up -d"); up != tc.wantUp {
				t.Errorf("compose up ran: %v, want %v (calls %q, err %q)", up, tc.wantUp, fc.calls, a.errFlash)
			}
			if tc.exit != nil && !strings.Contains(render(a), "exit status 17") {
				t.Errorf("the failure is not on screen:\n%s", render(a))
			}
		})
	}
}

// TestComposeBuildRefusals: missing compose files refuse a build up front
// (there is no per-container fallback), and so does --readonly.
func TestComposeBuildRefusals(t *testing.T) {
	a, _, _ := newComposeApp(t, Options{}, false)
	step(a, key("b"))
	if a.view == style.ViewComposeBuild || !strings.Contains(a.errFlash, "cannot build shop") {
		t.Errorf("missing files: view %v, err %q", a.view, a.errFlash)
	}
	r, _, _ := newComposeApp(t, Options{ReadOnly: true}, true)
	step(r, key("b"))
	if r.view == style.ViewComposeBuild || !strings.Contains(r.errFlash, "readonly") {
		t.Errorf("readonly: view %v, err %q", r.view, r.errFlash)
	}
}

// TestComposeConfig: y shows `compose config` in the inspect view, and
// --readonly allows it — it changes nothing.
func TestComposeConfig(t *testing.T) {
	a, _, dir := newComposeApp(t, Options{ReadOnly: true}, true)
	var ran string
	a.composeRunner = func(_ context.Context, args ...string) ([]byte, error) {
		ran = strings.Join(args, " ")
		return []byte("name: shop\nservices:\n  web:\n    image: nginx:1.27\n"), nil
	}
	runCmd(a, step(a, key("y")))
	if a.view != style.ViewInspect {
		t.Fatalf("y opened %v (err %q)", a.view, a.errFlash)
	}
	want := "compose -p shop --project-directory " + dir + " -f " + filepath.Join(dir, "compose.yaml") + " config"
	if ran != want {
		t.Errorf("ran %q\nwant %q", ran, want)
	}
	if out := render(a); !strings.Contains(out, "image: nginx:1.27") {
		t.Errorf("config not shown:\n%s", out)
	}
}

// TestComposeBuildBusyInBackground (#121): a compose build shows its
// project busy while it runs, even with its output closed — the project's
// compose keys say so — and "then up" runs when it finishes, wherever the
// user is by then. The busy marker clears.
func TestComposeBuildBusyInBackground(t *testing.T) {
	a, fc, _ := newComposeApp(t, Options{}, true)
	fs := &fakeStream{hold: true}
	a.streamFn = fs.run
	step(a, key("b"))
	step(a, key("tab"))
	step(a, key("right")) // then up: yes
	start := step(a, key("enter"))
	next := step(a, step(a, start())())
	step(a, key("esc"))
	if a.view != style.ViewProjects {
		t.Fatalf("esc went to %v", a.view)
	}
	if out := render(a); !strings.Contains(out, "building…") {
		t.Errorf("the project is not shown busy:\n%s", out)
	}
	step(a, key("u"))
	if !strings.Contains(a.errFlash, "already building") || len(fc.calls) != 0 {
		t.Errorf("u while building: err %q, calls %q", a.errFlash, fc.calls)
	}
	fs.end <- nil
	runCmd(a, drainToEnd(t, a, next))
	if out := render(a); strings.Contains(out, "building…") {
		t.Errorf("the busy marker outlived the build:\n%s", out)
	}
	if !strings.HasSuffix(fc.last(), " up -d") {
		t.Errorf("then up did not run after the build: calls %q, err %q", fc.calls, a.errFlash)
	}
}
