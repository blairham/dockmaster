// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeVolume stands in for the helper container: a tree of directories
// and files, and a record of every listing and read asked for.
type fakeVolume struct {
	dirs  map[string][]docker.VolumeEntry
	files map[string]string
	asked []string
}

func (f *fakeVolume) install(t *testing.T) {
	t.Helper()
	list, read := views.BrowseVolume, views.ReadVolumeFile
	t.Cleanup(func() { views.BrowseVolume, views.ReadVolumeFile = list, read })
	views.BrowseVolume = func(_ context.Context, _ *docker.Client, volume, dir string) ([]docker.VolumeEntry, error) {
		f.asked = append(f.asked, "ls "+volume+":"+dir)
		return f.dirs[dir], nil
	}
	views.ReadVolumeFile = func(_ context.Context, _ *docker.Client, volume, file string, _ int) ([]byte, bool, error) {
		f.asked = append(f.asked, "cat "+volume+":"+file)
		return []byte(f.files[file]), false, nil
	}
}

// browseApp opens the volumes view on one volume, "pgdata", whose root
// holds a directory and a file, and presses enter on it.
func browseApp(t *testing.T) (*App, *fakeVolume) {
	t.Helper()
	f := &fakeVolume{
		dirs: map[string][]docker.VolumeEntry{
			"/":     {{Name: "conf", Type: "dir"}, {Name: "PG_VERSION", Type: "file", Size: 3}},
			"/conf": {{Name: "pg.conf", Type: "file", Size: 20}},
		},
		files: map[string]string{"/PG_VERSION": "16\n"},
	}
	f.install(t)
	a := newTestApp(t)
	step(a, key("2"))
	step(a, views.VolumesRefreshMsg{Volumes: []docker.Volume{{Name: "pgdata", Driver: "local"}}})
	runOnce(a, step(a, key("enter")))
	if a.view != style.ViewVolumeBrowse {
		t.Fatalf("enter on a volume opened %v", a.view)
	}
	return a, f
}

// TestVolumeBrowse: enter lists a volume's root, a directory opens in
// place, esc climbs back out a directory at a time and then leaves.
func TestVolumeBrowse(t *testing.T) {
	a, f := browseApp(t)
	out := render(a)
	if !strings.Contains(out, "pgdata:/") || !strings.Contains(out, "conf/") || !strings.Contains(out, "PG_VERSION") {
		t.Fatalf("root listing:\n%s", out)
	}

	runOnce(a, step(a, key("enter"))) // conf/ sorts first
	out = render(a)
	if !strings.Contains(out, "pgdata:/conf") || !strings.Contains(out, "pg.conf") || strings.Contains(out, "PG_VERSION") {
		t.Errorf("enter on conf/:\n%s", out)
	}

	runOnce(a, step(a, key("esc")))
	if a.view != style.ViewVolumeBrowse || !strings.Contains(render(a), "PG_VERSION") {
		t.Errorf("esc in /conf left the browser or did not climb to /:\n%s", render(a))
	}
	step(a, key("esc"))
	if a.view != style.ViewVolumes {
		t.Errorf("esc at the root went to %v, want volumes", a.view)
	}
	if got := strings.Join(f.asked, "|"); got != "ls pgdata:/|ls pgdata:/conf|ls pgdata:/" {
		t.Errorf("listings asked for: %s", got)
	}
}

func TestVolumeBrowseOpensAFile(t *testing.T) {
	a, f := browseApp(t)
	step(a, key("down"))
	runOnce(a, step(a, key("enter")))
	if a.view != style.ViewInspect {
		t.Fatalf("enter on a file opened %v", a.view)
	}
	runOnce(a, a.viewMap[style.ViewInspect].Init())
	out := render(a)
	if !strings.Contains(out, "pgdata:/PG_VERSION") || !strings.Contains(out, "16") {
		t.Errorf("file view:\n%s", out)
	}
	if f.asked[len(f.asked)-1] != "cat pgdata:/PG_VERSION" {
		t.Errorf("asked %q", f.asked)
	}
}

// TestVolumeBrowseStaleListing: a listing for a directory already left
// does not overwrite the one on screen.
func TestVolumeBrowseStaleListing(t *testing.T) {
	a, _ := browseApp(t)
	step(a, key("enter")) // to /conf, listing not yet back
	step(
		a,
		views.VolumeBrowseMsg{Volume: "pgdata", Dir: "/", Entries: []docker.VolumeEntry{{Name: "stale", Type: "file"}}},
	)
	if strings.Contains(render(a), "stale") {
		t.Error("a listing of / landed in /conf")
	}
}

func TestVolumesOStillInspects(t *testing.T) {
	a := newTestApp(t)
	step(a, key("2"))
	step(a, views.VolumesRefreshMsg{Volumes: []docker.Volume{{Name: "pgdata", Driver: "local"}}})
	step(a, key("o"))
	if a.view != style.ViewInspect {
		t.Errorf("o on a volume opened %v", a.view)
	}
}
