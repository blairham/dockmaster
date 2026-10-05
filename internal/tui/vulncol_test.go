// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/scan"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// cacheScan writes a scan result for id into dir, as an earlier run would
// have, scanned age ago.
func cacheScan(t *testing.T, dir, id string, counts scan.Tally, age time.Duration) {
	t.Helper()
	r := scan.Result{ScannedAt: time.Now().Add(-age), Scanner: "grype", Counts: counts}
	if err := scan.NewCache(dir, 0).Put(id, r); err != nil {
		t.Fatal(err)
	}
}

// scanApp is an app with image scans on, its cache in a temp directory.
func scanApp(t *testing.T, s ImageScans, layouts map[string]views.ColumnLayout) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	return optsApp(t, Options{ImageScans: s, ScanCacheDir: dir, ColumnLayouts: layouts}), dir
}

// rowOf is the rendered line holding text.
func rowOf(out, text string) string {
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, text) {
			return line
		}
	}
	return ""
}

// TestVulnColumnFromCache: with imageScans.enable, images and containers
// show each image's cached scan — critical and high, medium and low when
// they fit — empty for one never scanned, and a stale one marked with ~.
func TestVulnColumnFromCache(t *testing.T) {
	dir := t.TempDir()
	cacheScan(t, dir, "sha256:aaaa", scan.Tally{Critical: 2, High: 5, Medium: 1, Low: 30}, time.Hour)
	cacheScan(t, dir, "sha256:bbbb", scan.Tally{High: 1}, 8*24*time.Hour)
	a := optsApp(t, Options{ImageScans: ImageScans{Enable: true}, ScanCacheDir: dir})
	a.dispatchCommand("images")
	step(a, views.ImagesRefreshMsg{Images: []docker.Image{
		{ID: "sha256:aaaa", Repo: "nginx", Tag: "1.27"},
		{ID: "sha256:bbbb", Repo: "redis", Tag: "7"},
		{ID: "sha256:cccc", Repo: "never", Tag: "scanned"},
	}})
	out := render(a)
	if !strings.Contains(out, "VULN") {
		t.Fatalf("no VULN column:\n%s", out)
	}
	if r := rowOf(out, "nginx"); !strings.Contains(r, "C2 H5 M1") || strings.Contains(r, "L30") {
		t.Errorf("nginx row %q: want C2 H5 M1, L30 left out for room", r)
	}
	if r := rowOf(out, "redis"); !strings.Contains(r, "H1~") {
		t.Errorf("redis row %q: want a stale H1~", r)
	}
	if r := rowOf(out, "never"); regexp.MustCompile(`[CHML]\d|~`).MatchString(r) {
		t.Errorf("an image never scanned shows a summary: %q", r)
	}

	step(a, key("0"))
	step(a, views.ContainersRefreshMsg{Containers: []docker.Container{
		{ID: "c1", Name: "web", Image: "nginx:1.27", ImageID: "sha256:aaaa", State: "running"},
	}})
	if out := render(a); !strings.Contains(out, "VULN") || !strings.Contains(rowOf(out, "web"), "C2 H5 M1") {
		t.Errorf("containers do not show their image's scan:\n%s", out)
	}
	step(a, key("ctrl+w"))
	if out := render(a); !strings.Contains(rowOf(out, "web"), "C2 H5 M1") {
		t.Errorf("wide containers lost the VULN cell:\n%s", out)
	}
}

// TestVulnColumnOffByDefault: without imageScans.enable the views are as
// they were — no VULN column.
func TestVulnColumnOffByDefault(t *testing.T) {
	a, _ := scanApp(t, ImageScans{}, nil)
	loadImages(a)
	if out := render(a); strings.Contains(out, "VULN") {
		t.Errorf("VULN shown with image scans off:\n%s", out)
	}
	loadContainers(a)
	step(a, key("0"))
	if out := render(a); strings.Contains(out, "VULN") {
		t.Errorf("VULN shown in containers with image scans off:\n%s", out)
	}
}

// TestVulnColumnViewsYAML: views.yaml hides VULN with image scans on, and
// shows it with them off — it is a column like any other.
func TestVulnColumnViewsYAML(t *testing.T) {
	hide := map[string]views.ColumnLayout{"images": {Columns: []string{"REPOSITORY", "TAG"}}}
	a, _ := scanApp(t, ImageScans{Enable: true}, hide)
	loadImages(a)
	if out := render(a); strings.Contains(out, "VULN") {
		t.Errorf("views.yaml did not hide VULN:\n%s", out)
	}

	dir := t.TempDir()
	cacheScan(t, dir, "sha256:aaaa", scan.Tally{Critical: 3}, time.Hour)
	show := map[string]views.ColumnLayout{"images": {Columns: []string{"REPOSITORY", "VULN"}}}
	b := optsApp(t, Options{ScanCacheDir: dir, ColumnLayouts: show})
	loadImages(b)
	if out := render(b); !strings.Contains(out, "VULN") || !strings.Contains(rowOf(out, "nginx"), "C3") {
		t.Errorf("views.yaml did not show VULN:\n%s", out)
	}
	// The containers view, not named, keeps its columns.
	step(b, key("0"))
	loadContainers(b)
	if out := render(b); strings.Contains(out, "VULN") {
		t.Errorf("VULN shown in containers, which views.yaml does not name:\n%s", out)
	}
}

// TestVulnColumnSortsBySeverity: sorting on VULN orders by critical count,
// then high — not by the text, which would put H90 above C1 — with images
// never scanned below one scanned clean.
func TestVulnColumnSortsBySeverity(t *testing.T) {
	dir := t.TempDir()
	cacheScan(t, dir, "sha256:aaaa", scan.Tally{High: 90}, time.Hour)
	cacheScan(t, dir, "sha256:bbbb", scan.Tally{Critical: 1}, time.Hour)
	cacheScan(t, dir, "sha256:cccc", scan.Tally{}, time.Hour)
	cacheScan(t, dir, "sha256:eeee", scan.Tally{Critical: 1, High: 2}, 30*24*time.Hour)
	layouts := map[string]views.ColumnLayout{"images": {SortColumn: "VULN", SortDesc: true}}
	a := optsApp(t, Options{ImageScans: ImageScans{Enable: true}, ScanCacheDir: dir, ColumnLayouts: layouts})
	a.dispatchCommand("images")
	step(a, views.ImagesRefreshMsg{Images: []docker.Image{
		{ID: "sha256:aaaa", Repo: "high90", Tag: "1"},
		{ID: "sha256:dddd", Repo: "unscanned", Tag: "1"},
		{ID: "sha256:bbbb", Repo: "crit1", Tag: "1"},
		{ID: "sha256:cccc", Repo: "clean", Tag: "1"},
		{ID: "sha256:eeee", Repo: "stalecrit", Tag: "1"},
	}})
	out := render(a)
	order := []string{"stalecrit", "crit1", "high90", "clean", "unscanned"}
	idx := make([]int, len(order))
	for i, name := range order {
		idx[i] = strings.Index(out, name)
	}
	if !slices.IsSorted(idx) || idx[0] < 0 {
		t.Errorf("want %v, worst first:\n%s", order, out)
	}
	if r := rowOf(out, "clean"); !strings.Contains(r, " 0 ") {
		t.Errorf("a clean scan reads %q, want 0", r)
	}
}

// TestScanCachesResult: a `v` scan's result is cached by the image's ID —
// tagged or not — and the VULN column shows it on returning.
func TestScanCachesResult(t *testing.T) {
	fakeScan(t)
	a, dir := scanApp(t, ImageScans{Enable: true}, nil)
	loadImages(a)
	runCmd(a, step(a, key("v")))
	if a.view != style.ViewScan {
		t.Fatalf("v opened %v", a.view)
	}
	b, err := os.ReadFile(filepath.Join(dir, "sha256-aaaa.json"))
	if err != nil {
		t.Fatalf("the scan of nginx:1.27 was not cached by its ID: %v", err)
	}
	for _, want := range []string{`"scanner": "grype"`, `"critical": 1`, `"high": 1`, `"low": 1`, `"scannedAt"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("cache file lacks %s:\n%s", want, b)
		}
	}
	step(a, key("esc"))
	if r := rowOf(render(a), "nginx"); !strings.Contains(r, "C1 H1 L1") {
		t.Errorf("nginx row after the scan: %q", r)
	}
	// The untagged image scans, and caches, by ID.
	step(a, key("down"))
	runCmd(a, step(a, key("v")))
	if _, err := os.Stat(filepath.Join(dir, "sha256-bbbb.json")); err != nil {
		t.Errorf("the untagged image's scan was not cached: %v", err)
	}
}

// TestScanCacheWriteFailureIsLogged: a cache that cannot be written is
// logged, not flashed — the scan still shows, and the column still has it
// for this run.
func TestScanCacheWriteFailureIsLogged(t *testing.T) {
	fakeScan(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	a := optsApp(t, Options{
		ImageScans:   ImageScans{Enable: true},
		ScanCacheDir: filepath.Join(blocker, "scans"), // under a file: cannot be made
		Logger:       slog.New(slog.NewTextHandler(&logBuf, nil)),
	})
	loadImages(a)
	runCmd(a, step(a, key("v")))
	if a.errFlash != "" {
		t.Errorf("a cache write failure was flashed: %q", a.errFlash)
	}
	if !strings.Contains(logBuf.String(), "scan result not cached") {
		t.Errorf("the failure was not logged: %q", logBuf.String())
	}
	step(a, key("esc"))
	if r := rowOf(render(a), "nginx"); !strings.Contains(r, "C1 H1 L1") {
		t.Errorf("the result is not in memory for this run: %q", r)
	}
}

// bgScanner is a fake scanner for background scans: it records what it was
// asked and how many ran at once, and blocks each scan until released.
type bgScanner struct {
	release chan struct{}
	asked   []string
	mu      sync.Mutex
	running int
	most    int
}

func fakeBackground(t *testing.T, block bool) *bgScanner {
	t.Helper()
	f := &bgScanner{release: make(chan struct{})}
	if !block {
		close(f.release)
	}
	old := views.RunScan
	views.RunScan = func(ctx context.Context, ref, _ string) (string, []scan.Vuln, error) {
		f.mu.Lock()
		f.asked = append(f.asked, ref)
		f.running++
		f.most = max(f.most, f.running)
		f.mu.Unlock()
		defer func() { f.mu.Lock(); f.running--; f.mu.Unlock() }()
		select {
		case <-f.release:
		case <-ctx.Done():
			return "", nil, ctx.Err()
		}
		return "trivy", []scan.Vuln{{ID: "CVE-1", Severity: scan.Critical}}, nil
	}
	t.Cleanup(func() { views.RunScan = old })
	return f
}

func (f *bgScanner) askedFor() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

var threeImages = views.ImagesRefreshMsg{Images: []docker.Image{
	{ID: "sha256:aaaa", Repo: "nginx", Tag: "1.27"},
	{ID: "sha256:bbbb", Repo: "redis", Tag: "7"},
	{ID: "sha256:aaaa", Repo: "nginx", Tag: "latest"}, // the same image again
	{ID: "sha256:cccc", Repo: "postgres", Tag: "17"},
}}

// TestBackgroundScanOff: with background off — the default, or with the
// column off — nothing scans unless v is pressed.
func TestBackgroundScanOff(t *testing.T) {
	for name, s := range map[string]ImageScans{
		"default":     {},
		"enable only": {Enable: true},
		"no column":   {Background: true},
	} {
		t.Run(name, func(t *testing.T) {
			f := fakeBackground(t, false)
			a, _ := scanApp(t, s, nil)
			a.dispatchCommand("images")
			if cmd := step(a, threeImages); cmd != nil {
				t.Errorf("the image list started something: %T", cmd())
			}
			if cmd := a.scanNextImage(); cmd != nil {
				t.Error("the tick would start a scan")
			}
			if got := f.askedFor(); len(got) != 0 {
				t.Errorf("scanned %v", got)
			}
		})
	}
}

// TestBackgroundScanOneAtATime: with background on, the images view scans
// each image with no fresh result in turn — never two at once, one scan per
// image ID whatever its tags, skipping a fresh result and rescanning a
// stale one — caching each and showing it.
func TestBackgroundScanOneAtATime(t *testing.T) {
	f := fakeBackground(t, false)
	dir := t.TempDir()
	cacheScan(t, dir, "sha256:bbbb", scan.Tally{High: 1}, time.Hour)       // fresh: skipped
	cacheScan(t, dir, "sha256:cccc", scan.Tally{High: 1}, 10*24*time.Hour) // stale: rescanned
	a := optsApp(t, Options{ImageScans: ImageScans{Enable: true, Background: true}, ScanCacheDir: dir})
	a.dispatchCommand("images")
	cmd := step(a, threeImages)
	if cmd == nil {
		t.Fatal("the image list did not start a background scan")
	}
	iv := typedView[*views.ImagesView](a, style.ViewImages)
	if iv.BackgroundScanning() != "sha256:aaaa" || !strings.Contains(render(a), "scanning nginx:1.27") {
		t.Errorf("scanning %q; title:\n%s", iv.BackgroundScanning(), render(a))
	}
	if a.scanNextImage() != nil {
		t.Error("a second scan started while one is running")
	}
	for range 5 {
		if cmd == nil {
			break
		}
		cmd = step(a, cmd())
		if cmd != nil && a.scanNextImage() != nil {
			t.Error("a second scan started while one is running")
		}
	}
	if cmd != nil {
		t.Fatal("background scanning did not finish")
	}
	if got := f.askedFor(); !slices.Equal(got, []string{"sha256:aaaa", "sha256:cccc"}) {
		t.Errorf("scanned %v, want aaaa then the stale cccc", got)
	}
	if f.most != 1 {
		t.Errorf("%d scans ran at once", f.most)
	}
	for _, id := range []string{"aaaa", "cccc"} {
		b, err := os.ReadFile(filepath.Join(dir, "sha256-"+id+".json"))
		if err != nil || !strings.Contains(string(b), `"scanner": "trivy"`) {
			t.Errorf("%s not cached: %v %s", id, err, b)
		}
	}
	out := render(a)
	if !strings.Contains(rowOf(out, "postgres"), "C1") || strings.Contains(rowOf(out, "postgres"), "~") {
		t.Errorf("the rescanned image is not shown fresh:\n%s", out)
	}
	if !strings.Contains(rowOf(out, "redis"), "H1") {
		t.Errorf("the fresh result was lost:\n%s", out)
	}
	if a.scanNextImage() != nil {
		t.Error("a scan started with every image fresh")
	}
}

// TestBackgroundScanStopsWhenLeft: leaving the images view — for another
// view, or into a v scan — cancels the background scan, so a scanner never
// runs behind another view or beside a v scan; the canceled scan's result
// is dropped and nothing starts until the view is back.
func TestBackgroundScanStopsWhenLeft(t *testing.T) {
	for name, leave := range map[string]func(*App) tea.Cmd{
		"switch": func(a *App) tea.Cmd { return step(a, key("0")) },
		"v scan": func(a *App) tea.Cmd { return step(a, key("v")) },
	} {
		t.Run(name, func(t *testing.T) {
			f := fakeBackground(t, true)
			a, dir := scanApp(t, ImageScans{Enable: true, Background: true}, nil)
			a.dispatchCommand("images")
			cmd := step(a, threeImages)
			if cmd == nil {
				t.Fatal("no background scan started")
			}
			result := make(chan tea.Msg, 1)
			go func() { result <- cmd() }()
			for len(f.askedFor()) == 0 {
				time.Sleep(time.Millisecond)
			}
			leave(a)
			var m tea.Msg
			select {
			case m = <-result:
			case <-time.After(2 * time.Second):
				t.Fatal("leaving the view did not cancel the background scan")
			}
			if next := step(a, m); next != nil {
				t.Error("the canceled scan's result started another")
			}
			if a.scanNextImage() != nil {
				t.Error("a background scan would start off the images view")
			}
			if _, err := os.Stat(filepath.Join(dir, "sha256-aaaa.json")); err == nil {
				t.Error("a canceled scan was cached")
			}
			// Back on the view, the next tick resumes.
			step(a, key("esc"))
			if name == "switch" {
				step(a, key("1"))
			}
			if a.view != style.ViewImages || a.scanNextImage() == nil {
				t.Errorf("background scanning did not resume on %v", a.view)
			}
			typedView[*views.ImagesView](a, style.ViewImages).Stop()
		})
	}
}

// TestBackgroundScanNoScanner: with no scanner installed, the background
// scan stops after the first attempt instead of failing every image.
func TestBackgroundScanNoScanner(t *testing.T) {
	asked := 0
	old := views.RunScan
	views.RunScan = func(context.Context, string, string) (string, []scan.Vuln, error) {
		asked++
		return "", nil, scan.ErrNoScanner
	}
	t.Cleanup(func() { views.RunScan = old })
	a, _ := scanApp(t, ImageScans{Enable: true, Background: true}, nil)
	a.dispatchCommand("images")
	cmd := step(a, threeImages)
	for cmd != nil && asked < 5 {
		cmd = step(a, cmd())
	}
	if asked != 1 || a.scanNextImage() != nil || a.errFlash != "" {
		t.Errorf("asked %d times, flash %q", asked, a.errFlash)
	}
}

// TestReloadAppliesImageScans: turning imageScans.enable on in a saved
// config.yaml shows VULN without a restart (ui.reactive).
func TestReloadAppliesImageScans(t *testing.T) {
	a, dir := reactiveApp(t, func() (Reloaded, error) {
		return Reloaded{Options: Options{ImageScans: ImageScans{Enable: true}}}, nil
	})
	loadImages(a)
	v := typedView[*views.ImagesView](a, style.ViewImages)
	if slices.Contains(titles(v.Table()), "VULN") {
		t.Fatal("setup: VULN shown before the reload")
	}
	write(t, filepath.Join(dir, "config.yaml"), "dockmaster: {}\n")
	reloadNow(t, a)
	if !slices.Contains(titles(v.Table()), "VULN") {
		t.Errorf("VULN not shown after the reload: %q", titles(v.Table()))
	}
}
