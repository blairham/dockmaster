package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/go-archive"
	"github.com/moby/moby/client"
)

// LocalPath resolves a path typed on this machine: ~ is the home
// directory, and a relative path is relative to where dockmaster was
// started, as it would be for the docker CLI.
func LocalPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("no local path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Abs(p)
}

// CopyFrom is `docker cp id:src dst`: srcPath in the container to dstPath
// on this machine. Directory-versus-contents and missing-destination rules
// are the CLI's own, from moby/go-archive.
func (c *Client) CopyFrom(ctx context.Context, id, srcPath, dstPath string) error {
	dst, err := LocalPath(dstPath)
	if err != nil {
		return err
	}
	res, err := c.api.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: srcPath})
	if err != nil {
		return fmt.Errorf("reading %s from %s: %w", srcPath, shortID(id), err)
	}
	defer res.Content.Close() //nolint:errcheck // read-only stream
	src := archive.CopyInfo{Path: srcPath, Exists: true, IsDir: res.Stat.Mode.IsDir()}
	if err := archive.CopyTo(res.Content, src, dst); err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	return nil
}

// CopyInto is `docker cp src id:dst`: srcPath on this machine into
// dstPath in the container.
func (c *Client) CopyInto(ctx context.Context, id, srcPath, dstPath string) error {
	src, err := LocalPath(srcPath)
	if err != nil {
		return err
	}
	srcInfo, err := archive.CopyInfoSourcePath(src, false)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	srcArchive, err := archive.TarResource(srcInfo)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	defer srcArchive.Close() //nolint:errcheck // read-only stream

	dstInfo := archive.CopyInfo{Path: dstPath}
	stat, err := c.api.ContainerStatPath(ctx, id, client.ContainerStatPathOptions{Path: dstPath})
	switch {
	case err == nil:
		dstInfo.Exists, dstInfo.IsDir = true, stat.Stat.Mode.IsDir()
	case !cerrdefs.IsNotFound(err):
		return fmt.Errorf("checking %s in %s: %w", dstPath, shortID(id), err)
	}
	dstDir, prepared, err := archive.PrepareArchiveCopy(srcArchive, srcInfo, dstInfo)
	if err != nil {
		return fmt.Errorf("preparing the copy: %w", err)
	}
	defer prepared.Close() //nolint:errcheck // read-only stream
	if _, err := c.api.CopyToContainer(ctx, id, client.CopyToContainerOptions{
		DestinationPath: dstDir,
		Content:         prepared,
	}); err != nil {
		return fmt.Errorf("writing %s into %s: %w", dstPath, shortID(id), err)
	}
	return nil
}
