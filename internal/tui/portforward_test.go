package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
	"github.com/blairham/dockyard/internal/tui/views"
)

func forwardApp(t *testing.T, opts Options) (*App, *[]string) {
	t.Helper()
	a := newSizedApp(t, opts, 200, 30)
	a.splashActive = false
	var opened []string
	a.urlOpener = func(u string) error { opened = append(opened, u); return nil }
	step(a, views.ContainersRefreshMsg{Containers: []docker.Container{
		{
			ID: "reg1", Name: "registry", State: "running",
			Endpoints: []docker.Endpoint{{Network: "kind", IP: "172.18.0.4"}},
			PortList:  []docker.PortMapping{{Type: "tcp", Private: 5000, Public: 5001}},
		},
		{ID: "db1", Name: "db", State: "running", Endpoints: []docker.Endpoint{{Network: "bridge", IP: "172.17.0.3"}}},
	}})
	return a, &opened
}

// TestForwardPrompt: F asks for the ports prefilled with the container's
// first port, refuses a bad spec in place, and starts the forward on a
// good one.
func TestForwardPrompt(t *testing.T) {
	a, _ := forwardApp(t, Options{})
	step(a, key("F"))
	if !a.prompt.Active() || a.prompt.Value() != "5000:5000" {
		t.Fatalf("prompt active %v value %q, want 5000:5000", a.prompt.Active(), a.prompt.Value())
	}
	if out := render(a); !strings.Contains(out, "forward registry") {
		t.Errorf("prompt does not name the container:\n%s", out)
	}
	for range 9 {
		step(a, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(a, "nope")
	step(a, key("enter"))
	if !a.prompt.Active() || !strings.Contains(a.prompt.Error(), "not a port") {
		t.Errorf("bad spec: active %v error %q", a.prompt.Active(), a.prompt.Error())
	}
	for range 4 {
		step(a, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(a, "15000:5000")
	cmd := step(a, key("enter"))
	if cmd == nil || a.prompt.Active() || !strings.Contains(a.flash, "localhost:15000 → registry:5000") {
		t.Errorf("good spec: cmd %v active %v flash %q", cmd != nil, a.prompt.Active(), a.flash)
	}
}

// TestForwardStartedIsRemembered: the session keeps the helper's ID so
// quitting can stop it, and says how to reach the forward.
func TestForwardStartedIsRemembered(t *testing.T) {
	a, _ := forwardApp(t, Options{})
	step(a, forwardStartedMsg{pf: docker.PortForward{ID: "h1", TargetName: "registry", Local: 15000, Remote: 5000}})
	if len(a.forwards) != 1 || a.forwards[0] != "h1" {
		t.Errorf("forwards = %v", a.forwards)
	}
	if !strings.Contains(a.flash, ":pf") {
		t.Errorf("flash = %q", a.flash)
	}
}

func TestBrowsePublishedPort(t *testing.T) {
	a, opened := forwardApp(t, Options{})
	step(a, key("b"))
	if len(*opened) != 1 || (*opened)[0] != "http://localhost:5001" {
		t.Errorf("opened %v, want the published port 5001", *opened)
	}
	step(a, tea.KeyPressMsg{Code: tea.KeyDown})
	step(a, key("b"))
	if len(*opened) != 1 || !strings.Contains(a.errFlash, "publishes no ports") {
		t.Errorf("no published port: opened %v flash %q", *opened, a.errFlash)
	}
}

// TestPortForwardsView: the list, b opening a forward, and ctrl-d asking
// before stopping it.
func TestPortForwardsView(t *testing.T) {
	a, opened := forwardApp(t, Options{})
	a.dispatchCommand("pf")
	if a.view != style.ViewPortForwards {
		t.Fatalf(":pf opened %v", a.view)
	}
	step(a, views.PortForwardsRefreshMsg{Forwards: []docker.PortForward{
		{ID: "h1", TargetName: "registry", Local: 15000, Remote: 5000, State: "running"},
	}})
	if out := render(a); !strings.Contains(out, "http://localhost:15000") || !strings.Contains(out, "registry") {
		t.Errorf("forward not listed:\n%s", out)
	}
	step(a, key("b"))
	if len(*opened) != 1 || (*opened)[0] != "http://localhost:15000" {
		t.Errorf("b opened %v", *opened)
	}
	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "stop forwarding localhost:15000 → registry:5000") {
		t.Errorf("confirm = %v %q", a.confirm.Active(), a.confirm.Prompt())
	}
}

func TestForwardReadonly(t *testing.T) {
	a, _ := forwardApp(t, Options{ReadOnly: true})
	step(a, key("F"))
	if a.prompt.Active() || !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("F in readonly: prompt %v flash %q", a.prompt.Active(), a.errFlash)
	}
}

// TestQuitStopsSessionForwards: forwards end with dockyard, as k9s's do —
// the helpers would otherwise outlive it and keep their ports.
func TestQuitStopsSessionForwards(t *testing.T) {
	a, _ := forwardApp(t, Options{})
	var stopped []string
	a.stopForward = func(_ context.Context, id string) error { stopped = append(stopped, id); return nil }
	step(a, forwardStartedMsg{pf: docker.PortForward{ID: "h1", TargetName: "registry", Local: 15000, Remote: 5000}})
	step(a, forwardStartedMsg{pf: docker.PortForward{ID: "h2", TargetName: "db", Local: 15432, Remote: 5432}})
	a.dispatchCommand("q")
	if len(stopped) != 2 || stopped[0] != "h1" || stopped[1] != "h2" {
		t.Errorf("quit stopped %v, want [h1 h2]", stopped)
	}
}
