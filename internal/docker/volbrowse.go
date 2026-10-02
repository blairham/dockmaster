package docker

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/pkg/stdcopy"
)

// BrowseImage runs a volume's file listing: any image with a POSIX shell,
// find and stat will do. The port-forward helper's image is one — a few
// megabytes, and already pulled once a forward has run.
const BrowseImage = ForwardImage

// LabelBrowse marks a browse helper with the volume it reads.
const LabelBrowse = "dockmaster.browse"

// browseMount is where a helper sees the volume.
const browseMount = "/v"

// VolumeEntry is one file in a volume directory.
type VolumeEntry struct {
	Modified time.Time
	Name     string
	Type     string // "dir", "file", "link" or "other"
	Mode     string // ls -l style, "drwxr-xr-x"
	Size     int64
}

// Dir reports whether the entry is a directory.
func (e VolumeEntry) Dir() bool { return e.Type == "dir" }

// VolumePath is dir made absolute and clean within the volume, so ".."
// cannot climb out of it: "", "/", "a/../.." all give "/".
func VolumePath(dir string) string { return path.Clean("/" + dir) }

// listScript prints one line per entry of $DMPATH: type|size|mtime|mode|path.
// The path comes in through the environment, never the script text.
const listScript = `cd "` + browseMount + `$DMPATH" || exit 1
find . -mindepth 1 -maxdepth 1 -exec stat -c '%F|%s|%Y|%A|%n' {} +`

// readScript prints at most $DMLIMIT bytes of $DMPATH.
const readScript = `head -c "$DMLIMIT" "` + browseMount + `$DMPATH"`

// BrowseVolume lists one directory of a volume, directories first. Docker
// has no API for a volume's files, so it runs a short-lived helper that
// mounts the volume read-only, with no network, and lists the directory.
func (c *Client) BrowseVolume(ctx context.Context, volume, dir string) ([]VolumeEntry, error) {
	out, err := c.volumeRun(ctx, volume, listScript, "DMPATH="+VolumePath(dir))
	if err != nil {
		return nil, err
	}
	return parseVolumeList(out), nil
}

// ReadVolumeFile returns the first limit bytes of a file in a volume, and
// whether there was more.
func (c *Client) ReadVolumeFile(ctx context.Context, volume, file string, limit int) ([]byte, bool, error) {
	out, err := c.volumeRun(ctx, volume, readScript,
		"DMPATH="+VolumePath(file), "DMLIMIT="+strconv.Itoa(limit+1))
	if err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// parseVolumeList reads listScript's output.
func parseVolumeList(out []byte) []VolumeEntry {
	var entries []VolumeEntry
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.SplitN(line, "|", 5)
		if len(f) != 5 {
			continue
		}
		size, _ := strconv.ParseInt(f[1], 10, 64)  //nolint:errcheck // a malformed size reads as 0
		mtime, _ := strconv.ParseInt(f[2], 10, 64) //nolint:errcheck // a malformed time reads as unset
		e := VolumeEntry{Name: path.Base(f[4]), Size: size, Mode: f[3], Type: "other"}
		if mtime > 0 {
			e.Modified = time.Unix(mtime, 0)
		}
		switch f[0] {
		case "directory":
			e.Type = "dir"
		case "regular file", "regular empty file":
			e.Type = "file"
		case "symbolic link":
			e.Type = "link"
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Dir() != entries[j].Dir() {
			return entries[i].Dir()
		}
		return entries[i].Name < entries[j].Name
	})
	return entries
}

// volumeRun runs script in a helper with volume mounted read-only at /v and
// returns its stdout. The helper is removed however the run ends.
func (c *Client) volumeRun(ctx context.Context, volume, script string, env ...string) ([]byte, error) {
	if _, err := c.api.ImageInspect(ctx, BrowseImage); err != nil {
		if perr := c.PullImage(ctx, BrowseImage); perr != nil {
			return nil, perr
		}
	}
	created, err := c.api.ContainerCreate(ctx,
		&container.Config{
			Image:           BrowseImage,
			Entrypoint:      []string{"sh", "-c", script},
			Env:             env,
			Labels:          map[string]string{LabelBrowse: volume},
			NetworkDisabled: true,
		},
		&container.HostConfig{
			NetworkMode: "none",
			Mounts: []mount.Mount{{
				Type: mount.TypeVolume, Source: volume, Target: browseMount, ReadOnly: true,
			}},
		},
		nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("browsing %s: %w", volume, err)
	}
	defer func() {
		// The caller's context may be spent; removal still has to happen.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = c.api.ContainerRemove(rctx, created.ID, container.RemoveOptions{Force: true}) //nolint:errcheck // best effort
	}()

	if serr := c.api.ContainerStart(ctx, created.ID, container.StartOptions{}); serr != nil {
		return nil, fmt.Errorf("browsing %s: %w", volume, serr)
	}
	waitC, errC := c.api.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	var code int64
	select {
	case w := <-waitC:
		code = w.StatusCode
	case werr := <-errC:
		return nil, fmt.Errorf("browsing %s: %w", volume, werr)
	}

	logs, err := c.api.ContainerLogs(ctx, created.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return nil, fmt.Errorf("browsing %s: %w", volume, err)
	}
	defer logs.Close() //nolint:errcheck // read-only stream
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, logs); err != nil {
		return nil, fmt.Errorf("browsing %s: %w", volume, err)
	}
	if code != 0 {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = fmt.Sprintf("exit %d", code)
		}
		return nil, fmt.Errorf("%s: %s", volume, strings.ReplaceAll(msg, browseMount+"/", "/"))
	}
	return stdout.Bytes(), nil
}
