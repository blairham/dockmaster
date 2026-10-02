// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// TestTruncateNeverTearsARune pins the PORTS column: every published port
// carries a three-byte "→", and a byte-count cut landing inside one drew a
// replacement glyph at the end of the cell.
func TestTruncateNeverTearsARune(t *testing.T) {
	const ports = "5433→5432/tcp 5433→5432/tcp"
	for n := 1; n <= len(ports)+1; n++ {
		got := truncate(ports, n)
		if !utf8.ValidString(got) {
			t.Errorf("truncate(%q, %d) = %q: not valid UTF-8", ports, n, got)
		}
		if w := lipgloss.Width(got); w > n {
			t.Errorf("truncate(%q, %d) = %q: %d cells wide", ports, n, got, w)
		}
	}
	if got := truncate(ports, 22); got != "5433→5432/tcp 5433→54…" {
		t.Errorf("truncate at 22 = %q, want the cut to fall on cell 22, not byte 22", got)
	}
	if got := truncate("8080→80/tcp", 22); got != "8080→80/tcp" {
		t.Errorf("a string that fits was changed: %q", got)
	}
}
