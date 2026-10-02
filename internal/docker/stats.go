// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// statsConcurrency bounds the fan-out of the per-container stats poll. Each
// call blocks server-side for one collection interval (~1s), so they have to
// run in parallel — but a host with 200 containers should not open 200
// simultaneous connections to the daemon socket either.
const statsConcurrency = 12

// Stats is the live resource sample for one container.
type Stats struct {
	// CPUPerc is the share of total host CPU, matching `docker stats` —
	// it can exceed 100% on a multi-core host.
	CPUPerc float64
	// MemUsage/MemLimit are bytes.
	MemUsage int64
	MemLimit int64
	// NetRx/NetTx and BlockRead/BlockWrite are cumulative bytes since the
	// container started.
	NetRx      int64
	NetTx      int64
	BlockRead  int64
	BlockWrite int64
	// PIDs is the process count inside the container.
	PIDs int64
	// CPUs is how many CPUs the container can use — the host's online
	// CPUs, narrowed by a cpuset limit. CPUPerc reaches CPUs*100.
	CPUs int
	// OK is false for a container the poll could not sample (exited
	// mid-poll, or the daemon refused). Distinguishes "0%" from "unknown".
	OK bool
}

// CPUShare is CPU use as a share of the CPUs the container can use,
// 0–100: CPUPerc spread over CPUs. One core saturated on a 14-CPU host is
// CPUPerc 100 but CPUShare ~7 — busy, not hot. Thresholds judge this.
func (s Stats) CPUShare() float64 {
	if s.CPUs <= 0 {
		return s.CPUPerc
	}
	return s.CPUPerc / float64(s.CPUs)
}

// MemPerc is memory usage against the container's limit, as `docker
// stats` MEM % reports it. An unlimited container's limit is the host's
// memory, so its percentage is its share of the machine — what the
// threshold colours judge it by.
func (s Stats) MemPerc() float64 {
	if s.MemLimit <= 0 {
		return 0
	}
	return float64(s.MemUsage) / float64(s.MemLimit) * 100
}

// Stats returns the cached sample for a container. The bool reports whether
// a sample has ever landed — callers render "…" until it has, rather than a
// misleading 0.00%.
func (c *Client) Stats(id string) (Stats, bool) {
	c.statsMu.RLock()
	defer c.statsMu.RUnlock()
	s, ok := c.stats[id]
	return s, ok
}

// SampleStats refreshes the stats cache for the given container IDs and
// returns the fresh map. IDs absent from the list are evicted, so a removed
// container does not leave its last sample behind to be rendered forever.
//
// Uses the two-sample form of the stats endpoint (stream=false, one-shot
// off) rather than ContainerStatsOneShot: one-shot returns a zeroed PreCPU
// block, and the CPU formula against a zero baseline reports a nonsense
// number on the order of thousands of percent.
func (c *Client) SampleStats(ctx context.Context, ids []string) map[string]Stats {
	out := make(map[string]Stats, len(ids))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	sem := make(chan struct{}, statsConcurrency)

	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			s, err := c.sampleOne(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// Keep the previous sample if we have one — a single
				// failed poll on a busy daemon should not blank the row.
				c.statsMu.RLock()
				prev, ok := c.stats[id]
				c.statsMu.RUnlock()
				if ok {
					out[id] = prev
				}
				return
			}
			out[id] = s
		}(id)
	}
	wg.Wait()

	c.statsMu.Lock()
	c.stats = out
	c.statsMu.Unlock()
	return out
}

func (c *Client) sampleOne(ctx context.Context, id string) (Stats, error) {
	// Stream off with the previous sample included is the old
	// ContainerStats(ctx, id, false): stream=false and no one-shot, so the
	// daemon waits one interval and fills PreCPUStats. Leaving
	// IncludePreviousSample off would send one-shot=true — the zeroed
	// PreCPU this exists to avoid.
	resp, err := c.api.ContainerStats(ctx, id, client.ContainerStatsOptions{
		Stream:                false,
		IncludePreviousSample: true,
	})
	if err != nil {
		return Stats{}, fmt.Errorf("sampling %s: %w", shortID(id), err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; close error is not actionable

	var v container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return Stats{}, fmt.Errorf("decoding stats for %s: %w", shortID(id), err)
	}

	s := Stats{
		CPUPerc:  cpuPercent(v),
		CPUs:     onlineCPUs(v),
		MemUsage: int64(v.MemoryStats.Usage), //nolint:gosec // daemon-reported byte counts
		MemLimit: int64(v.MemoryStats.Limit), //nolint:gosec // daemon-reported byte counts
		PIDs:     int64(v.PidsStats.Current), //nolint:gosec // daemon-reported process count
		OK:       true,
	}
	// cgroup v2 reports a `cache` entry that is reclaimable page cache, not
	// the container's working set. docker's own CLI subtracts it, and
	// without that a container that merely read a large file looks pinned
	// at its memory limit.
	if cache, ok := v.MemoryStats.Stats["inactive_file"]; ok &&
		int64(cache) < s.MemUsage { //nolint:gosec // daemon-reported
		s.MemUsage -= int64(cache) //nolint:gosec // bounds-checked above
	}

	for _, n := range v.Networks {
		s.NetRx += int64(n.RxBytes) //nolint:gosec // daemon-reported byte counts
		s.NetTx += int64(n.TxBytes) //nolint:gosec // daemon-reported byte counts
	}
	for _, b := range v.BlkioStats.IoServiceBytesRecursive {
		switch b.Op {
		case "read", "Read":
			s.BlockRead += int64(b.Value) //nolint:gosec // daemon-reported byte counts
		case "write", "Write":
			s.BlockWrite += int64(b.Value) //nolint:gosec // daemon-reported byte counts
		}
	}
	return s, nil
}

// cpuPercent is docker's own CPU formula: the container's CPU-time delta
// over the system CPU-time delta, scaled by the number of CPUs the
// container can use.
func cpuPercent(v container.StatsResponse) float64 {
	cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage) - float64(v.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(v.CPUStats.SystemUsage) - float64(v.PreCPUStats.SystemUsage)
	if cpuDelta <= 0 || sysDelta <= 0 {
		return 0
	}
	return cpuDelta / sysDelta * float64(onlineCPUs(v)) * 100
}

// onlineCPUs is how many CPUs the container can use, as docker computes it.
func onlineCPUs(v container.StatsResponse) int {
	cpus := int(v.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = len(v.CPUStats.CPUUsage.PercpuUsage)
	}
	return max(cpus, 1)
}
