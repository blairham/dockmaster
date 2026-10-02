// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/tui/style"
)

func newPodsApp(t *testing.T, opts Options) (*App, *cliFake) {
	t.Helper()
	f := &cliFake{out: map[string]string{
		"podman machine list --format json":                `[{"Name":"default","Running":true}]`,
		"podman machine inspect default":                   `[{"Rootful":false}]`,
		"podman --connection default pod inspect p1":       `{"Id":"p1","Name":"web","State":"Running"}`,
		"podman --connection default pod ps --format json": `[{"Id":"p1","Name":"web","Status":"Running","Created":"2026-10-01T12:00:00Z","Containers":[{"Names":"p1-infra","Status":"running"},{"Names":"nginx","Status":"running"}]}]`,
	}, fail: map[string]bool{}}
	opts.Engines = []engines.Provider{engines.Podman{Run: f.run}}
	if opts.Version == "" {
		opts.Version = "test"
	}
	a := NewApp(nil, opts)
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 200, Height: 30})
	_, cmd := a.dispatchCommand("pods")
	runCmd(a, cmd)
	return a, f
}

func TestPodsView(t *testing.T) {
	a, f := newPodsApp(t, Options{})
	if a.view != style.ViewPods {
		t.Fatalf(":pods opened %v", a.view)
	}
	out := render(a)
	for _, want := range []string{"MACHINE", "default", "web", "Running", "2/2", "p1-infra,nginx"} {
		if !strings.Contains(out, want) {
			t.Errorf("pods view is missing %q\n%s", want, out)
		}
	}

	runOnce(a, step(a, key("u")))
	runOnce(a, step(a, key("R")))
	for _, want := range []string{"podman --connection default pod start p1", "podman --connection default pod restart p1"} {
		if !f.called(want) {
			t.Errorf("missing %q in %q", want, f.calls)
		}
	}

	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "remove pod web") {
		t.Fatalf("confirm = %v %q", a.confirm.Active(), a.confirm.Prompt())
	}
	step(a, key("n"))
	if f.called("podman --connection default pod rm -f p1") {
		t.Error("declined remove ran")
	}
	step(a, key("ctrl+d"))
	runOnce(a, step(a, key("y")))
	if !f.called("podman --connection default pod rm -f p1") {
		t.Errorf("confirmed remove did not run: %q", f.calls)
	}

	runCmd(a, step(a, key("o")))
	if a.view != style.ViewInspect || !strings.Contains(render(a), `"State": "Running"`) {
		t.Errorf("inspect: view %v\n%s", a.view, render(a))
	}
}

func TestPodsReadonly(t *testing.T) {
	a, f := newPodsApp(t, Options{ReadOnly: true})
	for _, k := range []string{"u", "x", "R", "ctrl+d"} {
		a.errFlash = ""
		runOnce(a, step(a, key(k)))
		if !strings.Contains(a.errFlash, "readonly") || a.confirm.Active() {
			t.Errorf("%s not refused (flash %q)", k, a.errFlash)
		}
	}
	for _, c := range f.calls {
		if strings.Contains(c, "pod start") || strings.Contains(c, "pod stop") || strings.Contains(c, "pod rm") {
			t.Errorf("readonly ran %q", c)
		}
	}
}

func TestPodsWithoutPodman(t *testing.T) {
	a := newTestApp(t)
	a.dispatchCommand("pods")
	if out := render(a); !strings.Contains(out, "podman is not installed") {
		t.Errorf("no install hint:\n%s", out)
	}
}
