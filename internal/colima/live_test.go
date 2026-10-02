// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package colima

import (
	"context"
	"testing"
	"time"
)

// TestLiveList lists the real colima's profiles. Read-only — it never
// starts, stops or deletes anything — and skipped under -short or when
// colima is not installed.
func TestLiveList(t *testing.T) {
	if testing.Short() {
		t.Skip("live colima test")
	}
	c, err := New()
	if err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	profiles, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if p.Name == "" || p.Status == "" {
			t.Errorf("profile parsed without name or status: %+v", p)
		}
		t.Logf("%s %s cpus=%d mem=%d host=%s", p.Name, p.Status, p.CPUs, p.Memory, c.DockerHost(p.Name))
		if got, ok := c.ProfileForHost(c.DockerHost(p.Name)); !ok || got != p.Name {
			t.Errorf("ProfileForHost round trip for %s: %q, %v", p.Name, got, ok)
		}
	}
}
