// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// TestLogViewTakesTheLoggerConfig: a log opened from the containers view
// keeps at most logger.buffer lines and opens logger.sinceSeconds back.
func TestLogViewTakesTheLoggerConfig(t *testing.T) {
	a := NewApp(nil, Options{Version: "test", LogBuffer: 3, LogSince: 2 * time.Minute})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 40})
	loadContainers(a)
	step(a, key("enter"))
	lv := typedView[*views.LogsView](a, style.ViewLogs)
	if lv == nil {
		t.Fatal("enter did not open a log")
	}
	lines := make([]docker.LogLine, 0, 5)
	for i := 1; i <= 5; i++ {
		lines = append(lines, docker.LogLine{Text: fmt.Sprintf("line %d", i)})
	}
	// Opening the log started its stream: generation 1.
	step(a, views.LogBatchMsg{Gen: 1, Lines: lines})
	text, n := lv.PlainText()
	if n != 3 || !strings.HasPrefix(text, "line 3") {
		t.Errorf("kept %d lines:\n%s", n, text)
	}
	if lv.Range() != 2*time.Minute || !strings.Contains(render(a), "last 2m") {
		t.Errorf("range %v; title:\n%s", lv.Range(), render(a))
	}
}

// TestInspectRefreshKeepsSearchAndPlace: a refetched document keeps the
// reader's search and scroll position — before, a refresh dropped both.
func TestInspectRefreshKeepsSearchAndPlace(t *testing.T) {
	v := views.NewInspectFetchView("doc", nil)
	v.Resize(40, 3)
	body := make([]string, 0, 20)
	for i := 1; i <= 20; i++ {
		body = append(body, fmt.Sprintf("row %02d", i))
	}
	msg := views.InspectRefreshMsg{Kind: views.InspectContainer, Body: []byte(strings.Join(body, "\n"))}
	v.Update(msg)
	v.SetFilter("row 0")
	if v.Status() != "1/9" || v.Count() != 20 {
		t.Fatalf("setup: search reads %q over %d rows", v.Status(), v.Count())
	}
	v.Update(msg)
	if v.Status() != "1/9" {
		t.Errorf("a refresh dropped the search: %q", v.Status())
	}
	v.SetFilter("")
	for range 5 {
		v.UpdateTable(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	before := strings.Split(v.View(), "\n")[0]
	v.Update(msg)
	if after := strings.Split(v.View(), "\n")[0]; after != before {
		t.Errorf("a refresh moved the view from %q to %q", before, after)
	}
}

// TestInspectRefreshIsSingleFlight: a second refresh while one is out is
// dropped, so a live view on a slow daemon does not stack fetches.
func TestInspectRefreshIsSingleFlight(t *testing.T) {
	v := views.NewInspectFetchView("doc", nil)
	if v.Refresh() == nil {
		t.Fatal("the first refresh did nothing")
	}
	if v.Refresh() != nil {
		t.Error("a second refresh started while the first was out")
	}
	v.Update(views.InspectRefreshMsg{Kind: views.InspectContainer, Body: []byte("{}")})
	if v.Refresh() == nil {
		t.Error("no refresh after the first one landed")
	}
}

// TestLiveViewAutoRefresh: the tick refreshes an inspect view only when
// liveViewAutoRefresh is on.
func TestLiveViewAutoRefresh(t *testing.T) {
	for _, live := range []bool{false, true} {
		a := NewApp(nil, Options{Version: "test", LiveRefresh: live})
		a.splashActive, a.loading = false, false
		step(a, tea.WindowSizeMsg{Width: 160, Height: 40})
		loadContainers(a)
		step(a, key("o"))
		if a.view != style.ViewInspect {
			t.Fatalf("o opened %v", a.view)
		}
		v := a.viewMap[style.ViewInspect]
		v.Update(views.InspectRefreshMsg{Kind: views.InspectContainer, ID: "aaaaaaaaaaaa1111", Body: []byte("{}")})
		if got := a.refreshPolledView() != nil; got != live {
			t.Errorf("liveViewAutoRefresh %v: tick refreshes inspect = %v", live, got)
		}
	}
}
