// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"maps"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// TestJumpsValidation: a jump starts from a view whose rows it can read,
// opens a view, has exactly one of a label selector (for a labeled
// target) or a filter, names only variables its rows give, and is the one
// jump of its view. Each mistake is named.
func TestJumpsValidation(t *testing.T) {
	good := map[string]config.Jump{
		"containers": {TargetView: "volumes", LabelSelector: "com.docker.compose.project=$PROJECT"},
		"images":     {TargetView: "ps", Filter: "$IMAGE"},
		"networks":   {TargetView: "containers", Filter: "-f $NAME-1"},
	}
	js, err := Jumps(good)
	if err != nil || len(js) != 3 {
		t.Fatalf("good jumps: %+v %v", js, err)
	}
	for _, j := range js {
		if j.From == style.ViewContainers && (!j.Label || j.To != style.ViewVolumes) {
			t.Errorf("containers jump %+v", j)
		}
		if j.From == style.ViewImages && (j.Label || j.To != style.ViewContainers) {
			t.Errorf("images jump %+v", j)
		}
	}
	for _, tc := range []struct {
		entries map[string]config.Jump
		want    string
	}{
		{entries: map[string]config.Jump{"events": {TargetView: "containers", Filter: "x"}}, want: "a jump starts from"},
		{entries: map[string]config.Jump{"bogus": {TargetView: "containers", Filter: "x"}}, want: "a jump starts from"},
		{entries: map[string]config.Jump{"images": {TargetView: "nope", Filter: "x"}}, want: `targetView "nope" is not a view`},
		{entries: map[string]config.Jump{"images": {TargetView: "containers"}}, want: "one of labelSelector or filter"},
		{
			entries: map[string]config.Jump{"images": {TargetView: "containers", Filter: "x", LabelSelector: "a=b"}},
			want:    "one of labelSelector or filter",
		},
		{
			entries: map[string]config.Jump{"images": {TargetView: "projects", LabelSelector: "a=$NAME"}},
			want:    "projects rows have no labels",
		},
		{
			entries: map[string]config.Jump{"images": {TargetView: "containers", LabelSelector: "=$NAME"}},
			want:    "has no label key",
		},
		{
			entries: map[string]config.Jump{"images": {TargetView: "containers", LabelSelector: "my key=$NAME"}},
			want:    "has no label key",
		},
		{
			entries: map[string]config.Jump{"images": {TargetView: "containers", LabelSelector: " , "}},
			want:    "no terms",
		},
		{
			entries: map[string]config.Jump{"images": {TargetView: "containers", Filter: "-l a=$NAME"}},
			want:    "a label filter is labelSelector",
		},
		{
			entries: map[string]config.Jump{"volumes": {TargetView: "containers", Filter: "$PROJECT"}},
			want:    "$PROJECT is not a variable volume rows give",
		},
		{
			entries: map[string]config.Jump{"containers": {TargetView: "images", Filter: "${IMAGE-x}"}},
			want:    "$IMAGE-x is not a variable",
		},
		{
			entries: map[string]config.Jump{
				"containers": {TargetView: "images", Filter: "$IMAGE"},
				"ps":         {TargetView: "volumes", Filter: "$NAME"},
			},
			want: `"containers" is the same view`,
		},
	} {
		if _, err := Jumps(tc.entries); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err %v, want %q", tc.entries, err, tc.want)
		}
	}
}

// jumpApp is the containers view with jumps.
func jumpApp(t *testing.T, entries map[string]config.Jump) *App {
	t.Helper()
	js, err := Jumps(entries)
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(nil, Options{Version: "test", Jumps: js})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 170, Height: 40})
	loadContainers(a)
	return a
}

func shopVolumes() []docker.Volume {
	return []docker.Volume{
		{Name: "shop_pgdata", Driver: "local", Labels: map[string]string{"com.docker.compose.project": "shop"}},
		{Name: "other_cache", Driver: "local", Labels: map[string]string{"com.docker.compose.project": "other"}},
	}
}

// TestJumpThroughTheApp: enter on a container with a jump opens the
// target view over it, filtered by the selector with the row's value
// filled in; esc comes back. The header and help say what enter does.
func TestJumpThroughTheApp(t *testing.T) {
	a := jumpApp(t, map[string]config.Jump{
		"containers": {TargetView: "volumes", LabelSelector: "com.docker.compose.project=$PROJECT"},
	})
	header := strings.Join(strings.Split(render(a), "\n")[:6], "\n")
	if !strings.Contains(header, "Jump volumes") || strings.Contains(header, "Logs") {
		t.Errorf("the header does not show the jump on enter in place of Logs:\n%s", header)
	}
	if c, _ := typedView[*views.ContainersView](a, style.ViewContainers).Selected(); c.Project != "shop" {
		t.Fatalf("fixture: cursor on %q (%q)", c.Name, c.Project)
	}
	step(a, key("enter"))
	if a.view != style.ViewVolumes || a.filter != "-l com.docker.compose.project=shop" {
		t.Fatalf("enter opened %v filtered %q, want volumes filtered by the project", a.view, a.filter)
	}
	step(a, views.VolumesRefreshMsg{Volumes: shopVolumes()})
	if out := render(a); !strings.Contains(out, "shop_pgdata") || strings.Contains(out, "other_cache") {
		t.Errorf("the jump's selector did not filter the volumes:\n%s", out)
	}
	step(a, key("?"))
	if out := render(a); strings.Contains(out, "Jump to volumes") {
		t.Errorf("help over the volumes view shows the containers view's jump:\n%s", out)
	}
	step(a, key("esc"))
	step(a, key("esc")) // the filter
	step(a, key("esc")) // the jump
	if a.view != style.ViewContainers {
		t.Fatalf("esc did not come back from the jump: %v", a.view)
	}
	step(a, key("?"))
	out := render(a)
	if !strings.Contains(out, "Jump to volumes") || strings.Contains(out, "Drill in") {
		t.Errorf("help's <enter> is not the jump:\n%s", out)
	}
	// The jump changes a line, not the layout: help still fits 120×40.
	step(a, tea.WindowSizeMsg{Width: 120, Height: 40})
	out = render(a)
	if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); len(lines) > 40 || len(a.helpPanel().Sections) > 4 {
		t.Errorf("help with a jump: %d lines, %d columns", len(lines), len(a.helpPanel().Sections))
	}
	for _, sec := range a.helpPanel().Sections {
		for _, e := range sec.Entries {
			found := false
			for _, l := range strings.Split(out, "\n") {
				if i := strings.Index(l, e.Key); i >= 0 && strings.Contains(l[i:], e.Desc) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("help with a jump cuts %s %q", e.Key, e.Desc)
			}
		}
	}
}

// TestJumpLeavesOtherViewsAlone: a jump on one view does not take enter in
// another — images keeps its layers.
func TestJumpLeavesOtherViewsAlone(t *testing.T) {
	a := jumpApp(t, map[string]config.Jump{"containers": {TargetView: "volumes", Filter: "$NAME"}})
	loadImages(a)
	step(a, key("enter"))
	if a.view != style.ViewLayers {
		t.Errorf("enter on an image opened %v, want its layers", a.view)
	}
}

// TestJumpFilterQuotesValues: in a regex filter a value matches itself —
// nginx:1.27's dot is not any character — and the text around it stays
// the user's regex; a fuzzy filter takes the value as it is.
func TestJumpFilterQuotesValues(t *testing.T) {
	a := jumpApp(t, map[string]config.Jump{"images": {TargetView: "containers", Filter: "^$IMAGE$"}})
	loadImages(a)
	step(a, key("enter"))
	if a.view != style.ViewContainers || a.filter != `^nginx:1\.27$` {
		t.Fatalf("enter opened %v filtered %q", a.view, a.filter)
	}
	loadContainers(a)
	out := render(a)
	if !strings.Contains(out, "web") || strings.Contains(out, "standalone") {
		t.Errorf("the filter did not keep just nginx:1.27's container:\n%s", out)
	}

	j := Jump{Template: "-f $NAME", ToName: "containers"}
	if got, _ := j.expand(map[string]string{"NAME": "a.b"}); got != "-f a.b" {
		t.Errorf("fuzzy filter = %q", got)
	}
}

// TestJumpValueIsNotExpandedAgain: a row's value holding $VAR text is
// filled in once, as text — FuzzExpandPluginVars' rule.
func TestJumpValueIsNotExpandedAgain(t *testing.T) {
	vars := map[string]string{"NAME": "$ID", "ID": "secret"}
	for _, tc := range []struct {
		want string
		j    Jump
	}{
		{j: Jump{Template: "k=$NAME", Label: true}, want: "-l k=$ID"},
		{j: Jump{Template: "$NAME"}, want: `\$ID`},
		{j: Jump{Template: "-f $NAME"}, want: "-f $ID"},
	} {
		if got, err := tc.j.expand(vars); err != nil || got != tc.want {
			t.Errorf("%q: %q %v, want %q", tc.j.Template, got, err, tc.want)
		}
	}
}

// TestJumpRefusesACommaInASelector: a value with a comma would add a term
// to the selector, so the jump says so and goes nowhere.
func TestJumpRefusesACommaInASelector(t *testing.T) {
	a := jumpApp(t, map[string]config.Jump{
		"containers": {TargetView: "volumes", LabelSelector: "com.docker.compose.project=$NAME"},
	})
	step(a, views.ContainersRefreshMsg{Containers: []docker.Container{
		{ID: "x1", Name: "a,b", State: "running", Image: "busybox"},
	}})
	step(a, key("enter"))
	if a.view != style.ViewContainers || !strings.Contains(a.errFlash, "holds a comma") {
		t.Errorf("view %v flash %q", a.view, a.errFlash)
	}
}

// TestJumpVarsArePluginVars: the variables a jump may name for each view
// are the ones pluginVars gives that view's row — the list cannot drift
// from what is filled in.
func TestJumpVarsArePluginVars(t *testing.T) {
	a := newTestApp(t)
	load := map[style.ViewType]func(){
		style.ViewContainers: func() {
			a.dispatchCommand("containers")
			loadContainers(a)
		},
		style.ViewImages: func() { loadImages(a) },
		style.ViewVolumes: func() {
			a.dispatchCommand("volumes")
			step(a, views.VolumesRefreshMsg{Volumes: shopVolumes()})
		},
		style.ViewNetworks: func() {
			a.dispatchCommand("networks")
			step(a, views.NetworksRefreshMsg{Networks: []docker.Network{{ID: "n1", Name: "shop_default"}}})
		},
		style.ViewProjects: func() {
			a.dispatchCommand("projects")
			step(a, views.ProjectsRefreshMsg{Containers: sampleContainers()})
		},
		style.ViewRuntimes: func() {
			a.dispatchCommand("runtimes")
			step(a, views.RuntimesRefreshMsg{Machines: []engines.Machine{{Provider: "colima", Name: "default"}}})
		},
	}
	if len(load) != len(jumpRowVars) {
		t.Fatalf("jumpRowVars has %d views, the test loads %d", len(jumpRowVars), len(load))
	}
	for vt, fn := range load {
		fn()
		if a.view != vt {
			t.Fatalf("loaded %v, want %v", a.view, vt)
		}
		vars, ok := a.pluginVars()
		if !ok {
			t.Fatalf("%v: no row", vt)
		}
		var got []string
		for k := range maps.Keys(vars) {
			if !strings.HasPrefix(k, "COL") && !slices.Contains(jumpCommonVars, k) {
				got = append(got, k)
			}
		}
		want := slices.Clone(jumpRowVars[vt])
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%v: pluginVars gives %v, jumpRowVars says %v", vt, got, want)
		}
	}
}
