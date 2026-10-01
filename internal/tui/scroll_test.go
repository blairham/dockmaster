package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

func logLines(from, n int) []docker.LogLine {
	out := make([]docker.LogLine, n)
	for i := range out {
		out[i] = docker.LogLine{Text: fmt.Sprintf("line-%04d", from+i)}
	}
	return out
}

// TestScrollingPausesTheLogTail: as in k9s, scrolling a following log
// stops new lines from yanking the view back to the bottom, and G resumes.
// Every key and the wheel go through the app, as a keypress would.
func TestScrollingPausesTheLogTail(t *testing.T) {
	for _, scroll := range []struct {
		msg  tea.Msg
		name string
	}{
		{name: "k", msg: key("k")},
		{name: "up", msg: tea.KeyPressMsg{Code: tea.KeyUp}},
		{name: "ctrl+b", msg: key("ctrl+b")},
		{name: "g", msg: key("g")},
		{name: "wheel", msg: tea.MouseWheelMsg{Button: tea.MouseWheelUp}},
	} {
		t.Run(scroll.name, func(t *testing.T) {
			a := newTestApp(t)
			lv := views.NewLogsView(nil, "abc", "web")
			a.setView(style.ViewLogs, lv)
			a.pushView(style.ViewLogs)
			step(a, views.LogBatchMsg{Lines: logLines(0, 200)})
			if !lv.Follow() || !strings.Contains(render(a), "line-0199") {
				t.Fatal("setup: the tail is not following at the bottom")
			}

			step(a, scroll.msg)
			if lv.Follow() || lv.Status() != "paused" {
				t.Fatalf("%s did not pause follow (status %q)", scroll.name, lv.Status())
			}
			step(a, views.LogBatchMsg{Lines: logLines(200, 5)})
			if strings.Contains(render(a), "line-0204") {
				t.Errorf("a new line pulled the paused view to the bottom after %s", scroll.name)
			}

			step(a, key("G"))
			if !lv.Follow() || !strings.Contains(render(a), "line-0204") {
				t.Errorf("G did not resume following at the newest line")
			}
		})
	}
}

// TestScrollingPausesTheEventFeed: the events view is a stream too.
func TestScrollingPausesTheEventFeed(t *testing.T) {
	a := newTestApp(t)
	a.dispatchCommand("events")
	step(a, views.EventsBatchMsg{Events: sampleEvents()})
	ev := typedView[*views.EventsView](a, style.ViewEvents)
	if !ev.Follow() {
		t.Fatal("setup: the feed is not following")
	}
	step(a, key("k"))
	if ev.Follow() {
		t.Error("k did not pause the event feed")
	}
	step(a, key("G"))
	if !ev.Follow() {
		t.Error("G did not resume the event feed")
	}
}
