// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
)

func TestFormatDiff(t *testing.T) {
	out := plainLines(FormatDiff([]docker.FileChange{
		{Path: "/etc/hosts", Kind: "C"},
		{Path: "/tmp/old", Kind: "D"},
		{Path: "/var/log/a", Kind: "A"},
		{Path: "/var/log/b", Kind: "A"},
	}))
	for _, want := range []string{"2 added, 1 changed, 1 deleted", "C /etc/hosts", "D /tmp/old", "A /var/log/a"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff is missing %q:\n%s", want, out)
		}
	}
	if out := plainLines(FormatDiff(nil)); !strings.Contains(out, "No changes") {
		t.Errorf("empty diff: %q", out)
	}
}

func TestFormatStats(t *testing.T) {
	out := plainLines(FormatStats(docker.Stats{
		CPUPerc: 150, CPUs: 4, MemUsage: 512 << 20, MemLimit: 2 << 30, PIDs: 17,
		NetRx: 1_500_000, NetTx: 2_000, BlockRead: 3_000_000, BlockWrite: 0,
	}))
	h := docker.HumanSize
	for _, want := range []string{
		"150.00%", "37.5% of 4 CPUs", "25.0% of " + h(2<<30), "PIDs", "17",
		h(1_500_000) + " in", h(2_000) + " out", h(3_000_000) + " read", "0B written",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stats is missing %q:\n%s", want, out)
		}
	}
}

func TestTopColumns(t *testing.T) {
	cols := topColumns(docker.Processes{
		Titles: []string{"UID", "PID", "CMD"},
		Rows:   [][]string{{"root", "12599", "/sbin/init"}, {"65535", "1", "/pause"}},
	})
	if len(cols) != 3 || cols[0].Width != 5 || cols[1].Width != 5 || cols[2].Width < 20 {
		t.Errorf("columns = %+v (widest value, command at least 20)", cols)
	}
}
