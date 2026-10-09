// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/versions"
)

// Disk-usage row kinds, in the order `docker system df` prints them.
const (
	DiskImages     = "Images"
	DiskContainers = "Containers"
	DiskVolumes    = "Local Volumes"
	DiskBuildCache = "Build Cache"
)

// DiskUsageRow is one line of `docker system df`.
type DiskUsageRow struct {
	Type        string
	Total       int
	Active      int
	Size        int64
	Reclaimable int64
}

// DiskUsage is `docker system df`: what each kind of object costs on disk
// and how much of that a prune would give back. The daemon walks every
// volume to size it, so on a large host this is slow.
//
// No object type is named, so the daemon reports all four, as it did for
// the bare /system/df request; Verbose asks for the per-object lists that
// SummarizeDiskUsage counts, which API 1.52+ only returns on request.
func (c *Client) DiskUsage(ctx context.Context) ([]DiskUsageRow, error) {
	du, err := c.api.DiskUsage(ctx, client.DiskUsageOptions{Verbose: true})
	if err != nil {
		return nil, fmt.Errorf("reading disk usage: %w", err)
	}
	rows := SummarizeDiskUsage(du)
	// The client negotiated its API version on the first request, so by
	// now ClientVersion is the version this response was spoken in.
	if !versions.LessThan(c.api.ClientVersion(), daemonDFVersion) {
		applyDaemonReclaimable(rows, daemonUsage{
			ImageReclaimable:      &du.Images.Reclaimable,
			BuildCacheReclaimable: &du.BuildCache.Reclaimable,
		})
	}
	return rows, nil
}

// daemonUsage is the reclaimable space the daemon reports itself, per
// kind, from API 1.52 on. Nil means it reported none.
type daemonUsage struct {
	ImageReclaimable      *int64
	BuildCacheReclaimable *int64
}

// daemonDFVersion is the first API version whose /system/df carries the
// per-kind usage summaries.
//
// From Docker 29 the daemon computes reclaimable space itself, and with
// the containerd image store its accounting of shared layers differs from
// the client-side formula older CLIs used — on a real host 227MB computed
// against the 196MB `docker system df` prints. Showing a different number
// from the CLI's would be wrong in the way that matters, so for images and
// build cache the daemon's figure wins where it gives one. Below 1.52 the
// client library fills Reclaimable from a formula of its own that is not
// the CLI's, so it is ignored there and SummarizeDiskUsage's stands.
const daemonDFVersion = "1.52"

// applyDaemonReclaimable replaces computed reclaimable figures with the
// daemon's own wherever it reported one.
func applyDaemonReclaimable(rows []DiskUsageRow, du daemonUsage) {
	for i := range rows {
		switch {
		case rows[i].Type == DiskImages && du.ImageReclaimable != nil:
			rows[i].Reclaimable = *du.ImageReclaimable
		case rows[i].Type == DiskBuildCache && du.BuildCacheReclaimable != nil:
			rows[i].Reclaimable = *du.BuildCacheReclaimable
		}
	}
}

// SummarizeDiskUsage folds the daemon's per-object disk-usage lists into
// the four rows the docker CLI prints, computing "active" and
// "reclaimable" the way the CLI does:
//
//   - images: size is the shared layer total (the daemon's TotalSize,
//     LayersSize before API 1.52); an image is active when a
//     container uses it, and what those images use exclusively is not
//     reclaimable;
//   - containers: size is each writable layer; a running container's is not
//     reclaimable;
//   - volumes: an unreferenced volume is reclaimable; unknown sizes (-1)
//     count as zero;
//   - build cache: shared records are not counted; in-use ones are not
//     reclaimable.
func SummarizeDiskUsage(du client.DiskUsageResult) []DiskUsageRow {
	return []DiskUsageRow{
		imageUsage(du), containerUsage(du), volumeUsage(du), buildCacheUsage(du),
	}
}

func imageUsage(du client.DiskUsageResult) DiskUsageRow {
	img := DiskUsageRow{Type: DiskImages, Total: len(du.Images.Items), Size: du.Images.TotalSize}
	var used int64
	for _, i := range du.Images.Items {
		if i.Containers == 0 {
			continue
		}
		img.Active++
		if i.Size != -1 && i.SharedSize != -1 {
			used += i.Size - i.SharedSize
		}
	}
	img.Reclaimable = max(img.Size-used, 0)
	return img
}

func containerUsage(du client.DiskUsageResult) DiskUsageRow {
	ctr := DiskUsageRow{Type: DiskContainers, Total: len(du.Containers.Items)}
	for _, c := range du.Containers.Items {
		ctr.Size += c.SizeRw
		if c.State == container.StateRunning {
			ctr.Active++
		} else {
			ctr.Reclaimable += c.SizeRw
		}
	}
	return ctr
}

func volumeUsage(du client.DiskUsageResult) DiskUsageRow {
	vol := DiskUsageRow{Type: DiskVolumes, Total: len(du.Volumes.Items)}
	for _, v := range du.Volumes.Items {
		if v.UsageData == nil {
			continue
		}
		size := max(v.UsageData.Size, 0)
		vol.Size += size
		if v.UsageData.RefCount > 0 {
			vol.Active++
		} else {
			vol.Reclaimable += size
		}
	}
	return vol
}

func buildCacheUsage(du client.DiskUsageResult) DiskUsageRow {
	bc := DiskUsageRow{Type: DiskBuildCache, Total: len(du.BuildCache.Items)}
	for _, r := range du.BuildCache.Items {
		if r.InUse {
			bc.Active++
		}
		if r.Shared {
			continue
		}
		bc.Size += r.Size
		if !r.InUse {
			bc.Reclaimable += r.Size
		}
	}
	return bc
}
