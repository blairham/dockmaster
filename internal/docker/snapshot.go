// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/moby/client"
)

// CommitOptions is `docker commit`'s: the reference to tag the new image
// with (empty leaves it untagged), a message, and whether to pause the
// container while its filesystem is read — docker commit's default.
type CommitOptions struct {
	Ref     string
	Message string
	Pause   bool
}

// Commit is `docker commit`: an image from the container's current
// filesystem and config. It returns the new image's ID.
func (c *Client) Commit(ctx context.Context, id string, opts CommitOptions) (string, error) {
	res, err := c.api.ContainerCommit(ctx, id, client.ContainerCommitOptions{
		Reference: strings.TrimSpace(opts.Ref),
		Comment:   opts.Message,
		NoPause:   !opts.Pause,
	})
	if err != nil {
		return "", fmt.Errorf("committing %s: %w", shortID(id), err)
	}
	return res.ID, nil
}

// ExportContainer is `docker export -o dst`: the container's filesystem,
// flattened, as a tar at dst. It returns the bytes written.
func (c *Client) ExportContainer(ctx context.Context, id, dst string) (int64, error) {
	rc, err := c.api.ContainerExport(ctx, id, client.ContainerExportOptions{})
	if err != nil {
		return 0, fmt.Errorf("exporting %s: %w", shortID(id), err)
	}
	defer rc.Close() //nolint:errcheck // read to the end below
	return WriteTar(dst, rc)
}

// SaveImages is `docker save -o dst refs...`: the images, with their
// layers and tags, as one tar at dst. It returns the bytes written.
func (c *Client) SaveImages(ctx context.Context, refs []string, dst string) (int64, error) {
	if len(refs) == 0 {
		return 0, errors.New("no image to save")
	}
	rc, err := c.api.ImageSave(ctx, refs)
	if err != nil {
		return 0, fmt.Errorf("saving %s: %w", strings.Join(refs, ", "), err)
	}
	defer rc.Close() //nolint:errcheck // read to the end below
	return WriteTar(dst, rc)
}

// LoadImages is `docker load -i src`: the images in a tar written by
// docker save (or SaveImages). It returns what the daemon says it loaded —
// the tags, or the IDs of untagged images.
func (c *Client) LoadImages(ctx context.Context, src string) ([]string, error) {
	f, err := os.Open(src) //nolint:gosec // the user named this file to load
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only
	rc, err := c.api.ImageLoad(ctx, f, client.ImageLoadWithQuiet(true))
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", filepath.Base(src), err)
	}
	defer rc.Close() //nolint:errcheck // read to the end below
	loaded, err := readLoadStream(rc)
	if err != nil {
		return loaded, fmt.Errorf("loading %s: %w", filepath.Base(src), err)
	}
	return loaded, nil
}

// readLoadStream reads the daemon's JSON message stream from an image
// load: "Loaded image: <ref>" or "Loaded image ID: <id>" lines, or an
// error.
func readLoadStream(r io.Reader) ([]string, error) {
	var loaded []string
	dec := json.NewDecoder(r)
	for {
		var m struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return loaded, nil
			}
			return loaded, err
		}
		if m.Error != "" {
			return loaded, errors.New(m.Error)
		}
		for line := range strings.SplitSeq(m.Stream, "\n") {
			for _, prefix := range []string{"Loaded image: ", "Loaded image ID: "} {
				if ref, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok && ref != "" {
					loaded = append(loaded, ref)
				}
			}
		}
	}
}

// WriteTar writes r to dst whole or not at all: into a temporary file
// beside it, renamed over dst only once every byte is down, so a failed
// or canceled export never leaves half a tar — or half replaces an old
// one. The directory is made if missing; the file is 0600, since a
// container's filesystem can hold its secrets.
func WriteTar(dst string, r io.Reader) (int64, error) {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".*.part")
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		if rerr := os.Remove(tmp.Name()); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			err = errors.Join(err, rerr)
		}
		return 0, fmt.Errorf("writing %s: %w", dst, err)
	}
	return n, nil
}
