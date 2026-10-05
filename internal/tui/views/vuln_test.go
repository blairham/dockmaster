// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"slices"
	"testing"

	"github.com/blairham/dockmaster/internal/scan"
)

// TestVulnSummary: critical and high always, medium and low only while the
// cell stays within its budget, zero counts left out, "0" for a clean scan,
// and ~ on a stale one.
func TestVulnSummary(t *testing.T) {
	for _, c := range []struct {
		want  string
		tally scan.Tally
		stale bool
	}{
		{want: "C2 H5", tally: scan.Tally{Critical: 2, High: 5}},
		{want: "C1 M1 L3", tally: scan.Tally{Critical: 1, Medium: 1, Low: 3}},
		{want: "C2 H5 M1", tally: scan.Tally{Critical: 2, High: 5, Medium: 1, Low: 3}},
		{want: "C2 H5 M10", tally: scan.Tally{Critical: 2, High: 5, Medium: 10, Low: 3}},
		{want: "C2 H5 L3", tally: scan.Tally{Critical: 2, High: 5, Low: 3}},
		{want: "C120 H1500", tally: scan.Tally{Critical: 120, High: 1500, Medium: 1, Low: 1}},
		{want: "C1234 H56789", tally: scan.Tally{Critical: 1234, High: 56789, Medium: 1}},
		{want: "C12 H34", tally: scan.Tally{Critical: 12, High: 34, Medium: 100, Low: 1}},
		{want: "H5", tally: scan.Tally{High: 5}},
		{want: "M4 L12", tally: scan.Tally{Medium: 4, Low: 12}},
		{want: "0", tally: scan.Tally{Negligible: 7, Unknown: 2}},
		{want: "0~", stale: true},
		{want: "C1 H2~", tally: scan.Tally{Critical: 1, High: 2}, stale: true},
	} {
		if got := VulnSummary(scan.Result{Counts: c.tally}, c.stale); got != c.want {
			t.Errorf("VulnSummary(%+v, %v) = %q, want %q", c.tally, c.stale, got, c.want)
		}
	}
}

// TestVulnCompare: cells order by severity, never by text — unscanned,
// clean, low, medium, high, critical — with a stale mark and styling
// ignored.
func TestVulnCompare(t *testing.T) {
	want := []string{"", "0", "L9", "M1", "M2 L1", "H90", "C1", "C1 H2~", "\x1b[1mC2\x1b[0m", "C10"}
	got := slices.Clone(want)
	slices.Reverse(got)
	slices.SortStableFunc(got, vulnCompare)
	if !slices.Equal(got, want) {
		t.Errorf("sorted %q\nwant   %q", got, want)
	}
	if vulnCompare("C1 H2", "C1 H2~") != 0 {
		t.Error("staleness changed the order")
	}
}
