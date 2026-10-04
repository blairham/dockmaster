// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

func xrayContainers() []docker.Container {
	return []docker.Container{
		{
			ID: "c1", Name: "shop-web-1", State: "running", Project: "shop", Service: "web",
			Image: "nginx:1.27", ImageID: "sha256:aaaa", Volumes: []string{"pgdata"},
			Endpoints: []docker.Endpoint{{Network: "shop_default", IP: "172.18.0.2"}},
		},
		{ID: "c2", Name: "registry", State: "exited", Image: "registry:2", ImageID: "sha256:bbbb"},
	}
}

// openXray opens :xray on the fixture.
func openXray(t *testing.T) (*App, *views.XrayView) {
	t.Helper()
	a := newTestApp(t)
	a.dispatchCommand("xray")
	if a.view != style.ViewXray {
		t.Fatalf(":xray opened %v", a.view)
	}
	xv := typedView[*views.XrayView](a, style.ViewXray)
	step(a, views.XrayRefreshMsg{Containers: xrayContainers()})
	return a, xv
}

// gotoNode moves the cursor down to the row whose node ID has the prefix.
func gotoNode(t *testing.T, a *App, xv *views.XrayView, id string) {
	t.Helper()
	for range 20 {
		if n, ok := xv.Selected(); ok && n.ID == id {
			return
		}
		step(a, key("j"))
	}
	t.Fatalf("no node %s", id)
}

// TestXrayTree: :xray shows each project, its services and containers, and
// what every container uses — image, volumes, networks — with standalone
// containers under their own root (#9).
func TestXrayTree(t *testing.T) {
	a, _ := openXray(t)
	out := render(a)
	order := []string{
		"shop", "web", "shop-web-1", "image nginx:1.27", "volume pgdata", "network shop_default 172.18.0.2",
		"standalone containers", "registry", "image registry:2",
	}
	last := -1
	for _, want := range order {
		i := strings.Index(out, want)
		if i < 0 || i < last {
			t.Fatalf("%q missing or out of order:\n%s", want, out)
		}
		last = i
	}
}

// TestXrayEnter: enter acts on the node under the cursor — a container's
// logs, its image's layers, the containers on its network.
func TestXrayEnter(t *testing.T) {
	a, xv := openXray(t)
	gotoNode(t, a, xv, "c:c1")
	step(a, key("enter"))
	if a.view != style.ViewLogs {
		t.Fatalf("enter on a container opened %v", a.view)
	}
	step(a, key("esc"))
	gotoNode(t, a, xv, "c:c1/i")
	step(a, key("enter"))
	if a.view != style.ViewLayers {
		t.Fatalf("enter on an image opened %v", a.view)
	}
	step(a, key("esc"))
	gotoNode(t, a, xv, "c:c1/n:shop_default")
	step(a, key("enter"))
	if a.view != style.ViewContainers || !strings.Contains(render(a), "using network shop_default") {
		t.Errorf("enter on a network opened %v:\n%s", a.view, render(a))
	}
}

// TestXrayKeepsItsPlace: what is collapsed, and the cursor, survive the
// poll's refresh; the filter keeps the path to a match.
func TestXrayKeepsItsPlace(t *testing.T) {
	a, xv := openXray(t)
	gotoNode(t, a, xv, "c:c1")
	step(a, key("h")) // collapse the container
	before := xv.Count()
	step(a, views.XrayRefreshMsg{Containers: xrayContainers()})
	if n, _ := xv.Selected(); xv.Count() != before || n.ID != "c:c1" {
		t.Errorf("a refresh lost the place: %d rows (was %d), cursor %v", xv.Count(), before, n.ID)
	}
	if strings.Contains(render(a), "volume pgdata") {
		t.Error("a refresh reopened a collapsed container")
	}

	a.onFilterChange("pgdata")
	out := render(a)
	for _, want := range []string{"shop", "web", "shop-web-1", "volume pgdata"} {
		if !strings.Contains(out, want) {
			t.Errorf("filter pgdata lost %q on the path:\n%s", want, out)
		}
	}
	if strings.Contains(out, "registry") {
		t.Errorf("filter pgdata kept a non-matching branch:\n%s", out)
	}
}

// xrayFixture is the tree's containers plus a paused one, for p's unpause.
func xrayFixture() []docker.Container {
	return append(xrayContainers(), docker.Container{
		ID: "c3", Name: "zz-paused", State: "paused", Image: "redis:7", ImageID: "sha256:cccc",
	})
}

// openXrayOn opens :xray against a fake daemon, so the actions its keys
// ask for run and can be counted.
func openXrayOn(t *testing.T, readonly bool) (*App, *views.XrayView, func() []string) {
	t.Helper()
	a := newTestApp(t)
	a.readonly = readonly
	c, calls := fakeDaemon(t)
	a.client = c
	a.dispatchCommand("xray")
	xv := typedView[*views.XrayView](a, style.ViewXray)
	step(a, views.XrayRefreshMsg{Containers: xrayFixture()})
	return a, xv, calls
}

// TestXrayKeysAskForTheContainersViewsActions: on each kind of node, every
// container key asks for exactly what the containers view asks for on the
// same container — and on a node that is not a container, for nothing (#56).
func TestXrayKeysAskForTheContainersViewsActions(t *testing.T) {
	a, xv := openXray(t)
	step(a, views.XrayRefreshMsg{Containers: xrayFixture()})
	type want struct{ action, param string }
	cases := []struct {
		keys map[string]want
		node string
	}{
		{node: "c:c1", keys: map[string]want{
			"s": {"exec", "c1"}, "A": {"attach", "c1"},
			"u": {"start", "c1"}, "x": {"stop", "c1"}, "R": {"restart", "c1"},
			"K": {"kill", "c1"}, "p": {"pause", "c1"}, "ctrl+d": {"confirm_remove_container", "c1"},
			"v":     {"scan_image", "sha256:aaaa"},
			"enter": {"logs", "c1"}, "o": {"inspect_container", "c1"},
		}},
		{node: "c:c2", keys: map[string]want{
			"A": {"not_running", "registry"}, "u": {"start", "c2"}, "p": {"pause", "c2"},
		}},
		{node: "c:c3", keys: map[string]want{"p": {"unpause", "c3"}, "A": {"not_running", "zz-paused"}}},
		{node: "c:c1/i", keys: map[string]want{
			"v": {"scan_image", "sha256:aaaa"}, "enter": {"layers", "sha256:aaaa"},
			"s": {}, "A": {}, "u": {}, "x": {}, "R": {}, "K": {}, "p": {}, "ctrl+d": {"xray_nav", ""},
		}},
		{node: "c:c1/v:pgdata", keys: map[string]want{
			"v": {}, "s": {}, "u": {}, "x": {}, "K": {}, "p": {}, "ctrl+d": {"xray_nav", ""},
		}},
		{node: "c:c1/n:shop_default", keys: map[string]want{"v": {}, "s": {}, "x": {}, "ctrl+d": {"xray_nav", ""}}},
		{node: "p:shop", keys: map[string]want{
			"v": {}, "s": {"scale_form", "shop"}, "u": {}, "x": {}, "K": {}, "ctrl+d": {"xray_nav", ""},
		}},
		{node: "p:shop/s:web", keys: map[string]want{
			"v": {}, "s": {"scale_form", "shop\x00web"}, "x": {}, "ctrl+d": {"xray_nav", ""},
		}},
		{node: "standalone", keys: map[string]want{"s": {}, "x": {}, "ctrl+d": {"xray_nav", ""}}},
	}
	for _, tc := range cases {
		gotoNode(t, a, xv, tc.node)
		for k, w := range tc.keys {
			if act, param := xv.HandleKey(k); act != w.action || param != w.param {
				t.Errorf("%s on %s = (%q, %q), want (%q, %q)", k, tc.node, act, param, w.action, w.param)
			}
		}
		// Back to the top, so the next node is reached going down.
		for range 20 {
			step(a, key("k"))
		}
	}
}

// TestXrayLifecycleKeysRunAndRefresh: u, x, R and p on a container node
// reach the daemon through the app, and the tree refreshes afterwards,
// keeping its place.
func TestXrayLifecycleKeysRunAndRefresh(t *testing.T) {
	for _, tc := range []struct{ node, key, call string }{
		{node: "c:c2", key: "u", call: "POST /containers/c2/start"},
		{node: "c:c1", key: "x", call: "POST /containers/c1/stop"},
		{node: "c:c1", key: "R", call: "POST /containers/c1/restart"},
		{node: "c:c1", key: "p", call: "POST /containers/c1/pause"},
		{node: "c:c3", key: "p", call: "POST /containers/c3/unpause"},
	} {
		a, xv, calls := openXrayOn(t, false)
		gotoNode(t, a, xv, tc.node)
		cmd := step(a, key(tc.key))
		if cmd == nil {
			t.Fatalf("%s on %s: no command (err %q)", tc.key, tc.node, a.errFlash)
		}
		refresh := step(a, cmd())
		if got := strings.Join(calls(), "|"); got != tc.call {
			t.Errorf("%s on %s called %q, want %q", tc.key, tc.node, got, tc.call)
		}
		if refresh == nil {
			t.Fatalf("%s on %s: the tree did not refresh", tc.key, tc.node)
		}
		if _, ok := refresh().(views.XrayRefreshMsg); !ok {
			t.Errorf("%s on %s: the refresh after it was not the tree's", tc.key, tc.node)
		}
		if !strings.Contains(a.flash, "zz-paused") && !strings.Contains(a.flash, "shop-web-1") &&
			!strings.Contains(a.flash, "registry") {
			t.Errorf("%s on %s: flash %q does not name the container", tc.key, tc.node, a.flash)
		}
		step(a, views.XrayRefreshMsg{Containers: xrayFixture()})
		if n, ok := xv.Selected(); !ok || n.ID != tc.node {
			t.Errorf("%s on %s: after the refresh the cursor is on %v", tc.key, tc.node, n)
		}
	}
}

// TestXrayDestructiveKeysAsk: K and ctrl-d on a container node ask first,
// naming the container, and act only on yes.
func TestXrayDestructiveKeysAsk(t *testing.T) {
	for _, tc := range []struct{ key, prompt, call string }{
		{key: "K", prompt: "SIGKILL shop-web-1?", call: "POST /containers/c1/kill"},
		{key: "ctrl+d", prompt: "remove container shop-web-1?", call: "DELETE /containers/c1"},
	} {
		a, xv, calls := openXrayOn(t, false)
		gotoNode(t, a, xv, "c:c1")
		step(a, key(tc.key))
		if !a.confirm.Active() || a.confirm.Prompt() != tc.prompt {
			t.Fatalf("%s: confirm %v %q, want %q", tc.key, a.confirm.Active(), a.confirm.Prompt(), tc.prompt)
		}
		if len(calls()) != 0 {
			t.Fatalf("%s acted before the answer: %q", tc.key, calls())
		}
		runOnce(a, step(a, key("y")))
		if got := strings.Join(calls(), "|"); got != tc.call {
			t.Errorf("%s after yes called %q, want %q", tc.key, got, tc.call)
		}
	}
}

// TestXrayShellAttachCopyScan: s and A hand the terminal to docker on the
// container, c and i copy its name and ID, and v scans an image node's
// image.
func TestXrayShellAttachCopyScan(t *testing.T) {
	fakeDockerCLI(t)
	a, xv := openXray(t)
	a.client = &docker.Client{Host: "unix:///run/fake.sock"}
	var ran []string
	a.execProcess = func(c *exec.Cmd, _ tea.ExecCallback) tea.Cmd {
		ran = append(ran, strings.Join(c.Args, " "))
		return nil
	}
	gotoNode(t, a, xv, "c:c1")
	step(a, key("s"))
	step(a, key("A"))
	if len(ran) != 2 || !strings.Contains(ran[0], "exec -it c1") || !strings.Contains(ran[1], "c1") ||
		!strings.Contains(ran[1], "attach") {
		t.Errorf("s then A ran %q", ran)
	}

	if cmd := step(a, key("c")); cmd == nil || a.flash != "copied shop-web-1" {
		t.Errorf("c on a container: flash %q, cmd %v", a.flash, cmd != nil)
	}
	if cmd := step(a, key("i")); cmd == nil || a.flash != "copied ID c1" {
		t.Errorf("i on a container: flash %q", a.flash)
	}
	if a.view != style.ViewXray {
		t.Fatalf("copying left xray for %v", a.view)
	}

	gotoNode(t, a, xv, "c:c1/i")
	step(a, key("v"))
	if a.view != style.ViewScan {
		t.Errorf("v on an image node opened %v", a.view)
	}
}

// TestXrayKeysOffAContainerAreHarmless: the container keys on a project, a
// volume or a network do nothing — no command, no confirm, the view stays —
// and copy copies that node's name.
func TestXrayKeysOffAContainerAreHarmless(t *testing.T) {
	a, xv, calls := openXrayOn(t, false)
	for _, node := range []string{"p:shop", "p:shop/s:web", "c:c1/v:pgdata", "c:c1/n:shop_default"} {
		gotoNode(t, a, xv, node)
		for _, k := range []string{"s", "A", "u", "x", "R", "K", "p", "ctrl+d", "v"} {
			runOnce(a, step(a, key(k)))
			if a.confirm.Active() || a.view != style.ViewXray {
				t.Fatalf("%s on %s: confirm %v, view %v", k, node, a.confirm.Active(), a.view)
			}
			if n, _ := xv.Selected(); n.ID != node {
				t.Fatalf("%s on %s moved the cursor to %s", k, node, n.ID)
			}
		}
		for range 20 {
			step(a, key("k"))
		}
	}
	for _, c := range calls() {
		if !strings.HasPrefix(c, "GET ") {
			t.Errorf("a container key off a container reached the daemon: %s", c)
		}
	}
	gotoNode(t, a, xv, "c:c1/n:shop_default")
	step(a, key("c"))
	if a.flash != "copied shop_default" {
		t.Errorf("c on a network: %q", a.flash)
	}
	step(a, key("i"))
	if !strings.Contains(a.errFlash, "no ID") {
		t.Errorf("i on a network: %q", a.errFlash)
	}
	// The standalone root names nothing to copy.
	for range 20 {
		step(a, key("k"))
	}
	gotoNode(t, a, xv, "standalone")
	if cmd := step(a, key("c")); cmd != nil || a.errFlash != "nothing selected to copy" {
		t.Errorf("c on the standalone root: flash %q, err %q", a.flash, a.errFlash)
	}

	// h and l still fold: the container keys took nothing from the tree.
	for range 20 {
		step(a, key("k"))
	}
	gotoNode(t, a, xv, "c:c1")
	before := xv.Count()
	step(a, key("h"))
	step(a, key("l"))
	if xv.Count() != before || !strings.Contains(render(a), "volume pgdata") {
		t.Errorf("h then l did not reopen the container: %d rows, was %d", xv.Count(), before)
	}
}

// TestXrayReadonly: --readonly refuses every mutating container key on the
// tree before it asks or acts; logs and inspect still open.
func TestXrayReadonly(t *testing.T) {
	a, xv, calls := openXrayOn(t, true)
	ran := false
	a.execProcess = func(*exec.Cmd, tea.ExecCallback) tea.Cmd { ran = true; return nil }
	gotoNode(t, a, xv, "c:c1")
	for _, k := range []string{"s", "A", "u", "x", "R", "K", "p", "ctrl+d"} {
		runOnce(a, step(a, key(k)))
		if !strings.Contains(a.errFlash, "readonly") || a.confirm.Active() {
			t.Errorf("%s in readonly: err %q, confirm %v", k, a.errFlash, a.confirm.Active())
		}
	}
	if ran || len(calls()) != 0 {
		t.Errorf("readonly still acted: terminal %v, calls %q", ran, calls())
	}
	step(a, key("o"))
	if a.view != style.ViewInspect {
		t.Errorf("o in readonly opened %v", a.view)
	}
}
