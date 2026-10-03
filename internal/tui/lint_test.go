// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeLint stands in for the daemon: three containers, one clean, one with
// a warning, one with a risk.
func fakeLint(t *testing.T) {
	t.Helper()
	old := views.RunLint
	views.RunLint = func(context.Context, *docker.Client) ([]docker.LintResult, error) {
		return []docker.LintResult{
			{Container: docker.Container{ID: "c1", Name: "abc-tidy"}},
			{Container: docker.Container{ID: "c2", Name: "mid-api"}, Findings: []docker.Finding{
				{
					Rule:     "no-healthcheck",
					Severity: docker.Warn,
					Message:  "no healthcheck: docker reports it running while it is hung",
				},
			}},
			{Container: docker.Container{ID: "c3", Name: "zed-agent"}, Findings: []docker.Finding{
				{Rule: "docker-socket", Severity: docker.Risk, Message: "mounts the docker socket /var/run/docker.sock"},
				{Rule: "root", Severity: docker.Warn, Message: "runs as root"},
			}},
		}, nil
	}
	t.Cleanup(func() { views.RunLint = old })
}

// TestLintView: :lint lists every container worst first, enter opens one
// container's findings, and the filter matches rule names (#10).
func TestLintView(t *testing.T) {
	fakeLint(t)
	a := newTestApp(t)
	_, cmd := a.dispatchCommand("lint")
	runCmd(a, cmd)
	if a.view != style.ViewLint {
		t.Fatalf(":lint opened %v", a.view)
	}
	out := render(a)
	// Named so alphabetical order is the reverse of severity: only the
	// worst-first sort puts zed-agent on top.
	agent, api, tidy := strings.Index(out, "zed-agent"), strings.Index(out, "mid-api"), strings.Index(out, "abc-tidy")
	if agent < 0 || api < 0 || tidy < 0 || agent > api || api > tidy {
		t.Fatalf("not worst first:\n%s", out)
	}
	for _, want := range []string{"risk", "warn", "ok", "mounts the docker socket"} {
		if !strings.Contains(out, want) {
			t.Errorf("lint view lacks %q:\n%s", want, out)
		}
	}

	runCmd(a, step(a, key("enter")))
	if a.view != style.ViewInspect {
		t.Fatalf("enter opened %v", a.view)
	}
	report := render(a)
	for _, want := range []string{"docker-socket", "root", "risk", "warn"} {
		if !strings.Contains(report, want) {
			t.Errorf("findings report lacks %q:\n%s", want, report)
		}
	}
	step(a, key("esc"))

	a.onFilterChange("no-healthcheck")
	if lv := typedView[*views.LintView](a, style.ViewLint); lv.Count() != 1 {
		t.Errorf("/no-healthcheck shows %d rows, want the one container with that finding", lv.Count())
	}
}
