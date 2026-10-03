// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
)

// TestContainerUsage: a listed container knows the image it was created
// from by ID and the named volumes it mounts — not bind mounts or tmpfs,
// which are not volumes (#8).
func TestContainerUsage(t *testing.T) {
	c := newContainer(container.Summary{
		ID: "c1", Names: []string{"/web"}, Image: "nginx:1.27", ImageID: "sha256:aaaa",
		Mounts: []container.MountPoint{
			{Type: mount.TypeVolume, Name: "pgdata"},
			{Type: mount.TypeBind, Source: "/srv/www"},
			{Type: mount.TypeVolume, Name: "cache"},
			{Type: mount.TypeTmpfs},
		},
	})
	if got := strings.Join(c.Volumes, ","); got != "cache,pgdata" {
		t.Errorf("Volumes = %q, want the named volumes only, sorted", got)
	}
	if !c.UsesImage("sha256:aaaa") || c.UsesImage("nginx:1.27") || c.UsesImage("") {
		t.Error("UsesImage must match the full image ID and nothing else")
	}
	if !c.UsesVolume("pgdata") || c.UsesVolume("/srv/www") {
		t.Error("UsesVolume matched a bind mount, or missed a volume")
	}
}
