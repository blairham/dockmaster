// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import "testing"

// TestParseReplicas: digits only, and small enough for an int — the form
// caps typing at six, so the overflow is only reachable from here.
func TestParseReplicas(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{in: "0", want: 0, ok: true},
		{in: "3", want: 3, ok: true},
		{in: "120", want: 120, ok: true},
		{in: "", ok: false},
		{in: "-1", ok: false},
		{in: "+1", ok: false},
		{in: "1.5", ok: false},
		{in: "1 0", ok: false},
		{in: "x", ok: false},
		{in: "99999999999999999999", ok: false},
	} {
		got, ok := parseReplicas(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseReplicas(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
