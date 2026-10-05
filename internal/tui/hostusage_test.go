// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// usageLine is the header's CPU/MEM line, unstyled.
func usageLine(t *testing.T, a *App) string {
	t.Helper()
	for _, l := range a.renderInfoPanel() {
		plain := ansi.ReplaceAllString(l, "")
		if strings.HasPrefix(plain, "CPU/MEM:") {
			return strings.TrimSpace(strings.TrimPrefix(plain, "CPU/MEM:"))
		}
	}
	t.Fatalf("no CPU/MEM line in the header: %q", a.renderInfoPanel())
	return ""
}

// usageApp is the containers view against a fake daemon whose host has 4
// CPUs and 8 GiB, as the startup Info call would have said.
func usageApp(t *testing.T, opts Options, width int) (*App, func() []string) {
	t.Helper()
	a := newSizedApp(t, opts, width, 30)
	a.splashActive = false
	c, calls := fakeDaemon(t)
	c.NCPU, c.MemTotal = 4, 8*gib
	a.client = c
	return a, calls
}

// sampleUsage is a sample for web and api, the two running containers:
// 50% + 150% of a core is 200 of the host's 400; 3 GiB + 1 GiB is half of
// 8.
func sampleUsage() map[string]docker.Stats {
	cs := sampleContainers()
	return map[string]docker.Stats{
		cs[0].ID: {OK: true, CPUs: 4, CPUPerc: 50, MemUsage: 3 * gib, MemLimit: 8 * gib},
		cs[1].ID: {OK: true, CPUs: 4, CPUPerc: 150, MemUsage: 1 * gib, MemLimit: 8 * gib},
	}
}

// TestHeaderHostUsage: the header shows the daemon host's CPU and memory
// in use — the poll's sums over the host's CPUs and memory — "…" until a
// sample has landed, and nothing to wait for with nothing running.
func TestHeaderHostUsage(t *testing.T) {
	a, calls := usageApp(t, Options{}, 160)
	if got := usageLine(t, a); got != "…" {
		t.Errorf("before the containers list: %q, want …", got)
	}
	loadContainers(a)
	if got := usageLine(t, a); got != "…" {
		t.Errorf("before the first sample: %q, want …", got)
	}
	step(a, views.ContainerStatsMsg{Stats: sampleUsage()})
	if got := usageLine(t, a); got != "50% / 50%" {
		t.Errorf("CPU/MEM = %q, want 50%% / 50%%", got)
	}
	if !strings.Contains(render(a), "CPU/MEM:  50% / 50%") {
		t.Errorf("the frame's header does not show it:\n%s", render(a))
	}
	if n := len(calls()); n != 0 {
		t.Errorf("the header made %d daemon calls: %q", n, calls())
	}

	step(a, views.ContainersRefreshMsg{Containers: sampleContainers()[2:3]}) // only the exited one
	if got := usageLine(t, a); got != "0% / 0%" {
		t.Errorf("nothing running: %q, want 0%% / 0%%", got)
	}
}

// TestHeaderHostUsageOff: with the poll off — t, or --no-stats — or the
// host's size unknown, the line says n/a rather than a stale or made-up
// figure.
func TestHeaderHostUsageOff(t *testing.T) {
	a, _ := usageApp(t, Options{}, 160)
	loadContainers(a)
	step(a, views.ContainerStatsMsg{Stats: sampleUsage()})
	step(a, key("t"))
	if got := usageLine(t, a); got != "n/a" {
		t.Errorf("after t: %q, want n/a", got)
	}
	step(a, key("t"))
	if got := usageLine(t, a); got == "n/a" {
		t.Errorf("t back on still says n/a")
	}

	b, _ := usageApp(t, Options{NoStats: true}, 160)
	loadContainers(b)
	if got := usageLine(t, b); got != "n/a" {
		t.Errorf("--no-stats: %q, want n/a", got)
	}

	c, _ := usageApp(t, Options{}, 160)
	c.client.NCPU = 0
	loadContainers(c)
	step(c, views.ContainerStatsMsg{Stats: sampleUsage()})
	if got := usageLine(t, c); got != "n/a" {
		t.Errorf("unknown host size: %q, want n/a", got)
	}
	d := newTestApp(t) // no daemon at all
	if got := usageLine(t, d); got != "n/a" {
		t.Errorf("no client: %q, want n/a", got)
	}
}

// TestHeaderHostUsageThresholds: past the thresholds a figure is drawn in
// the warn or critical color, as the CPU% and MEM columns are.
func TestHeaderHostUsageThresholds(t *testing.T) {
	a, _ := usageApp(t, Options{}, 160)
	loadContainers(a)
	cs := sampleContainers()
	step(a, views.ContainerStatsMsg{Stats: map[string]docker.Stats{
		cs[0].ID: {OK: true, CPUs: 4, CPUPerc: 380, MemUsage: 6 * gib}, // 95%, 75%
	}})
	line := ""
	for _, l := range a.renderInfoPanel() {
		if strings.Contains(ansi.ReplaceAllString(l, ""), "CPU/MEM") {
			line = l
		}
	}
	plain := ansi.ReplaceAllString(line, "")
	if !strings.Contains(plain, "95% / 75%") {
		t.Fatalf("CPU/MEM = %q", plain)
	}
	if !strings.Contains(line, views.ThresholdText("95%", 95, 70, 90)) ||
		!strings.Contains(line, views.ThresholdText("75%", 75, 70, 90)) {
		t.Errorf("the figures are not in their threshold colors: %q", line)
	}
}

// TestHeaderHostUsageAt80Columns: the line fits the info panel at 80
// columns, and the header keeps its height — the line takes the row the
// shortcut grid already had.
func TestHeaderHostUsageAt80Columns(t *testing.T) {
	a, _ := usageApp(t, Options{}, 80)
	loadContainers(a)
	step(a, views.ContainerStatsMsg{Stats: sampleUsage()})
	if rows := a.chrome.TopSectionRows(); rows != 6 {
		t.Errorf("the header is %d rows, want 6", rows)
	}
	for _, l := range strings.Split(render(a), "\n") {
		if w := lipgloss.Width(l); w > 80 {
			t.Errorf("a line is %d wide at 80 columns: %q", w, l)
		}
	}
	if !strings.Contains(render(a), "CPU/MEM:  50% / 50%") {
		t.Errorf("no CPU/MEM at 80 columns:\n%s", render(a))
	}
}
