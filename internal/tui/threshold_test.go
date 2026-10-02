// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

const gib = 1 << 30

// sgrBefore is the last SGR sequence that opens before text on its line.
var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// rgb is how a color appears inside an SGR foreground: "38;2;r;g;b".
func rgb(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

// cellColor reports the threshold color, if any, the frame paints text in.
func cellColor(frame, text string) string {
	for _, l := range strings.Split(frame, "\n") {
		i := strings.Index(l, text)
		if i < 0 {
			continue
		}
		open := sgr.FindAllString(l[:i], -1)
		if len(open) == 0 || !strings.HasSuffix(l[:i], open[len(open)-1]) {
			return "plain"
		}
		switch last := open[len(open)-1]; {
		case strings.Contains(last, rgb(style.ColorRed)):
			return "red"
		case strings.Contains(last, rgb(style.ColorOrange)):
			return "orange"
		}
		return "plain"
	}
	return "missing"
}

// statsFor gives three sample containers CPU and memory readings on a
// 14-CPU host: web cool, api past warn, standalone past critical.
func statsFor() map[string]docker.Stats {
	cs := sampleContainers()
	return map[string]docker.Stats{
		cs[0].ID: {OK: true, CPUs: 14, CPUPerc: 140, MemUsage: 1 * gib, MemLimit: 10 * gib},  // 10%, 10%
		cs[1].ID: {OK: true, CPUs: 14, CPUPerc: 1008, MemUsage: 7 * gib, MemLimit: 10 * gib}, // 72%, 70%
		cs[3].ID: {OK: true, CPUs: 14, CPUPerc: 1316, MemUsage: 9 * gib, MemLimit: 10 * gib}, // 94%, 90%
	}
}

// loadWithStats lists the sample containers with statsFor's readings and
// parks the cursor on migrate, which has none: the selected row is drawn
// in the selection style, without threshold colors, so a measured row
// must not be the selected one.
func loadWithStats(t *testing.T, a *App) {
	t.Helper()
	loadContainers(a)
	step(a, views.ContainerStatsMsg{Stats: statsFor()})
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	for range 8 {
		if c, _ := cv.Selected(); c.Name == "migrate" {
			return
		}
		step(a, key("j"))
	}
	t.Fatal("could not park the cursor on migrate")
}

// TestCPUMemThresholdColors: CPU% and MEM turn orange at warn and red at
// critical — k9s's 70/90 by default — and a busy single core on a large
// host is not flagged.
func TestCPUMemThresholdColors(t *testing.T) {
	a := newTestApp(t)
	loadWithStats(t, a)
	frame := renderStyled(a)
	for _, tc := range []struct{ text, want string }{
		{text: "140.00", want: "plain"},
		{text: "1008.00", want: "orange"},
		{text: "1316.00", want: "red"},
		{text: docker.HumanSize(1 * gib), want: "plain"},
		{text: docker.HumanSize(7 * gib), want: "orange"},
		{text: docker.HumanSize(9 * gib), want: "red"},
	} {
		if got := cellColor(frame, tc.text); got != tc.want {
			t.Errorf("%s is %s, want %s", tc.text, got, tc.want)
		}
	}
}

// TestThresholdsFromConfigSurviveAContextSwitch: config.yaml's thresholds
// reach the view, and the views rebuilt by a context switch keep them.
func TestThresholdsFromConfigSurviveAContextSwitch(t *testing.T) {
	strict := views.Thresholds{CPUWarn: 5, CPUCritical: 8, MemWarn: 5, MemCritical: 8}
	a := NewApp(nil, Options{Version: "test", Thresholds: strict})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 44})
	check := func(when string) {
		t.Helper()
		loadWithStats(t, a)
		if got := cellColor(renderStyled(a), "140.00"); got != "red" {
			t.Errorf("%s: a 10%% share under critical 8 is %s, want red", when, got)
		}
	}
	check("at start")
	c, err := docker.New("unix:///nonexistent/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	a.applySwitchContext(switchContextMsg{name: "other", client: c})
	check("after a context switch")
}
