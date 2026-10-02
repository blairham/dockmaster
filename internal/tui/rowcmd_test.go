// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// TestLogsAndInspectCommands: the palette suggested :logs and :inspect and
// then called them unknown. Each now does what the view's own key does on
// the selected row, and says why when nothing here applies.
func TestLogsAndInspectCommands(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	if msg, _ := a.dispatchCommand("logs"); msg != "" || a.view != style.ViewLogs {
		t.Fatalf(":logs from containers: %q, view %v", msg, a.view)
	}
	if lv := typedView[*views.LogsView](a, style.ViewLogs); lv.ContainerID() != sampleContainers()[0].ID {
		t.Errorf(":logs opened %s, want the selected container", lv.ContainerID())
	}
	step(a, key("esc"))

	if msg, _ := a.dispatchCommand("inspect"); msg != "" || a.view != style.ViewInspect {
		t.Errorf(":inspect from containers: %q, view %v", msg, a.view)
	}
	step(a, key("esc"))

	a.dispatchCommand("volumes")
	step(a, views.VolumesRefreshMsg{Volumes: []docker.Volume{{Name: "pgdata", Driver: "local"}}})
	if msg, _ := a.dispatchCommand("describe"); msg != "" || a.view != style.ViewInspect {
		t.Errorf(":describe from volumes: %q, view %v", msg, a.view)
	}

	a.dispatchCommand("events")
	msg, cmd := a.dispatchCommand("logs")
	if !strings.Contains(msg, "selected row") || cmd != nil || a.view != style.ViewEvents {
		t.Errorf(":logs from events = %q (cmd %v, view %v), want a clear refusal", msg, cmd != nil, a.view)
	}
	if strings.Contains(msg, "unknown command") {
		t.Errorf(":logs is still an unknown command: %q", msg)
	}
}
