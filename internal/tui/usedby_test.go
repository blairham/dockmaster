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

// usageContainers: web runs nginx with the pgdata volume on the shop
// network; worker runs nginx too but is stopped; db runs postgres.
func usageContainers() []docker.Container {
	return []docker.Container{
		{
			ID: "c1", Name: "web", Image: "nginx:1.27", ImageID: "sha256:aaaa", State: "running",
			Volumes: []string{"pgdata"}, Endpoints: []docker.Endpoint{{Network: "shop"}}, Project: "shop",
		},
		{ID: "c2", Name: "worker", Image: "nginx:1.27", ImageID: "sha256:aaaa", State: "exited"},
		{
			ID: "c3", Name: "db", Image: "postgres:17", ImageID: "sha256:cccc", State: "running",
			Endpoints: []docker.Endpoint{{Network: "bridge"}},
		},
	}
}

// listed is the names the containers view shows, in order.
func listed(a *App) string {
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	names := make([]string, 0, cv.Count())
	for range cv.Count() {
		c, _ := cv.Selected()
		names = append(names, c.Name)
		step(a, key("j"))
	}
	for range cv.Count() {
		step(a, key("k"))
	}
	return strings.Join(names, ",")
}

// TestUsedByImage: U on an image lists exactly the containers created from
// it — by image ID, stopped ones included — titled with what they use; esc
// goes back to the images, and the containers view lists everything again
// on the next visit (#8).
func TestUsedByImage(t *testing.T) {
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: usageContainers()})
	loadImages(a) // nginx is sha256:aaaa
	step(a, key("U"))
	if a.view != style.ViewContainers {
		t.Fatalf("U opened %v", a.view)
	}
	if got := listed(a); got != "web,worker" && got != "worker,web" {
		t.Errorf("users of nginx: %q, want web and the stopped worker", got)
	}
	if out := render(a); !strings.Contains(out, "using image nginx") {
		t.Errorf("title does not say what is shown:\n%s", out)
	}
	step(a, key("esc"))
	if a.view != style.ViewImages {
		t.Fatalf("esc went to %v, want back to images", a.view)
	}
	step(a, key("0"))
	if cv := typedView[*views.ContainersView](a, style.ViewContainers); cv.Scope() != "" || cv.Count() != 3 {
		t.Errorf("after leaving, scope %q and %d rows; want the full list", cv.Scope(), cv.Count())
	}
}

// TestUsedByVolumeAndNetwork: U on a volume or a network matches mounts and
// attachments by name.
func TestUsedByVolumeAndNetwork(t *testing.T) {
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: usageContainers()})
	step(a, key("2"))
	step(a, views.VolumesRefreshMsg{Volumes: []docker.Volume{{Name: "pgdata"}}})
	step(a, key("U"))
	if got := listed(a); got != "web" {
		t.Errorf("users of volume pgdata: %q", got)
	}
	step(a, key("esc"))

	step(a, key("3"))
	step(a, views.NetworksRefreshMsg{Networks: []docker.Network{{ID: "n1", Name: "bridge"}}})
	step(a, key("U"))
	if got := listed(a); got != "db" {
		t.Errorf("containers on network bridge: %q", got)
	}
}

// TestJumpToProject: J on a compose container opens the projects view on its
// project; on a container that is not in one it says so.
func TestJumpToProject(t *testing.T) {
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: usageContainers()})
	for c, _ := typedView[*views.ContainersView](a, style.ViewContainers).Selected(); c.Name != "web"; c, _ = typedView[*views.ContainersView](a, style.ViewContainers).Selected() {
		step(a, key("j"))
	}
	step(a, key("J"))
	if a.view != style.ViewProjects || a.filter != "^shop$" {
		t.Errorf("J opened %v filtered %q, want projects on shop", a.view, a.filter)
	}

	b := newTestApp(t)
	step(b, views.ContainersRefreshMsg{Containers: usageContainers()})
	for c, _ := typedView[*views.ContainersView](b, style.ViewContainers).Selected(); c.Name != "db"; c, _ = typedView[*views.ContainersView](b, style.ViewContainers).Selected() {
		step(b, key("j"))
	}
	step(b, key("J"))
	if b.view != style.ViewContainers || !strings.Contains(b.errFlash, "not part of a compose project") {
		t.Errorf("J on a plain container: view %v err %q", b.view, b.errFlash)
	}
}
