// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// labelApp is an app whose containers view lists one container, web, with
// the given labels, and records forward starts instead of running helpers.
func labelApp(t *testing.T, opts Options, labels map[string]string) (*App, *[]startedForward) {
	t.Helper()
	a := newSizedApp(t, opts, 200, 30)
	a.splashActive = false
	started := fakeStarts(a)
	step(a, views.ContainersRefreshMsg{Containers: []docker.Container{{
		ID: "web1", Name: "shop-web-1", Service: "web", State: "running", Labels: labels,
		Endpoints: []docker.Endpoint{{Network: "shop_default", IP: "172.20.0.5"}},
		PortList:  []docker.PortMapping{{Type: "tcp", Private: 3000}},
	}}})
	return a, started
}

func startsString(started []startedForward) string {
	parts := make([]string, 0, len(started))
	for _, s := range started {
		parts = append(parts, s.target+" "+docker.ForwardSpec{Local: s.local, Remote: s.remote}.String())
	}
	return strings.Join(parts, ", ")
}

// TestForwardLabelPresetsThePrompt: dockmaster.port-forwards fills the
// prompt — the user still sees it and presses enter — and every forward in
// it starts.
func TestForwardLabelPresetsThePrompt(t *testing.T) {
	a, started := labelApp(t, Options{}, map[string]string{
		docker.LabelPortForwards: "web::8080:80, 8443:443",
	})
	step(a, key("F"))
	if !a.prompt.Active() || a.prompt.Value() != "8080:80,8443:443" || a.confirm.Active() {
		t.Fatalf("prompt active %v value %q confirm %v; want the label's forwards in the prompt",
			a.prompt.Active(), a.prompt.Value(), a.confirm.Active())
	}
	if len(*started) != 0 {
		t.Fatalf("a preset started %v before enter", *started)
	}
	runOnce(a, step(a, key("enter")))
	if got := startsString(*started); got != "shop-web-1 8080:80, shop-web-1 8443:443" {
		t.Errorf("started %q", got)
	}
	if len(a.forwards) != 2 {
		t.Errorf("session forwards %v, want both", a.forwards)
	}
}

// TestForwardAutoLabelConfirms: dockmaster.auto-port-forwards skips the
// prompt but not the user: a confirm names the forwards and the label, no
// starts nothing, yes starts them all.
func TestForwardAutoLabelConfirms(t *testing.T) {
	a, started := labelApp(t, Options{}, map[string]string{
		docker.LabelAutoPortForwards: "8080:80,8443:443",
		docker.LabelPortForwards:     "9999:99",
	})
	step(a, key("F"))
	if a.prompt.Active() || !a.confirm.Active() {
		t.Fatalf("prompt %v confirm %v; want a confirm and no prompt", a.prompt.Active(), a.confirm.Active())
	}
	q := a.confirm.Prompt()
	for _, want := range []string{"shop-web-1", "8080:80 8443:443", "on localhost", docker.LabelAutoPortForwards} {
		if !strings.Contains(q, want) {
			t.Errorf("confirm %q does not say %q", q, want)
		}
	}
	if strings.Contains(q, "network") {
		t.Errorf("loopback confirm warns about the network: %q", q)
	}
	runOnce(a, step(a, key("n")))
	if len(*started) != 0 || a.confirm.Active() {
		t.Fatalf("no started %v (confirm still open %v)", *started, a.confirm.Active())
	}
	step(a, key("F"))
	runOnce(a, step(a, key("y")))
	if got := startsString(*started); got != "shop-web-1 8080:80, shop-web-1 8443:443" {
		t.Errorf("yes started %q", got)
	}
}

// TestForwardBadLabelIsReported: a label that does not parse is reported in
// the prompt, which opens as if there were no label — prefilled with the
// container's own port, never with any part of the label — and nothing
// starts until the user enters a spec the prompt accepts.
func TestForwardBadLabelIsReported(t *testing.T) {
	for _, tc := range []struct {
		labels map[string]string
		name   string
		label  string
	}{
		{
			name: "bad auto", label: docker.LabelAutoPortForwards,
			labels: map[string]string{docker.LabelAutoPortForwards: "8080:80,70000"},
		},
		{
			name: "auto for another container", label: docker.LabelAutoPortForwards,
			labels: map[string]string{docker.LabelAutoPortForwards: "db::5432"},
		},
		{
			name: "bad preset", label: docker.LabelPortForwards,
			labels: map[string]string{docker.LabelPortForwards: "web::http"},
		},
		{
			name: "hostile", label: docker.LabelAutoPortForwards,
			labels: map[string]string{docker.LabelAutoPortForwards: "\x1b]52;c;aGk=\x07:80"},
		},
		{
			name: "huge", label: docker.LabelAutoPortForwards,
			labels: map[string]string{docker.LabelAutoPortForwards: strings.Repeat("1:1,", 1000)},
		},
	} {
		a, started := labelApp(t, Options{}, tc.labels)
		step(a, key("F"))
		if a.confirm.Active() || !a.prompt.Active() {
			t.Errorf("%s: confirm %v prompt %v; want the plain prompt", tc.name, a.confirm.Active(), a.prompt.Active())
			continue
		}
		if a.prompt.Value() != "3000:3000" {
			t.Errorf("%s: prompt prefilled %q, want the container's own 3000:3000", tc.name, a.prompt.Value())
		}
		if msg := a.prompt.ErrMsg(); !strings.Contains(msg, tc.label) || strings.ContainsAny(msg, "\x1b\x07") {
			t.Errorf("%s: prompt error %q should name %s, with no raw control", tc.name, msg, tc.label)
		}
		if out := render(a); strings.ContainsAny(out, "\x1b\x07") {
			t.Errorf("%s: a raw control reached the frame", tc.name)
		}
		if len(*started) != 0 {
			t.Errorf("%s: a bad label started %v", tc.name, *started)
		}
		runOnce(a, step(a, key("enter")))
		if got := startsString(*started); got != "shop-web-1 3000:3000" {
			t.Errorf("%s: enter started %q", tc.name, got)
		}
	}
}

// TestForwardLabelReadonly: --readonly refuses shift-f before any label is
// read, so an auto label opens no confirm.
func TestForwardLabelReadonly(t *testing.T) {
	a, started := labelApp(t, Options{ReadOnly: true}, map[string]string{docker.LabelAutoPortForwards: "8080:80"})
	step(a, key("F"))
	if a.confirm.Active() || a.prompt.Active() || len(*started) != 0 || !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("readonly: confirm %v prompt %v started %v flash %q",
			a.confirm.Active(), a.prompt.Active(), *started, a.errFlash)
	}
}

// TestForwardAddressWarns: with portForwardAddress off loopback the prompt
// and the auto confirm say where the forward listens and that the network
// can reach it, and the forward is started there. Loopback, v4 or v6, says
// nothing.
func TestForwardAddressWarns(t *testing.T) {
	for _, tc := range []struct {
		addr string
		warn bool
	}{
		{addr: "", warn: false},
		{addr: "127.0.0.1", warn: false},
		{addr: "::1", warn: false},
		{addr: "0.0.0.0", warn: true},
		{addr: "192.168.1.20", warn: true},
	} {
		addr, _ := netip.ParseAddr(tc.addr)
		a, started := labelApp(t, Options{ForwardAddress: addr}, nil)
		step(a, key("F"))
		out := render(a)
		if got := strings.Contains(out, "reachable from the network"); got != tc.warn {
			t.Errorf("%q: prompt warns %v, want %v:\n%s", tc.addr, got, tc.warn, out)
		}
		if tc.warn && !strings.Contains(out, "on "+tc.addr) {
			t.Errorf("%q: prompt does not name the address:\n%s", tc.addr, out)
		}
		runOnce(a, step(a, key("enter")))
		if len(*started) != 1 || (*started)[0].addr != addr {
			t.Errorf("%q: started %+v, want one on %v", tc.addr, *started, addr)
		}

		b, _ := labelApp(t, Options{ForwardAddress: addr}, map[string]string{docker.LabelAutoPortForwards: "8080:80"})
		step(b, key("F"))
		if got := strings.Contains(b.confirm.Prompt(), "reachable from the network"); got != tc.warn {
			t.Errorf("%q: auto confirm %q warns %v, want %v", tc.addr, b.confirm.Prompt(), got, tc.warn)
		}
	}
}

// TestForwardAddressStartedFlash: the flash and :pf name a forward on
// another address by it, so it never reads as localhost.
func TestForwardAddressStartedFlash(t *testing.T) {
	a, _ := labelApp(t, Options{ForwardAddress: netip.MustParseAddr("0.0.0.0")}, nil)
	step(a, key("F"))
	runOnce(a, step(a, key("enter")))
	if !strings.Contains(a.flash, "0.0.0.0:3000 → shop-web-1:3000") {
		t.Errorf("flash %q", a.flash)
	}
}

// TestForwardAddressReloads: a saved portForwardAddress applies to the next
// forward under ui.reactive.
func TestForwardAddressReloads(t *testing.T) {
	a, started := labelApp(t, Options{}, nil)
	a.applyReload(reloadedMsg{r: Reloaded{
		Theme:   style.Base(),
		Options: Options{ForwardAddress: netip.MustParseAddr("0.0.0.0")},
	}})
	step(a, key("F"))
	runOnce(a, step(a, key("enter")))
	if len(*started) != 1 || (*started)[0].addr.String() != "0.0.0.0" {
		t.Errorf("after reload started %+v, want one on 0.0.0.0", *started)
	}
}

// TestShowForwardsScopes: f on a container opens :pf narrowed to that
// container's forwards, named in the title; esc goes back and the
// narrowing lifts, so :pf lists every forward again.
func TestShowForwardsScopes(t *testing.T) {
	a, _ := forwardApp(t, Options{})
	all := views.PortForwardsRefreshMsg{Forwards: []docker.PortForward{
		{ID: "h1", Target: "reg1", TargetName: "registry", Local: 15000, Remote: 5000, State: "running"},
		{ID: "h2", Target: "db1", TargetName: "db", Local: 15432, Remote: 5432, State: "running"},
		{ID: "h3", Target: "reg1", TargetName: "registry", Local: 15001, Remote: 5001, State: "running"},
	}}
	step(a, key("f"))
	pv := typedView[*views.PortForwardsView](a, style.ViewPortForwards)
	if a.view != style.ViewPortForwards || pv == nil {
		t.Fatalf("f opened %v", a.view)
	}
	step(a, all)
	if pv.Count() != 2 || pv.Status() != "to registry" {
		t.Errorf("scoped to registry: %d rows, status %q; want 2, \"to registry\"", pv.Count(), pv.Status())
	}
	if out := render(a); strings.Contains(out, "15432") || !strings.Contains(out, "to registry") {
		t.Errorf("scoped frame:\n%s", out)
	}
	step(a, key("esc"))
	if a.view != style.ViewContainers {
		t.Fatalf("esc went to %v, want containers", a.view)
	}
	if pv.Count() != 3 || pv.Status() != "" {
		t.Errorf("after esc: %d rows, status %q; want the scope lifted", pv.Count(), pv.Status())
	}

	// db has none: the list is empty and says whose it is.
	step(a, key("j"))
	step(a, key("f"))
	step(a, views.PortForwardsRefreshMsg{})
	if out := render(a); !strings.Contains(out, "no port forwards to db") {
		t.Errorf("empty scoped list:\n%s", out)
	}
	a.dispatchCommand("pf")
	step(a, all)
	if pv.Count() != 3 {
		t.Errorf(":pf after f lists %d, want every forward", pv.Count())
	}
}

// TestShowForwardsIsNotReadonly: f only looks.
func TestShowForwardsIsNotReadonly(t *testing.T) {
	a, _ := forwardApp(t, Options{ReadOnly: true})
	step(a, key("f"))
	if a.view != style.ViewPortForwards {
		t.Errorf("f in readonly opened %v (flash %q)", a.view, a.errFlash)
	}
}
