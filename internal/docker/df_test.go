package docker

import (
	"crypto/tls"
	"net/http"
	"reflect"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
)

// TestSummarizeDiskUsage pins the docker CLI's arithmetic for every row,
// including the cases that are easy to get wrong: an image's shared layers
// are not "used", a paused container is reclaimable (only running is not),
// unknown volume sizes count as zero, and shared build cache is not
// counted at all.
func TestSummarizeDiskUsage(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: 1000,
		Images: []*image.Summary{
			{Containers: 2, Size: 400, SharedSize: 100}, // uses 300 exclusively
			{Containers: 0, Size: 300, SharedSize: 100},
			{Containers: 1, Size: -1, SharedSize: -1}, // unknown: active, not subtracted
		},
		Containers: []*container.Summary{
			{State: "running", SizeRw: 50},
			{State: "exited", SizeRw: 20},
			{State: "paused", SizeRw: 5},
		},
		Volumes: []*volume.Volume{
			{UsageData: &volume.UsageData{RefCount: 1, Size: 70}},
			{UsageData: &volume.UsageData{RefCount: 0, Size: 30}},
			{UsageData: &volume.UsageData{RefCount: 0, Size: -1}},
			{},
		},
		BuildCache: []*build.CacheRecord{
			{InUse: true, Size: 10},
			{InUse: false, Size: 40},
			{Shared: true, Size: 999},
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
	if got := SummarizeDiskUsage(types.DiskUsage{}); len(got) != 4 || got[0].Total != 0 {
		t.Errorf("empty report = %+v", got)
	}
}

func TestApplyDaemonReclaimable(t *testing.T) {
	rows := SummarizeDiskUsage(types.DiskUsage{})
	rows[0].Reclaimable, rows[3].Reclaimable = 227, 50
	img, bc := int64(196), int64(40)
	applyDaemonReclaimable(rows, daemonUsage{
		ImageUsage:      &struct{ Reclaimable *int64 }{Reclaimable: &img},
		BuildCacheUsage: &struct{ Reclaimable *int64 }{Reclaimable: &bc},
	})
	if rows[0].Reclaimable != 196 || rows[3].Reclaimable != 40 {
		t.Errorf("daemon figures not applied: %+v", rows)
	}
	rows[0].Reclaimable = 227
	applyDaemonReclaimable(rows, daemonUsage{})
	if rows[0].Reclaimable != 227 {
		t.Error("an absent daemon figure overwrote the computed one")
	}
}

func TestDaemonBaseURL(t *testing.T) {
	for host, want := range map[string]string{
		"unix:///Users/u/.colima/default/docker.sock": "http://docker",
		"tcp://10.0.0.5:2375":                         "http://10.0.0.5:2375",
	} {
		if got, err := daemonBaseURL(host, &http.Client{}); err != nil || got != want {
			t.Errorf("daemonBaseURL(%q) = %q, %v; want %q", host, got, err, want)
		}
	}
	tlsClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
	if got, _ := daemonBaseURL("tcp://h:2376", tlsClient); got != "https://h:2376" {
		t.Errorf("TLS tcp host = %q", got)
	}
	if _, err := daemonBaseURL("ssh://h", &http.Client{}); err == nil {
		t.Error("ssh host accepted")
	}
}
