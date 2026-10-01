package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/build"
)

// PruneBuildCache removes unused build-cache entries, like
// `docker builder prune` without -a: cache still referenced by an image is
// kept.
func (c *Client) PruneBuildCache(ctx context.Context) (deleted int, reclaimed uint64, err error) {
	rep, err := c.api.BuildCachePrune(ctx, build.CachePruneOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("pruning build cache: %w", err)
	}
	return len(rep.CachesDeleted), rep.SpaceReclaimed, nil
}
