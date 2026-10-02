// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"strings"
	"testing"
)

func TestLeavesDefaultForeground(t *testing.T) {
	for _, tc := range []struct {
		params string
		want   bool
	}{
		{params: "", want: true},
		{params: "0", want: true},
		{params: "39", want: true},
		{params: "0;32"},
		{params: "0;1;39", want: true},
		{params: "32"},
		{params: "1"},
		{params: "38;2;0;0;0"},
		{params: "48;2;0;0;0"},
		{params: "0;48;2;0;0;0", want: true},
		{params: "38;5;0"},
	} {
		if got := leavesDefaultForeground(tc.params); got != tc.want {
			t.Errorf("leavesDefaultForeground(%q) = %v, want %v", tc.params, got, tc.want)
		}
	}
}

// TestLogTextColor pins k9s's log look: uncolored text in lightskyblue, a
// line's own color kept for its extent, and the log color back after it.
func TestLogTextColor(t *testing.T) {
	plain := logTextColor("listening on :5000")
	if !strings.HasPrefix(plain, logTextFg) {
		t.Errorf("plain line not in the log color: %q", plain)
	}
	ok := logTextColor("[\x1b[0;32m  OK  \x1b[0m] Started kubelet")
	if !strings.Contains(ok, "\x1b[0;32m  OK  ") {
		t.Errorf("green OK was overridden: %q", ok)
	}
	if !strings.Contains(ok, "\x1b[0m"+logTextFg+"] Started") {
		t.Errorf("log color not restored after the line's reset: %q", ok)
	}
}

// TestBrokenEscapeIsDropped pins the line from a real kind node whose
// half-cut escape cost every row after it the canvas color.
func TestBrokenEscapeIsDropped(t *testing.T) {
	raw := "         Starting \x1b[0;1;39mkubelet.service\x1b[…elet: The Kubernetes Node Agent...\r"
	got := sanitizeLogText(raw)
	if want := "         Starting \x1b[0;1;39mkubelet.service…elet: The Kubernetes Node Agent..."; got != want {
		t.Errorf("sanitize =\n%q\nwant\n%q", got, want)
	}
	for _, s := range []string{"tail \x1b", "\x1b[12", "a\x1b[;b"} {
		if got := sanitizeLogText(s); strings.Contains(got, "\x1b") {
			t.Errorf("sanitize(%q) kept a broken escape: %q", s, got)
		}
	}
	if got := sanitizeLogText("[\x1b[0;32m  OK  \x1b[0m]"); got != "[\x1b[0;32m  OK  \x1b[0m]" {
		t.Errorf("complete colors were touched: %q", got)
	}
}
