// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
)

// FuzzExpandPluginVars: a variable's value is substituted once and never
// read again — a container named "$HOME" or "${COL-NAME}" expands to that
// text, not to whatever it names — and an argument with no $ in it comes
// back unchanged.
func FuzzExpandPluginVars(f *testing.F) {
	for _, s := range []string{"web", "$HOME", "${COL-NAME}", "$$NAME", "a b;rm -rf /", "$(id)", ""} {
		f.Add(s, "--name=$NAME")
	}
	f.Fuzz(func(t *testing.T, value, arg string) {
		vars := map[string]string{"NAME": value}
		if got := expandPluginVars("$NAME", vars); got != value {
			t.Fatalf("$NAME with NAME=%q expanded to %q", value, got)
		}
		if got := expandPluginVars("${NAME}", vars); got != value {
			t.Fatalf("${NAME} with NAME=%q expanded to %q", value, got)
		}
		if !strings.Contains(arg, "$") {
			if got := expandPluginVars(arg, vars); got != arg {
				t.Fatalf("%q has no variable but expanded to %q", arg, got)
			}
		}
	})
}

// FuzzParseShortcut: any hotkeys.yaml spelling is either a key or an error —
// never a panic, and never an empty key accepted as bound.
func FuzzParseShortcut(f *testing.F) {
	for _, s := range []string{"Shift-0", "Shift-A", "Ctrl-U", "Alt-X", "F2", "a", "-", "Ctrl-", "Shift-", "ctrl-F12", "é", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		k, err := ParseShortcut(s)
		if err == nil && k == "" {
			t.Fatalf("%q parsed to an empty key", s)
		}
	})
}
