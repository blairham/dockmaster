// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker_test

import (
	"context"
	"testing"
	"time"

	"github.com/blairham/dockmaster/internal/docker"
)

// dial returns a live client, or skips the test when no daemon is
// reachable. These tests are the only place the Engine-API shapes are
// checked against a real daemon rather than against the SDK's type
// definitions, so they are worth keeping even though they cannot run in
// a container-less CI job.
func dial(t *testing.T) (*docker.Client, context.Context) {
	t.Helper()
	return dialFor(t, 30*time.Second)
}

// dialFor is dial with an explicit budget, for the calls whose honest
// answer takes minutes (see TestLiveImages).
func dialFor(t *testing.T, budget time.Duration) (*docker.Client, context.Context) {
	t.Helper()
	// These are the only tests that need a real daemon. `go test -short`
	// skips them so a CI job without docker — or a developer in a hurry —
	// still gets the whole pure-logic suite.
	if testing.Short() {
		t.Skip("live daemon test skipped in -short mode")
	}
	c, err := docker.New("")
	if err != nil {
		t.Skipf("no docker client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Negotiate(ctx); err != nil {
		t.Skipf("no docker daemon: %v", err)
	}
	return c, ctx
}

func TestLiveContainers(t *testing.T) {
	c, ctx := dial(t)
	all, err := c.Containers(ctx, true)
	if err != nil {
		t.Fatalf("Containers: %v", err)
	}
	t.Logf("daemon %s (%s) api=%s — %d containers", c.Version, c.OSArch, c.APIVersion, len(all))
	for i, ct := range all {
		if i >= 5 {
			break
		}
		t.Logf("  %-12s %-28s %-9s %-24s ports=%q proj=%q health=%q",
			ct.Short(), ct.Name, ct.State, ct.Status, ct.Ports, ct.Project, ct.Health)
		if ct.ID == "" || ct.Name == "" {
			t.Errorf("container %d has empty ID or Name: %+v", i, ct)
		}
	}
}

func TestLiveImages(t *testing.T) {
	// `docker images` is O(local image store); a host with a few hundred
	// images takes minutes through the CLI itself, so this gets its own
	// budget rather than the shared 30s one.
	c, ctx := dialFor(t, 5*time.Minute)
	imgs, err := c.Images(ctx, false)
	if err != nil {
		t.Fatalf("Images: %v", err)
	}
	t.Logf("%d image rows", len(imgs))
	for i, im := range imgs {
		if i >= 5 {
			break
		}
		t.Logf("  %-12s %-50s %-10s dangling=%t", im.Short(), im.Ref(), docker.HumanSize(im.Size), im.Dangling)
	}
}

func TestLiveVolumesNetworks(t *testing.T) {
	c, ctx := dial(t)
	vols, err := c.Volumes(ctx, false)
	if err != nil {
		t.Fatalf("Volumes: %v", err)
	}
	nets, err := c.Networks(ctx)
	if err != nil {
		t.Fatalf("Networks: %v", err)
	}
	t.Logf("%d volumes, %d networks", len(vols), len(nets))
	for i, n := range nets {
		if i >= 5 {
			break
		}
		t.Logf(
			"  net %-20s %-8s subnet=%-18s containers=%d builtin=%t",
			n.Name,
			n.Driver,
			n.Subnet,
			n.Containers,
			n.Builtin(),
		)
	}
}

func TestLiveStats(t *testing.T) {
	c, ctx := dial(t)
	all, err := c.Containers(ctx, false)
	if err != nil {
		t.Fatalf("Containers: %v", err)
	}
	ids := make([]string, 0, len(all))
	for _, ct := range all {
		if ct.Running() {
			ids = append(ids, ct.ID)
		}
	}
	if len(ids) == 0 {
		t.Skip("no running containers to sample")
	}
	start := time.Now()
	stats := c.SampleStats(ctx, ids)
	t.Logf("sampled %d/%d containers in %s", len(stats), len(ids), time.Since(start).Round(time.Millisecond))
	for id, s := range stats {
		// The one-shot-vs-two-sample bug shows up here: a CPU percentage
		// computed against a zeroed PreCPU baseline lands in the thousands.
		if s.CPUPerc < 0 || s.CPUPerc > 100*64 {
			t.Errorf("implausible CPU%% %.2f for %s — PreCPU baseline likely zeroed", s.CPUPerc, id[:12])
		}
		if s.MemUsage < 0 {
			t.Errorf("negative memory %d for %s", s.MemUsage, id[:12])
		}
	}
	for id, s := range stats {
		t.Logf("  %s cpu=%.2f%% mem=%s/%s (%.1f%%) pids=%d",
			id[:12], s.CPUPerc, docker.HumanSize(s.MemUsage), docker.HumanSize(s.MemLimit), s.MemPerc(), s.PIDs)
		break
	}
}

func TestLiveLogs(t *testing.T) {
	c, ctx := dial(t)
	all, err := c.Containers(ctx, false)
	if err != nil {
		t.Fatalf("Containers: %v", err)
	}
	for _, ct := range all {
		if !ct.Running() {
			continue
		}
		lines, err := c.Logs(ctx, ct.ID, 5)
		if err != nil {
			t.Fatalf("Logs(%s): %v", ct.Name, err)
		}
		t.Logf("%s: %d log lines", ct.Name, len(lines))
		for _, l := range lines {
			// A framed (non-TTY) stream read raw leaves the 8-byte header
			// on the front of every line — catch that here rather than in
			// the viewport.
			if len(l.Text) > 0 && l.Text[0] < 0x09 {
				t.Errorf("log line starts with a frame-header byte %#x — stdcopy demux missed: %q", l.Text[0], l.Text)
			}
			t.Logf("    %q", l.Text)
		}
		return
	}
	t.Skip("no running containers with logs")
}
