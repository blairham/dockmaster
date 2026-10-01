package tui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// TestDumpFrame writes a plain-text frame to the path in DOCKMASTER_DUMP.
// Used to keep the README's sample screen honest — it is generated from
// the real model rather than hand-drawn.
func TestDumpFrame(t *testing.T) {
	path := os.Getenv("DOCKMASTER_DUMP")
	if path == "" {
		t.Skip("set DOCKMASTER_DUMP to write a frame")
	}
	a := NewApp(nil, Options{Version: "v0.1.0"})
	a.splashActive = false
	a.loading = false
	a.statsOn = true
	step(a, tea.WindowSizeMsg{Width: 118, Height: 30})
	step(a, views.ContainersRefreshMsg{Containers: sampleContainers()})
	step(a, views.ContainerStatsMsg{Stats: map[string]docker.Stats{
		"aaaaaaaaaaaa1111": {CPUPerc: 0.42, MemUsage: 24_100_000, MemLimit: 2_000_000_000, OK: true},
		"bbbbbbbbbbbb2222": {CPUPerc: 12.80, MemUsage: 311_000_000, MemLimit: 2_000_000_000, OK: true},
	}})
	out := render(a)
	if err := os.WriteFile(path, []byte(strings.TrimRight(out, " \n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}
