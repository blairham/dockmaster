// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"reflect"
	"testing"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
)

// TestSummarizeDiskUsage pins the docker CLI's arithmetic for every row,
// including the cases that are easy to get wrong: an image's shared layers
// are not "used", a paused container is reclaimable (only running is not),
// unknown volume sizes count as zero, and shared build cache is not
// counted at all.
func TestSummarizeDiskUsage(t *testing.T) {
	// The client's own Reclaimable/Active figures are set to nonsense:
	// the rows are computed from the item lists alone.
	du := client.DiskUsageResult{
		Images: client.ImagesDiskUsage{
			TotalSize:   1000,
			Reclaimable: -7,
			Items: []image.Summary{
				{Containers: 2, Size: 400, SharedSize: 100}, // uses 300 exclusively
				{Containers: 0, Size: 300, SharedSize: 100},
				{Containers: 1, Size: -1, SharedSize: -1}, // unknown: active, not subtracted
			},
		},
		Containers: client.ContainersDiskUsage{
			ActiveCount: 99,
			Items: []container.Summary{
				{State: container.StateRunning, SizeRw: 50},
				{State: container.StateExited, SizeRw: 20},
				{State: container.StatePaused, SizeRw: 5},
			},
		},
		Volumes: client.VolumesDiskUsage{
			Items: []volume.Volume{
				{UsageData: &volume.UsageData{RefCount: 1, Size: 70}},
				{UsageData: &volume.UsageData{RefCount: 0, Size: 30}},
				{UsageData: &volume.UsageData{RefCount: 0, Size: -1}},
				{},
			},
		},
		BuildCache: client.BuildCacheDiskUsage{
			Items: []build.CacheRecord{
				{InUse: true, Size: 10},
				{InUse: false, Size: 40},
				{Shared: true, Size: 999},
			},
		},
	}
	want := []DiskUsageRow{
		{Type: DiskImages, Total: 3, Active: 2, Size: 1000, Reclaimable: 700},
		{Type: DiskContainers, Total: 3, Active: 1, Size: 75, Reclaimable: 25},
		{Type: DiskVolumes, Total: 4, Active: 1, Size: 100, Reclaimable: 30},
		{Type: DiskBuildCache, Total: 3, Active: 1, Size: 50, Reclaimable: 40},
	}
	if got := SummarizeDiskUsage(du); !reflect.DeepEqual(got, want) {
		t.Errorf("SummarizeDiskUsage =\n%+v\nwant\n%+v", got, want)
	}
	if got := SummarizeDiskUsage(client.DiskUsageResult{}); len(got) != 4 || got[0].Total != 0 {
		t.Errorf("empty report = %+v", got)
	}
}

func TestApplyDaemonReclaimable(t *testing.T) {
	rows := SummarizeDiskUsage(client.DiskUsageResult{})
	rows[0].Reclaimable, rows[3].Reclaimable = 227, 50
	img, bc := int64(196), int64(40)
	applyDaemonReclaimable(rows, daemonUsage{ImageReclaimable: &img, BuildCacheReclaimable: &bc})
	if rows[0].Reclaimable != 196 || rows[3].Reclaimable != 40 {
		t.Errorf("daemon figures not applied: %+v", rows)
	}
	rows[0].Reclaimable = 227
	applyDaemonReclaimable(rows, daemonUsage{})
	if rows[0].Reclaimable != 227 {
		t.Error("an absent daemon figure overwrote the computed one")
	}
}
