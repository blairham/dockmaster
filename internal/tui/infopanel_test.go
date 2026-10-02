// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestInfoValuesKeepTheirBeginning pins how the header clips: a value too
// long for the info panel keeps its start — the scheme and path of an
// endpoint, the head of a context name — and loses the overflow, with no
// ellipsis. It used to keep the tail behind a "…".
func TestInfoValuesKeepTheirBeginning(t *testing.T) {
	a := newSizedApp(t, Options{Splashless: true, ReadOnly: true}, 240, 30)
	room := a.infoValueRoom()
	host := "unix:///Users/someone/with/a/long/home/directory/.colima/default/docker.sock"
	ctx := "a-docker-context-name-that-is-much-longer-than-the-info-panel"
	if len(host) <= room || len(ctx) <= room {
		t.Fatalf("fixture too short to clip: room %d", room)
	}
	lines := a.renderInfoPanelWith(ctx, host, "29.5.2 linux/aarch64")
	got := map[string]string{}
	for _, l := range lines {
		plain := ansi.ReplaceAllString(l, "")
		if w := lipgloss.Width(plain); w > a.chrome.InfoLabelWidth-1-infoGap {
			t.Errorf("info line is %d wide, panel holds %d: %q", w, a.chrome.InfoLabelWidth-1-infoGap, plain)
		}
		if (strings.HasPrefix(plain, "Endpoint") || strings.HasPrefix(plain, "Context")) && strings.Contains(plain, "…") {
			t.Errorf("info line carries an ellipsis: %q", plain)
		}
		if k, v, ok := strings.Cut(plain, ":"); ok {
			got[k] = strings.TrimSpace(v)
		}
	}
	if want := host[:room]; got["Endpoint"] != want {
		t.Errorf("Endpoint = %q, want the first %d cells %q", got["Endpoint"], room, want)
	}
	if !strings.HasPrefix(got["Context"], ctx[:room-len(" [RO]")]) || !strings.HasSuffix(got["Context"], "[RO]") {
		t.Errorf("Context = %q, want its head and the [RO] badge", got["Context"])
	}
}
