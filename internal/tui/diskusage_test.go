package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
	"github.com/blairham/dockyard/internal/tui/views"
)

func diskRows() []docker.DiskUsageRow {
	return []docker.DiskUsageRow{
		{Type: docker.DiskImages, Total: 4, Active: 2, Size: 1576666835, Reclaimable: 196034743},
		{Type: docker.DiskContainers, Total: 3, Active: 3, Size: 3960832},
		{Type: docker.DiskVolumes, Total: 4, Active: 3, Size: 24851240043},
		{Type: docker.DiskBuildCache, Total: 1, Size: 39923437, Reclaimable: 39923437},
	}
}

// TestDiskUsageView renders `docker system df`'s rows and columns, opened
// from the palette.
func TestDiskUsageView(t *testing.T) {
	a := newTestApp(t)
	a.dispatchCommand("df")
	if a.view != style.ViewDiskUsage {
		t.Fatalf(":df opened %v", a.view)
	}
	step(a, views.DiskUsageRefreshMsg{Rows: diskRows()})
	out := render(a)
	for _, want := range []string{
		"TYPE", "RECLAIMABLE", "Images", "Containers", "Local Volumes", "Build Cache",
		"1.58GB", "196MB (12%)", "39.9MB (100%)", "24.9GB",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("disk usage view is missing %q\n%s", want, out)
		}
	}
}

// TestDiskUsagePrunePerRow: P prunes the kind under the cursor, through the
// same confirmed prune the per-resource views use.
func TestDiskUsagePrunePerRow(t *testing.T) {
	want := []string{
		"prune all dangling images",
		"remove every stopped container",
		"prune every unused volume",
		"prune the build cache",
	}
	for i, words := range want {
		a := newTestApp(t)
		a.dispatchCommand("df")
		step(a, views.DiskUsageRefreshMsg{Rows: diskRows()})
		for range i {
			step(a, tea.KeyPressMsg{Code: tea.KeyDown})
		}
		step(a, key("P"))
		if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), words) {
			t.Errorf("row %d: confirm = %v %q, want %q", i, a.confirm.Active(), a.confirm.Prompt(), words)
		}
	}
}
