package tui

import (
	"strings"
	"testing"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
	"github.com/blairham/dockyard/internal/tui/views"
)

// TestTopDiffStatsKeys: T, D and S on a container open its processes, its
// filesystem changes, and its full stats.
func TestTopDiffStatsKeys(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	web := sampleContainers()[0].ID

	step(a, key("T"))
	if a.view != style.ViewTop {
		t.Fatalf("T opened %v, want top", a.view)
	}
	step(a, views.TopRefreshMsg{ID: web, Procs: docker.Processes{
		Titles: []string{"UID", "PID", "CMD"},
		Rows:   [][]string{{"root", "1", "nginx: master process"}, {"nginx", "29", "nginx: worker process"}},
	}})
	// After the real listing, so it would overwrite it if accepted.
	step(a, views.TopRefreshMsg{ID: "someone-else", Procs: docker.Processes{Titles: []string{"X"}, Rows: [][]string{{"wrong"}}}})
	out := render(a)
	for _, want := range []string{"web processes", "UID", "PID", "CMD", "nginx: master process", "nginx: worker process"} {
		if !strings.Contains(out, want) {
			t.Errorf("top is missing %q", want)
		}
	}
	if strings.Contains(out, "wrong") {
		t.Error("another container's process list landed in this view")
	}
	a.filterBar.OpenWith("")
	a.onFilterChange("worker")
	if tv := typedView[*views.TopView](a, style.ViewTop); tv.Count() != 1 {
		t.Errorf("filter worker: %d rows", tv.Count())
	}
	a.filterBar.Close()
	step(a, key("esc"))
	step(a, key("esc"))

	for k, title := range map[string]string{"D": "web diff", "S": "web stats", "H": "web health"} {
		step(a, key(k))
		iv := typedView[*views.InspectView](a, style.ViewInspect)
		if a.view != style.ViewInspect || iv.Title() != title {
			t.Errorf("%s opened %v titled %q, want %q", k, a.view, iv.Title(), title)
		}
		step(a, key("esc"))
	}

	if msg, _ := a.dispatchCommand("top"); msg != "" || a.view != style.ViewTop {
		t.Errorf(":top = %q, view %v", msg, a.view)
	}
	step(a, key("esc"))
	stats := typedView[*views.ContainersView](a, style.ViewContainers).StatsEnabled()
	a.dispatchCommand("stats")
	if typedView[*views.ContainersView](a, style.ViewContainers).StatsEnabled() == stats {
		t.Error(":stats no longer toggles the CPU/MEM poll")
	}
}
