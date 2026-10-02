package docker

import (
	"context"
	"fmt"
	"sort"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Processes is `docker top`: the processes in a container, as ps on the
// daemon's host reports them, column titles and rows as given.
type Processes struct {
	Titles []string
	Rows   [][]string
}

// Top lists the processes running in container id.
func (c *Client) Top(ctx context.Context, id string) (Processes, error) {
	top, err := c.api.ContainerTop(ctx, id, client.ContainerTopOptions{})
	if err != nil {
		return Processes{}, fmt.Errorf("listing processes in %s: %w", shortID(id), err)
	}
	return Processes{Titles: top.Titles, Rows: top.Processes}, nil
}

// FileChange is one path a container changed against its image.
type FileChange struct {
	Path string
	Kind string // A added, C changed, D deleted — docker diff's letters
}

// Diff is `docker diff`: what container id has written over its image,
// sorted by path so a directory's changes stay together.
func (c *Client) Diff(ctx context.Context, id string) ([]FileChange, error) {
	res, err := c.api.ContainerDiff(ctx, id, client.ContainerDiffOptions{})
	if err != nil {
		return nil, fmt.Errorf("diffing %s: %w", shortID(id), err)
	}
	return fileChanges(res.Changes), nil
}

func fileChanges(changes []container.FilesystemChange) []FileChange {
	out := make([]FileChange, 0, len(changes))
	for _, ch := range changes {
		kind := "C"
		switch ch.Kind {
		case container.ChangeAdd:
			kind = "A"
		case container.ChangeDelete:
			kind = "D"
		}
		out = append(out, FileChange{Path: ch.Path, Kind: kind})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// StatsFor samples one container's stats: the numbers behind the
// containers view's CPU and MEM columns, and the rest of `docker stats`.
func (c *Client) StatsFor(ctx context.Context, id string) (Stats, error) {
	return c.sampleOne(ctx, id)
}
