package docker

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/moby/moby/client"
)

// Volume is one row of the volumes view.
type Volume struct {
	Created time.Time

	Labels map[string]string

	Name       string
	Driver     string
	Scope      string
	Mountpoint string
	Project    string

	// Size is the on-disk size in bytes, or -1 when the daemon did not
	// report one. Only the `local` driver populates UsageData, and only
	// when the list call asks for it.
	Size int64
	// Refs is how many containers reference the volume, or -1 if unknown.
	Refs int64
}

// Age is the volume's creation age, one unit.
func (v Volume) Age() string { return since(v.Created) }

// Dangling reports whether nothing references the volume. Unknown ref
// counts are treated as referenced — guessing "unused" on missing data is
// how a delete-all sweep eats a database.
func (v Volume) Dangling() bool { return v.Refs == 0 }

// Volumes lists volumes. withSize asks the daemon to walk each volume's
// tree for a byte count, which is slow on large volumes — the view only
// turns it on when the user asks for the size column.
func (c *Client) Volumes(ctx context.Context, withSize bool) ([]Volume, error) {
	opts := client.VolumeListOptions{}
	// The size/ref counts ride on UsageData, which the daemon only fills
	// when the dangling filter forces a full walk. Ask for everything and
	// let the empty-args path stay cheap.
	if withSize {
		opts.Filters = make(client.Filters).Add("dangling", "false", "true")
	}
	resp, err := c.api.VolumeList(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}

	out := make([]Volume, 0, len(resp.Items))
	for _, v := range resp.Items {
		row := Volume{
			Name:       v.Name,
			Driver:     v.Driver,
			Scope:      v.Scope,
			Mountpoint: v.Mountpoint,
			Labels:     v.Labels,
			Project:    v.Labels[LabelProject],
			Size:       -1,
			Refs:       -1,
		}
		if t, err := time.Parse(time.RFC3339, v.CreatedAt); err == nil {
			row.Created = t
		}
		if v.UsageData != nil {
			row.Size = v.UsageData.Size
			row.Refs = v.UsageData.RefCount
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// InspectVolume returns pretty-printed inspect JSON for a volume.
func (c *Client) InspectVolume(ctx context.Context, name string) ([]byte, error) {
	res, err := c.api.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspecting volume %s: %w", name, err)
	}
	return indentJSON(res.Raw)
}

// RemoveVolume deletes a volume.
func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	if _, err := c.api.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: force}); err != nil {
		return fmt.Errorf("removing volume %s: %w", name, err)
	}
	return nil
}

// PruneVolumes removes every volume no container references.
func (c *Client) PruneVolumes(ctx context.Context) (deleted int, reclaimed uint64, err error) {
	// The daemon defaults to anonymous volumes only; `all=true` matches
	// `docker volume prune -a` and is what the confirm prompt describes.
	// All is the client's spelling of that filter: it adds all=true to
	// the filters, the same request the old explicit filter made.
	res, err := c.api.VolumePrune(ctx, client.VolumePruneOptions{All: true})
	if err != nil {
		return 0, 0, fmt.Errorf("pruning volumes: %w", err)
	}
	return len(res.Report.VolumesDeleted), res.Report.SpaceReclaimed, nil
}
