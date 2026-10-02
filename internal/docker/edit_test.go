// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"strings"
	"testing"
)

func TestEditUpdate(t *testing.T) {
	cur := EditState{Name: "web", Restart: "no", Memory: 256 << 20, MemorySwap: 512 << 20}

	u, changed, rename, err := EditUpdate(cur, EditSpec{Name: "web", Memory: "1g", CPUs: "1.5", Restart: "unless-stopped"})
	if err != nil || !changed || rename != "" {
		t.Fatalf("changed %v rename %q err %v", changed, rename, err)
	}
	if u.Memory != 1<<30 || u.MemorySwap != 1<<30+256<<20 {
		t.Errorf("memory %d swap %d: the 256m of swap headroom is not kept", u.Memory, u.MemorySwap)
	}
	if u.NanoCPUs != 1_500_000_000 || u.RestartPolicy.Name != "unless-stopped" {
		t.Errorf("update %+v", u)
	}

	for _, tc := range []struct {
		swap, want int64
	}{
		{swap: -1, want: -1},             // unlimited swap stays unlimited
		{swap: 0, want: 2 * (512 << 20)}, // no limit before: docker run's 2x
	} {
		c := EditState{Name: "x", Restart: "no", MemorySwap: tc.swap}
		if tc.swap == -1 {
			c.Memory = 256 << 20
		}
		u, _, _, _ := EditUpdate(c, EditSpec{Memory: "512m"})
		if u.MemorySwap != tc.want {
			t.Errorf("swap %d: new swap %d, want %d", tc.swap, u.MemorySwap, tc.want)
		}
	}

	// Unchanged and empty fields send nothing; a new name renames.
	_, changed, rename, _ = EditUpdate(cur, EditSpec{Name: "api", Memory: "256m", Restart: "no"})
	if changed || rename != "api" {
		t.Errorf("unchanged limits: changed %v rename %q", changed, rename)
	}

	for spec, want := range map[EditSpec]string{
		{CPUs: "lots"}:  "cpus",
		{CPUs: "-1"}:    "cpus",
		{Memory: "big"}: "memory",
		{Memory: "1m"}:  "minimum",
	} {
		if _, _, _, err := EditUpdate(cur, spec); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: %v, want an error about %s", spec, err, want)
		}
	}
	if MemoryText(512<<20) != "512m" || MemoryText(2<<30) != "2g" || CPUsText(1_500_000_000) != "1.5" ||
		CPUsText(0) != "" {
		t.Error("form text")
	}
}
