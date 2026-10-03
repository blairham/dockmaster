// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// clipboard runs cmd and returns what it asked the terminal to copy, ""
// when it was not a clipboard request.
func clipboard(cmd tea.Cmd) string {
	if cmd == nil {
		return ""
	}
	// The message type is unexported; it is a string named setClipboardMsg.
	if m := cmd(); fmt.Sprintf("%T", m) == "tea.setClipboardMsg" {
		return fmt.Sprint(m)
	}
	return ""
}

// TestCopyNameAndID: c copies the selected row's name and i its full ID,
// in every table that has them (#5); where rows have no ID, i says so
// instead of copying something else.
func TestCopyNameAndID(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	if got := clipboard(step(a, key("c"))); got != "web" || a.flash != "copied web" {
		t.Errorf("c on a container copied %q (flash %q), want its name", got, a.flash)
	}
	if got := clipboard(step(a, key("i"))); got != "aaaaaaaaaaaa1111" || a.flash != "copied ID aaaaaaaaaaaa1111" {
		t.Errorf("i on a container copied %q (flash %q), want its full ID", got, a.flash)
	}

	b := newTestApp(t)
	loadImages(b)
	step(b, key("1"))
	loadImages(b)
	if got := clipboard(step(b, key("c"))); got == "" || got == "<none>" {
		t.Errorf("c on an image copied %q, want its reference", got)
	}

	p, _, _ := newComposeApp(t, Options{}, true)
	if got := clipboard(step(p, key("c"))); got != "shop" {
		t.Errorf("c on a project copied %q, want its name", got)
	}
	if got := clipboard(step(p, key("i"))); got != "" || p.errFlash == "" {
		t.Errorf("i on a project copied %q (err %q); a project has no ID, so it should say so", got, p.errFlash)
	}
}

// TestCopyInLogsIsStillTheLog: c in a log copies the log's text, not a
// row: the log view binds c itself, and a view's own key wins.
func TestCopyInLogsIsStillTheLog(t *testing.T) {
	a, _ := openLogs(t)
	step(a, key("c"))
	if a.flash == "copied web" {
		t.Error("c in a log copied the container's name instead of the log")
	}
}
