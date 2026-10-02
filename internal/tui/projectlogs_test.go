// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeProjectLogs stands in for the daemon: the project it was asked for,
// and a stream fed with the given lines.
func fakeProjectLogs(t *testing.T, lines ...docker.LogLine) *string {
	t.Helper()
	asked := new(string)
	old := views.OpenProjectLogs
	views.OpenProjectLogs = func(_ context.Context, _ *docker.Client, project string, _ int, _ bool, _ time.Time) (*docker.LogStream, error) {
		*asked = project
		ch := make(chan docker.LogLine, len(lines))
		for _, l := range lines {
			ch <- l
		}
		return &docker.LogStream{Lines: ch, Err: make(chan error, 1), Sources: []string{"web-1", "worker-1"}}, nil
	}
	t.Cleanup(func() { views.OpenProjectLogs = old })
	return asked
}

// TestProjectLogs: l on a project opens one log for all its containers,
// each line prefixed with the container, the prefixes padded to one width;
// a member that stopped shows as a dim note; and the keys that act on one
// container do nothing in a log that has several (#4).
func TestProjectLogs(t *testing.T) {
	asked := fakeProjectLogs(t,
		docker.LogLine{Source: "web-1", Text: "GET / 200"},
		docker.LogLine{Source: "worker-1", Text: "job done"},
		docker.LogLine{Source: "web-1", Text: "— stopped —", Note: true},
	)
	a, _, _ := newComposeApp(t, Options{}, true)
	pump(a, step(a, key("l")), 4)
	if a.view != style.ViewLogs || *asked != "shop" {
		t.Fatalf("l opened %v for project %q", a.view, *asked)
	}
	out := render(a)
	for _, want := range []string{"web-1    | GET / 200", "worker-1 | job done", "web-1    | — stopped —"} {
		if !strings.Contains(out, want) {
			t.Errorf("merged log lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "shop") {
		t.Errorf("the log is not titled with the project:\n%s", out)
	}

	for _, k := range []string{"o", "x", "R", "d"} {
		if act, _ := a.activeViewHandleKey(k); act != "" {
			t.Errorf("%s in a project log asks for %q; there is no one container to act on", k, act)
		}
	}
}

// pump runs a command chain n links deep: the log's drain re-arms itself
// forever on an open stream, so running it to completion would not end.
func pump(a *App, cmd tea.Cmd, n int) {
	for ; cmd != nil && n > 0; n-- {
		msg := cmd()
		if msg == nil {
			return
		}
		cmd = step(a, msg)
	}
}

// TestProjectLogsCommand: :logs on the projects view opens the project's
// log, as l does.
func TestProjectLogsCommand(t *testing.T) {
	fakeProjectLogs(t)
	a, _, _ := newComposeApp(t, Options{}, true)
	if errMsg, _ := a.dispatchCommand("logs"); errMsg != "" {
		t.Fatalf(":logs on a project: %s", errMsg)
	}
	if lv := typedView[*views.LogsView](
		a,
		style.ViewLogs,
	); a.view != style.ViewLogs || lv == nil ||
		lv.Project() != "shop" {
		t.Errorf(":logs opened %v", a.view)
	}
}
