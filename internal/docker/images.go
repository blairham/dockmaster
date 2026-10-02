// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

// Image is one row of the images view.
type Image struct {
	Created time.Time

	ID     string
	Repo   string
	Tag    string
	Digest string

	Size       int64
	Containers int64 // -1 when the daemon did not calculate it

	Dangling bool
}

// Ref is the repo:tag reference, or <none>:<none> for a dangling layer.
func (i Image) Ref() string {
	if i.Repo == "" {
		return "<none>"
	}
	if i.Tag == "" {
		return i.Repo
	}
	return i.Repo + ":" + i.Tag
}

// Short is the 12-char image ID.
func (i Image) Short() string { return shortID(i.ID) }

// Age is the build age, one unit.
func (i Image) Age() string { return since(i.Created) }

// Images lists local images. all=false hides intermediate layers, matching
// `docker images`; all=true is `docker images -a`.
//
// One Engine-API row can carry several RepoTags, and docker's own CLI emits
// one line per tag — so do the same here, otherwise a multi-tagged image is
// only actionable under whichever tag happened to sort first.
func (c *Client) Images(ctx context.Context, all bool) ([]Image, error) {
	res, err := c.api.ImageList(ctx, client.ImageListOptions{All: all})
	if err != nil {
		// A deadline here is not a dockmaster problem and the bare
		// "context deadline exceeded" sends people looking in the wrong
		// place. Some daemons genuinely cannot enumerate a large local
		// image store — one here failed to finish in five minutes with
		// 651 images, through the docker CLI as well.
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf(
				"the daemon did not return the image list in time — it may be struggling with a large local image store; `docker image prune` or `docker system df` from a shell is the place to start",
			)
		}
		return nil, fmt.Errorf("listing images: %w", err)
	}

	out := make([]Image, 0, len(res.Items))
	for _, s := range res.Items {
		digest := ""
		if len(s.RepoDigests) > 0 {
			if _, d, ok := strings.Cut(s.RepoDigests[0], "@"); ok {
				digest = shortID(d)
			}
		}
		base := Image{
			ID:         s.ID,
			Digest:     digest,
			Created:    time.Unix(s.Created, 0),
			Size:       s.Size,
			Containers: s.Containers,
		}

		tags := s.RepoTags
		// A dangling image has no RepoTags at all — or the daemon reports
		// the literal "<none>:<none>" placeholder, which must not be shown
		// as if it were a real repository name.
		tags = keepRealTags(tags)
		if len(tags) == 0 {
			base.Dangling = true
			out = append(out, base)
			continue
		}
		for _, t := range tags {
			row := base
			repo, tag, ok := strings.Cut(t, ":")
			// Cut on the LAST colon: a registry with a port (localhost:5000/x)
			// puts a colon in the repo half too.
			if idx := strings.LastIndexByte(t, ':'); idx > strings.LastIndexByte(t, '/') {
				repo, tag, ok = t[:idx], t[idx+1:], true
			}
			if !ok {
				repo, tag = t, "latest"
			}
			row.Repo, row.Tag = repo, tag
			out = append(out, row)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			// Dangling images sort last — they are debris, not inventory.
			if out[i].Repo == "" {
				return false
			}
			if out[j].Repo == "" {
				return true
			}
			return out[i].Repo < out[j].Repo
		}
		return out[i].Tag < out[j].Tag
	})
	return out, nil
}

func keepRealTags(tags []string) []string {
	out := tags[:0:0]
	for _, t := range tags {
		if t == "" || t == "<none>:<none>" || t == "<none>" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// InspectImage returns pretty-printed inspect JSON for an image.
func (c *Client) InspectImage(ctx context.Context, id string) ([]byte, error) {
	var raw bytes.Buffer
	if _, err := c.api.ImageInspect(ctx, id, client.ImageInspectWithRawResponse(&raw)); err != nil {
		return nil, fmt.Errorf("inspecting image %s: %w", shortID(id), err)
	}
	return indentJSON(raw.Bytes())
}

// ImageLayer is one row of the image-history (layers) drill-in.
type ImageLayer struct {
	Created   time.Time
	ID        string
	CreatedBy string
	Comment   string
	Size      int64
}

// ImageHistory returns the build layers of an image, newest first.
func (c *Client) ImageHistory(ctx context.Context, id string) ([]ImageLayer, error) {
	res, err := c.api.ImageHistory(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading history of %s: %w", shortID(id), err)
	}
	out := make([]ImageLayer, 0, len(res.Items))
	for _, h := range res.Items {
		out = append(out, ImageLayer{
			ID:        h.ID,
			Created:   time.Unix(h.Created, 0),
			CreatedBy: cleanBuildStep(h.CreatedBy),
			Size:      h.Size,
			Comment:   h.Comment,
		})
	}
	return out, nil
}

// cleanBuildStep strips the buildkit/shell noise off a history CreatedBy so
// the column shows the Dockerfile instruction rather than its wrapper.
func cleanBuildStep(s string) string {
	s = strings.TrimPrefix(s, "/bin/sh -c #(nop) ")
	s = strings.TrimPrefix(s, "/bin/sh -c ")
	s = strings.TrimPrefix(s, "|")
	return strings.Join(strings.Fields(s), " ")
}

// RemoveImage deletes an image by ID or reference.
func (c *Client) RemoveImage(ctx context.Context, id string, force bool) error {
	_, err := c.api.ImageRemove(ctx, id, client.ImageRemoveOptions{Force: force, PruneChildren: true})
	if err != nil {
		return fmt.Errorf("removing image %s: %w", shortID(id), err)
	}
	return nil
}

// PullImage pulls a reference, draining the progress stream to completion.
// The stream is drained rather than reported: the TUI shows a spinner, and
// abandoning the reader early cancels the pull server-side.
func (c *Client) PullImage(ctx context.Context, ref string) error {
	rc, err := c.api.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pulling %s: %w", ref, err)
	}
	defer rc.Close() //nolint:errcheck // drained below; close error is not actionable

	dec := json.NewDecoder(rc)
	for {
		var line struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&line); err != nil {
			break // EOF, or a malformed trailing chunk — the pull is done either way
		}
		if line.Error != "" {
			return fmt.Errorf("pulling %s: %s", ref, line.Error)
		}
	}
	return nil
}

// PruneImages removes unused images. dangling=true is the conservative
// `docker image prune`; false is `-a`, which deletes every image not
// referenced by a container.
func (c *Client) PruneImages(ctx context.Context, danglingOnly bool) (deleted int, reclaimed uint64, err error) {
	args := make(client.Filters).Add("dangling", fmt.Sprintf("%t", danglingOnly))
	res, err := c.api.ImagePrune(ctx, client.ImagePruneOptions{Filters: args})
	if err != nil {
		return 0, 0, fmt.Errorf("pruning images: %w", err)
	}
	return len(res.Report.ImagesDeleted), res.Report.SpaceReclaimed, nil
}
