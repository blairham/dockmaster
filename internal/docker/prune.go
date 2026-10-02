// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"
)

// PruneBuildCache removes unused build-cache entries, like
// `docker builder prune` without -a: cache still referenced by an image is
// kept.
func (c *Client) PruneBuildCache(ctx context.Context) (deleted int, reclaimed uint64, err error) {
	res, err := c.api.BuildCachePrune(ctx, client.BuildCachePruneOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("pruning build cache: %w", err)
	}
	return len(res.Report.CachesDeleted), res.Report.SpaceReclaimed, nil
}
