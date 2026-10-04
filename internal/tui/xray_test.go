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
