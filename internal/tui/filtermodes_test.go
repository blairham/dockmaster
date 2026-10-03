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

func labeled() []docker.Container {
	svc := func(s string) map[string]string {
		return map[string]string{docker.LabelProject: "shop", docker.LabelService: s, "tier": "front"}
	}
	return []docker.Container{
		{ID: "c1", Name: "shop-web-1", State: "running", Labels: svc("web")},
		{ID: "c2", Name: "shop-worker-1", State: "running", Labels: map[string]string{docker.LabelService: "worker"}},
		{ID: "c3", Name: "db", State: "running"},
	}
}

// TestLabelAndFuzzyFilters: the filter bar takes k9s's -l and -f (#7). -l
// reads each container's labels — compose's project and service among
// them — and -f matches a name with letters left out.
func TestLabelAndFuzzyFilters(t *testing.T) {
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: labeled()})
	for _, c := range []struct{ filter, want string }{
		{filter: "-l com.docker.compose.service=web", want: "shop-web-1"},
		{filter: "-l tier", want: "shop-web-1"},
		{filter: "-l !tier", want: "shop-worker-1,db"},
		{filter: "-l com.docker.compose.service", want: "shop-web-1,shop-worker-1"},
		{filter: "-f swrk", want: "shop-worker-1"},
		{filter: "web", want: "shop-web-1"},
	} {
		a.onFilterChange(c.filter)
		if got := listed(a); got != c.want {
			t.Errorf("%q lists %q, want %q", c.filter, got, c.want)
		}
	}
}

// TestLabelFilterOnImagesVolumesNetworks: the other labeled lists answer
// -l from their own labels.
func TestLabelFilterOnImagesVolumesNetworks(t *testing.T) {
	a := newTestApp(t)
	step(a, key("2"))
	step(a, views.VolumesRefreshMsg{Volumes: []docker.Volume{
		{Name: "pgdata", Labels: map[string]string{"backup": "daily"}}, {Name: "cache"},
	}})
	a.onFilterChange("-l backup=daily")
	if n := a.activeView().Count(); n != 1 {
		t.Errorf("volumes with backup=daily: %d rows, want 1", n)
	}
	step(a, key("3"))
	step(a, views.NetworksRefreshMsg{Networks: []docker.Network{
		{ID: "n1", Name: "shop_default", Labels: map[string]string{docker.LabelProject: "shop"}}, {ID: "n2", Name: "bridge"},
	}})
	a.onFilterChange("-l com.docker.compose.project=shop")
	if n := a.activeView().Count(); n != 1 {
		t.Errorf("networks in project shop: %d rows, want 1", n)
	}
	step(a, key("1"))
	step(a, views.ImagesRefreshMsg{Images: []docker.Image{
		{ID: "sha256:a", Repo: "app", Tag: "1", Labels: map[string]string{"org.opencontainers.image.source": "x"}},
		{ID: "sha256:b", Repo: "nginx", Tag: "1"},
	}})
	a.onFilterChange("-l org.opencontainers.image.source")
	if n := a.activeView().Count(); n != 1 {
		t.Errorf("images with a source label: %d rows, want 1", n)
	}
}

// TestLabelFilterWhereThereAreNoLabels: in a view without labels -l would
// empty the list, so the app says why instead of leaving it to look broken.
func TestLabelFilterWhereThereAreNoLabels(t *testing.T) {
	a, _, _ := newComposeApp(t, Options{}, true)
	if a.view != style.ViewProjects {
		t.Fatalf("setup: on %v", a.view)
	}
	a.onFilterChange("-l app=web")
	if !strings.Contains(a.flash, "-l filters labels") {
		t.Errorf("no hint for -l on projects; flash %q", a.flash)
	}
	b := newTestApp(t)
	b.onFilterChange("-l app=web")
	if strings.Contains(b.flash, "-l filters labels") {
		t.Errorf("hint shown on containers, which have labels: %q", b.flash)
	}
}
