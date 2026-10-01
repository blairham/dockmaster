package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/versions"
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
func (c *Client) DiskUsage(ctx context.Context) ([]DiskUsageRow, error) {
	du, err := c.api.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return nil, fmt.Errorf("reading disk usage: %w", err)
	}
	rows := SummarizeDiskUsage(du)
	if r, err := c.daemonReclaimable(ctx); err == nil {
		applyDaemonReclaimable(rows, r)
	}
	return rows, nil
}

// daemonUsage is the part of API 1.52+'s /system/df that this SDK predates:
// per-kind summaries in which the daemon reports reclaimable space itself.
type daemonUsage struct {
	ImageUsage      *struct{ Reclaimable *int64 } `json:"ImageUsage"`
	BuildCacheUsage *struct{ Reclaimable *int64 } `json:"BuildCacheUsage"`
}

// daemonDFVersion is the first API version whose /system/df carries the
// per-kind usage summaries.
const daemonDFVersion = "1.52"

// daemonReclaimable asks the daemon for its own reclaimable figures for
// images and build cache.
//
// From Docker 29 the daemon computes these, and with the containerd image
// store its accounting of shared layers differs from the client-side
// formula older CLIs used — on a real host 227MB computed against the
// 196MB `docker system df` prints. Showing a different number from the
// CLI's would be wrong in the way that matters. The request is filtered to
// images and build cache, so it does not repeat the volume walk.
func (c *Client) daemonReclaimable(ctx context.Context) (daemonUsage, error) {
	var du daemonUsage
	v, err := c.api.ServerVersion(ctx)
	if err != nil {
		return du, err
	}
	if versions.LessThan(v.APIVersion, daemonDFVersion) {
		return du, errors.New("daemon predates per-kind disk usage")
	}
	base, err := daemonBaseURL(c.api.DaemonHost(), c.api.HTTPClient())
	if err != nil {
		return du, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/v"+daemonDFVersion+"/system/df?type=image&type=build-cache", nil)
	if err != nil {
		return du, err
	}
	resp, err := c.api.HTTPClient().Do(req)
	if err != nil {
		return du, err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only response
	if resp.StatusCode != http.StatusOK {
		return du, fmt.Errorf("system df: %s", resp.Status)
	}
	return du, json.NewDecoder(resp.Body).Decode(&du)
}

// daemonBaseURL is the URL prefix for a raw request through the SDK's own
// HTTP client. Its transport dials unix sockets and named pipes itself, so
// the host part is a placeholder there; TCP endpoints use their address,
// over TLS when the client is configured for it.
func daemonBaseURL(host string, hc *http.Client) (string, error) {
	u, err := url.Parse(host)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "unix", "npipe":
		return "http://docker", nil
	case "tcp", "http", "https":
		scheme := "http"
		if t, ok := hc.Transport.(*http.Transport); ok && t.TLSClientConfig != nil {
			scheme = "https"
		}
		return scheme + "://" + u.Host, nil
	}
	return "", fmt.Errorf("unsupported docker host %q", host)
}

// applyDaemonReclaimable replaces computed reclaimable figures with the
// daemon's own wherever it reported one.
func applyDaemonReclaimable(rows []DiskUsageRow, du daemonUsage) {
	for i := range rows {
		switch {
		case rows[i].Type == DiskImages && du.ImageUsage != nil && du.ImageUsage.Reclaimable != nil:
			rows[i].Reclaimable = *du.ImageUsage.Reclaimable
		case rows[i].Type == DiskBuildCache && du.BuildCacheUsage != nil && du.BuildCacheUsage.Reclaimable != nil:
			rows[i].Reclaimable = *du.BuildCacheUsage.Reclaimable
		}
	}
}

// SummarizeDiskUsage folds the daemon's raw disk-usage report into the four
// rows the docker CLI prints, computing "active" and "reclaimable" the way
// the CLI does:
//
//   - images: size is the shared layer total; an image is active when a
//     container uses it, and what those images use exclusively is not
//     reclaimable;
//   - containers: size is each writable layer; a running container's is not
//     reclaimable;
//   - volumes: an unreferenced volume is reclaimable; unknown sizes (-1)
//     count as zero;
//   - build cache: shared records are not counted; in-use ones are not
//     reclaimable.
func SummarizeDiskUsage(du types.DiskUsage) []DiskUsageRow {
	img := DiskUsageRow{Type: DiskImages, Total: len(du.Images), Size: du.LayersSize}
	var used int64
	for _, i := range du.Images {
		if i == nil || i.Containers == 0 {
			continue
		}
		img.Active++
		if i.Size != -1 && i.SharedSize != -1 {
			used += i.Size - i.SharedSize
		}
	}
	img.Reclaimable = max(img.Size-used, 0)

	ctr := DiskUsageRow{Type: DiskContainers, Total: len(du.Containers)}
	for _, c := range du.Containers {
		if c == nil {
			continue
		}
		ctr.Size += c.SizeRw
		if c.State == "running" {
			ctr.Active++
		} else {
			ctr.Reclaimable += c.SizeRw
		}
	}

	vol := DiskUsageRow{Type: DiskVolumes, Total: len(du.Volumes)}
	for _, v := range du.Volumes {
		if v == nil || v.UsageData == nil {
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

	bc := DiskUsageRow{Type: DiskBuildCache, Total: len(du.BuildCache)}
	for _, r := range du.BuildCache {
		if r == nil {
			continue
		}
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

	return []DiskUsageRow{img, ctr, vol, bc}
}
