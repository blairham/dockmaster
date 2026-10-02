// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"regexp"
	"strings"
	"testing"
)

// sgrAt matches one complete color sequence at the start of a string — the
// only escape sanitizeLogText may let through.
var sgrAt = regexp.MustCompile(`^\x1b\[[0-9;:]*m`)

// FuzzSanitizeLogText: whatever a container writes, the line that reaches
// the frame holds no control character, and every ESC in it opens a complete
// color sequence — no cursor movement, erase, title, link or half-sequence
// that would act on the terminal rather than color text (SECURITY.md).
func FuzzSanitizeLogText(f *testing.F) {
	for _, s := range []string{
		"plain line",
		"\x1b[31mred\x1b[0m",
		"progress 10%\rprogress 100%\r\n",
		"erase\x1b[K right border",
		"\x1b]0;title\x07after",
		"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\",
		"kubelet.service\x1b[",
		"tab\there",
		"\x1b[?25l\x1b[2J\x1b[H",
		"\x1bP+q\x1b\\",
		"\u009b2J\u009d0;title\u009c",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := sanitizeLogText(in)
		for i := 0; i < len(out); i++ {
			c := out[i]
			if c == 0x1b {
				m := sgrAt.FindString(out[i:])
				if m == "" {
					t.Fatalf("an escape that is not a complete color sequence survived: %q -> %q", in, out)
				}
				i += len(m) - 1
				continue
			}
			if c < ' ' || c == 0x7f {
				t.Fatalf("control byte %#x survived: %q -> %q", c, in, out)
			}
		}
		if strings.ContainsRune(out, '\u009b') {
			t.Fatalf("a C1 CSI survived: %q -> %q", in, out)
		}
	})
}
