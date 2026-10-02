// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// wideContainers is the sample with a command, network and address.
func wideApp(t *testing.T) (*App, *views.ContainersView) {
	t.Helper()
	cs := sampleContainers()
	cs[0].Command, cs[0].Network, cs[0].IP = "nginx -g daemon off;", "shop_default", "172.18.0.2"
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: cs})
	return a, typedView[*views.ContainersView](a, style.ViewContainers)
}

func TestWideColumns(t *testing.T) {
	a, v := wideApp(t)
	if out := render(a); strings.Contains(out, "COMMAND") || strings.Contains(out, "NETWORKS") {
		t.Fatalf("wide columns before ctrl+w:\n%s", out)
	}
	step(a, key("ctrl+w"))
	assertFrameFits(t, a, "wide containers at 160 columns")
	// The extra columns squeeze the text ones to their floor at 160; check
	// the content where there is room for it.
	step(a, tea.WindowSizeMsg{Width: 230, Height: 44})
	out := render(a)
	for _, want := range []string{"ID", "COMMAND", "NETWORKS", "IP", "aaaaaaaaaaaa", "nginx -g daemon", "shop_default", "172.18.0.2"} {
		if !strings.Contains(out, want) {
			t.Errorf("wide view lacks %q:\n%s", want, out)
		}
	}
	if a.flash != "wide columns on" || !v.Wide() {
		t.Errorf("flash %q wide %v", a.flash, v.Wide())
	}
	if c, _ := v.Selected(); c.Name != "web" {
		t.Errorf("cursor selects %q after ctrl+w, want web", c.Name)
	}
	step(a, key("ctrl+w"))
	if out := render(a); strings.Contains(out, "COMMAND") || a.flash != "wide columns off" {
		t.Errorf("ctrl+w again (flash %q):\n%s", a.flash, out)
	}
}

// TestWideKeepsTheSort: the sort follows its column by name across the
// toggle, and falls back to the first column when its column goes away.
func TestWideKeepsTheSort(t *testing.T) {
	a, _ := wideApp(t)
	step(a, key("shift+right")) // sort by NAME
	step(a, key("shift+right")) // then IMAGE
	step(a, key("shift+down"))  // descending
	step(a, key("ctrl+w"))
	if out := render(a); !strings.Contains(out, "IMAGE↓") || strings.Contains(out, "ID↓") {
		t.Errorf("the sort did not follow IMAGE into the wide columns:\n%s", out)
	}
	for range 7 { // over to NETWORKS
		step(a, key("shift+right"))
	}
	if out := render(a); !strings.Contains(out, "NETWORKS↑") {
		t.Fatalf("setup: sort not on NETWORKS:\n%s", out)
	}
	step(a, key("ctrl+w"))
	if out := render(a); !strings.Contains(out, "NAME↑") {
		t.Errorf("a sort on a column that went away did not fall back to NAME:\n%s", out)
	}
}
