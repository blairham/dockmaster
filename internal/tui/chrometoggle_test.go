// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"
)

// TestCtrlGAndCtrlEToggleTheChrome pins k9s's chrome keys: ctrl+g hides and
// restores the breadcrumbs, ctrl+e the header, and either way the frame
// stays exactly the terminal's height with the freed rows given to the box.
func TestCtrlGAndCtrlEToggleTheChrome(t *testing.T) {
	a := newSizedApp(t, Options{Splashless: true}, 200, 30)
	loadContainers(a)
	ctrl := func(r rune) tea.KeyMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }
	frame := func() []string { return strings.Split(strings.TrimRight(render(a), "\n"), "\n") }
	hasCrumbs := func(lines []string) bool {
		return strings.Contains(strings.Join(lines[len(lines)-2:], ""), chrome.CrumbText("Containers"))
	}
	hasHeader := func(lines []string) bool { return strings.Contains(lines[0], "Context:") }

	check := func(what string, wantCrumbs, wantHeader bool) {
		t.Helper()
		lines := frame()
		if len(lines) != a.height {
			t.Errorf("%s: frame is %d lines, terminal is %d", what, len(lines), a.height)
		}
		if got := hasCrumbs(lines); got != wantCrumbs {
			t.Errorf("%s: crumbs shown %v, want %v", what, got, wantCrumbs)
		}
		if got := hasHeader(lines); got != wantHeader {
			t.Errorf("%s: header shown %v, want %v", what, got, wantHeader)
		}
	}
	check("start", true, true)
	step(a, ctrl('g'))
	check("after ctrl+g", false, true)
	step(a, ctrl('e'))
	check("after ctrl+e", false, false)
	step(a, ctrl('g'))
	step(a, ctrl('e'))
	check("both restored", true, true)
}
