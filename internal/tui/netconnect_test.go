// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// netDaemon is a docker API that answers every call with success and
// records it as "METHOD /path body", the API version prefix dropped.
func netDaemon(t *testing.T) (*docker.Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	version := regexp.MustCompile(`^/v[0-9.]+`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		if r.URL.Path == "/_ping" {
			_, _ = w.Write([]byte("OK"))
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, r.Method+" "+version.ReplaceAllString(r.URL.Path, "")+" "+string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	c, err := docker.New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

// netApp is the containers view on usageContainers against netDaemon,
// the cursor on web (on the shop network).
func netApp(t *testing.T, opts Options) (*App, func() []string) {
	t.Helper()
	a := newTestApp(t)
	a.readonly = opts.ReadOnly
	c, calls := netDaemon(t)
	a.client = c
	step(a, views.ContainersRefreshMsg{Containers: usageContainers()})
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	for i := 0; i < 3; i++ {
		if s, _ := cv.Selected(); s.Name == "web" {
			break
		}
		step(a, key("j"))
	}
	return a, calls
}

// openPicker presses N and answers the network listing with networks.
func openPicker(t *testing.T, a *App, networks ...string) *views.NetworkPickerView {
	t.Helper()
	act, param := a.activeView().HandleKey("N")
	if act != "network_picker" {
		t.Fatalf("N asked for %q", act)
	}
	targets, err := views.DecodeNetTargets(param)
	if err != nil {
		t.Fatal(err)
	}
	step(a, key("N"))
	step(a, netFormMsg{targets: targets, networks: networks})
	if a.view != style.ViewNetPicker {
		t.Fatalf("the networks did not open the picker: %v", a.view)
	}
	return typedView[*views.NetworkPickerView](a, style.ViewNetPicker)
}

// TestNetworkPickerConnects: N lists the networks with the one the
// container is on marked, starts on one it is not on, and enter connects it
// with the aliases and address typed — a bad address refused in the form.
func TestNetworkPickerConnects(t *testing.T) {
	a, calls := netApp(t, Options{})
	openPicker(t, a, "backend", "bridge", "shop")
	out := render(a)
	for _, want := range []string{"networks of web", "‹ backend ›", "enter connects"} {
		if !strings.Contains(out, want) {
			t.Errorf("picker is missing %q:\n%s", want, out)
		}
	}
	step(a, key("left")) // wraps to shop, which web is on
	if out := render(a); !strings.Contains(out, "shop ● connected") || !strings.Contains(out, "enter disconnects") {
		t.Errorf("the network web is on is not marked:\n%s", out)
	}
	step(a, key("right"))

	step(a, tab)
	typeText(a, "api, web2")
	step(a, tab)
	typeText(a, "not-an-ip")
	step(a, key("enter"))
	if a.view != style.ViewNetPicker || !strings.Contains(render(a), "is not an IP address") {
		t.Fatalf("a bad address was not refused in the form:\n%s", render(a))
	}
	if len(calls()) != 0 {
		t.Fatalf("called the daemon on a bad address: %q", calls())
	}
	for range len("not-an-ip") {
		step(a, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(a, "172.20.0.9")
	runOnce(a, step(a, key("enter")))

	got := strings.Join(calls(), "|")
	for _, want := range []string{
		"POST /networks/backend/connect", `"Container":"c1"`, `"Aliases":["api","web2"]`, `"IPv4Address":"172.20.0.9"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("connect request is missing %s: %s", want, got)
		}
	}
	if a.view != style.ViewContainers || a.flash != "connected web to backend" {
		t.Errorf("after connecting: view %v, flash %q", a.view, a.flash)
	}
}

// TestNetworkPickerDisconnectConfirms: choosing a network the container is
// on disconnects it, only after a yes.
func TestNetworkPickerDisconnectConfirms(t *testing.T) {
	a, calls := netApp(t, Options{})
	openPicker(t, a, "backend", "shop")
	step(a, key("right"))
	step(a, key("enter"))
	if !a.confirm.Active() || !strings.Contains(render(a), "disconnect web from shop?") {
		t.Fatalf("disconnect did not ask first:\n%s", render(a))
	}
	if len(calls()) != 0 {
		t.Fatalf("disconnected before the answer: %q", calls())
	}
	runOnce(a, step(a, key("y")))
	if got := strings.Join(calls(), "|"); !strings.Contains(got, "POST /networks/shop/disconnect") ||
		!strings.Contains(got, `"Container":"c1"`) || strings.Contains(got, `"Force":true`) {
		t.Errorf("after yes: %s", got)
	}
	if a.view != style.ViewContainers || a.flash != "disconnected web from shop" {
		t.Errorf("after disconnecting: view %v, flash %q", a.view, a.flash)
	}
}

// TestNetworkPickerMarkedRows: with rows marked, N picks for all of them,
// takes no fixed address (two containers cannot share one), and connects
// each — then clears the marks, as a bulk key does.
func TestNetworkPickerMarkedRows(t *testing.T) {
	a, calls := netApp(t, Options{})
	step(a, key("space"))
	step(a, key("j"))
	step(a, key("space"))
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	if cv.MarkCount() != 2 {
		t.Fatalf("marked %d", cv.MarkCount())
	}
	f := openPicker(t, a, "backend")
	out := render(a)
	if !strings.Contains(out, "networks of 2 containers") || !strings.Contains(out, "one container only") {
		t.Errorf("picker over two marked rows:\n%s", out)
	}
	if c := f.Change(); len(c.Targets) != 2 || c.IP != "" {
		t.Errorf("change over marks: %+v", c)
	}
	runOnce(a, step(a, key("enter")))
	got := strings.Join(calls(), "|")
	if strings.Count(got, "POST /networks/backend/connect") != 2 || !strings.Contains(got, `"Container":"c1"`) {
		t.Errorf("connect over marks: %s", got)
	}
	if cv.MarkCount() != 0 {
		t.Errorf("marks left after connecting: %d", cv.MarkCount())
	}
}

// TestUsedByNetworkCtrlDDisconnects: in a network's used-by list, ctrl-d
// disconnects the container from that network after a confirm — it never
// removes the container; outside it, ctrl-d removes as before.
func TestUsedByNetworkCtrlDDisconnects(t *testing.T) {
	a, calls := netApp(t, Options{})
	step(a, key("3"))
	step(a, views.NetworksRefreshMsg{Networks: []docker.Network{{ID: "n1", Name: "bridge"}}})
	step(a, key("U"))
	if got := listed(a); got != "db" {
		t.Fatalf("containers on bridge: %q", got)
	}
	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(render(a), "disconnect db from bridge?") {
		t.Fatalf("ctrl-d in a network's used-by did not ask to disconnect:\n%s", render(a))
	}
	runOnce(a, step(a, key("y")))
	got := strings.Join(calls(), "|")
	if !strings.Contains(got, "POST /networks/bridge/disconnect") || strings.Contains(got, "DELETE") {
		t.Errorf("after yes: %s", got)
	}

	// Leaving lifts it: ctrl-d is remove again.
	step(a, key("esc"))
	step(a, key("0"))
	if act, _ := a.activeView().HandleKey("ctrl+d"); act != "confirm_remove_container" {
		t.Errorf("ctrl-d after leaving the used-by list: %q", act)
	}
}

// TestNetworkConnectReadonly: --readonly refuses the picker and the
// disconnect alike.
func TestNetworkConnectReadonly(t *testing.T) {
	a, calls := netApp(t, Options{ReadOnly: true})
	step(a, key("N"))
	if a.view != style.ViewContainers || !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("N in readonly: view %v, err %q", a.view, a.errFlash)
	}
	for _, act := range []string{"connect_network", "confirm_disconnect_network"} {
		a.errFlash = ""
		a.handleAction(act, views.DisconnectParam("bridge", docker.Container{ID: "c3", Name: "db"}))
		if !strings.Contains(a.errFlash, "readonly") || a.confirm.Active() {
			t.Errorf("%s in readonly: err %q", act, a.errFlash)
		}
	}
	if len(calls()) != 0 {
		t.Errorf("readonly called the daemon: %q", calls())
	}
}
