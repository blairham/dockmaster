package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

func sampleEvents() []docker.Event {
	at := time.Date(2026, 10, 1, 12, 30, 5, 0, time.Local)
	return []docker.Event{
		{Time: at, Type: "container", Action: "start", Name: "web", Attrs: map[string]string{"image": "nginx:1.27"}},
		{
			Time:   at,
			Type:   "container",
			Action: "die",
			Name:   "worker",
			Attrs:  map[string]string{"exitCode": "137", "image": "app:2"},
		},
		{Time: at, Type: "image", Action: "pull", Name: "redis:7", Attrs: map[string]string{"name": "redis:7"}},
		{
			Time:   at,
			Type:   "network",
			Action: "connect",
			Name:   "shop_default",
			Attrs:  map[string]string{"container": "abcdef1234567890"},
		},
	}
}

// TestEventsView: the feed opens from the palette and shows each event's
// time, type, action, subject and notable details, colored by meaning.
func TestEventsView(t *testing.T) {
	a := newTestApp(t)
	a.dispatchCommand("events")
	if a.view != style.ViewEvents {
		t.Fatalf(":events opened %v", a.view)
	}
	if out := render(a); !strings.Contains(out, "no events in the last 10 minutes") {
		t.Errorf("empty feed:\n%s", out)
	}
	step(a, views.EventsBatchMsg{Events: sampleEvents()})
	out := render(a)
	for _, want := range []string{
		"12:30:05", "container", "start", "web", "image=nginx:1.27",
		"die", "worker", "exitCode=137", "pull", "redis:7", "connect", "container=abcdef123456",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("feed is missing %q\n%s", want, out)
		}
	}

	red := lipgloss.NewStyle().Foreground(style.ColorRed).Render("x")
	red = red[:strings.IndexByte(red, 'x')]
	for _, l := range strings.Split(renderStyled(a), "\n") {
		if strings.Contains(l, "worker") && !strings.Contains(l, red) {
			t.Errorf("a container dying with 137 is not red: %q", l)
		}
	}
}

func TestEventsFilterAndFollow(t *testing.T) {
	a := newTestApp(t)
	a.dispatchCommand("events")
	step(a, views.EventsBatchMsg{Events: sampleEvents()})
	ev := typedView[*views.EventsView](a, style.ViewEvents)
	ev.SetFilter("die")
	if out := render(a); !strings.Contains(out, "worker") || strings.Contains(out, "redis:7") {
		t.Errorf("filter die:\n%s", out)
	}
	ev.SetFilter("")
	if !ev.Follow() {
		t.Fatal("feed does not start following")
	}
	step(a, key("f"))
	if ev.Follow() {
		t.Error("f did not pause following")
	}
}

// TestEventsStreamStopsWhenLeft: the feed is a stream like a log tail, and
// leaving the view stops it.
func TestEventsStreamStopsWhenLeft(t *testing.T) {
	a := newTestApp(t)
	a.dispatchCommand("events")
	ev := typedView[*views.EventsView](a, style.ViewEvents)
	var _ views.Stoppable = ev
	step(a, key("0"))
	if a.view != style.ViewContainers {
		t.Fatalf("view = %v", a.view)
	}
}

// TestSixOpensEvents: the events feed has a digit like the other views,
// and the header and help say so.
func TestSixOpensEvents(t *testing.T) {
	a := newSizedApp(t, Options{Splashless: true}, 240, 40)
	loadContainers(a)
	if out := render(a); !strings.Contains(out, "<6>") || !strings.Contains(out, "Events") {
		t.Errorf("header does not offer <6> Events:\n%s", out)
	}
	step(a, key("6"))
	if a.view != style.ViewEvents {
		t.Errorf("6 opened %v", a.view)
	}
	step(a, key("?"))
	if out := render(a); !strings.Contains(out, "<6>") {
		t.Errorf("help does not list <6>:\n%s", out)
	}
}
