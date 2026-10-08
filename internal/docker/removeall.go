// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"fmt"
	"sort"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

// RemoveAllResult is what a delete-all sweep did: how many it removed, how
// many it left because the daemon would not remove them, and the errors
// that were something other than "in use".
type RemoveAllResult struct {
	Err     error
	Removed int
	Skipped int
}

// tally folds one removal's outcome in. A conflict — in use, or an image
// other images are built on — is the expected refusal and only counts; any
// other error is kept, so a timeout or a dead daemon is not reported as
// "skipped".
func (r *RemoveAllResult) tally(err error) {
	switch {
	case err == nil:
		r.Removed++
	case cerrdefs.IsConflict(err):
		r.Skipped++
	default:
		r.Skipped++
		r.Err = errors.Join(r.Err, err)
	}
}

// RemoveAllImages removes every image it can, never forced: an image a
// container uses, running or stopped, is skipped, as is one the daemon
// refuses for any other reason. Newest first, so a child image goes before
// the parent it would otherwise pin.
func (c *Client) RemoveAllImages(ctx context.Context) (RemoveAllResult, error) {
	res, err := c.api.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return RemoveAllResult{}, fmt.Errorf("listing images: %w", err)
	}
	ctrs, err := c.Containers(ctx, true)
	if err != nil {
		return RemoveAllResult{}, err
	}
	inUse := make(map[string]bool, len(ctrs))
	for _, ct := range ctrs {
		inUse[ct.ImageID] = true
	}

	items := res.Items
	sort.SliceStable(items, func(i, j int) bool { return items[i].Created > items[j].Created })

	var out RemoveAllResult
	for _, s := range items {
		// Checked here rather than left to the daemon: removing one tag of
		// a multi-tagged image is an untag, which the daemon allows even
		// while a container runs it.
		if inUse[s.ID] {
			out.Skipped++
			continue
		}
		// Each tag goes by name, so a multi-tagged image needs no force;
		// the last one deletes the image. An untagged one goes by ID.
		refs := keepRealTags(s.RepoTags)
		if len(refs) == 0 {
			refs = []string{s.ID}
		}
		var rmErr error
		for _, ref := range refs {
			if rmErr = c.RemoveImage(ctx, ref, false); rmErr != nil {
				break
			}
		}
		out.tally(rmErr)
	}
	return out, nil
}

// RemoveAllVolumes removes every volume it can, never forced: the daemon
// refuses one a container references, running or stopped, and that one is
// skipped.
func (c *Client) RemoveAllVolumes(ctx context.Context) (RemoveAllResult, error) {
	res, err := c.api.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return RemoveAllResult{}, fmt.Errorf("listing volumes: %w", err)
	}
	var out RemoveAllResult
	for _, v := range res.Items {
		out.tally(c.RemoveVolume(ctx, v.Name, false))
	}
	return out, nil
}
