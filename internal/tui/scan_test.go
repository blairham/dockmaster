// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/scan"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeScan answers every scan with three findings and records the ref.
func fakeScan(t *testing.T) *string {
	t.Helper()
	asked := new(string)
	old := views.RunScan
	views.RunScan = func(_ context.Context, ref, _ string) (string, []scan.Vuln, error) {
		*asked = ref
		return "grype", []scan.Vuln{
			{
				ID:        "CVE-2024-24790",
				Severity:  scan.Critical,
				Package:   "stdlib",
				Installed: "go1.20.8",
				FixedIn:   "1.21.11",
				Title:     "net/netip: unexpected behavior",
			},
			{ID: "GO-2023-2102", Severity: scan.High, Package: "stdlib", Installed: "go1.20.8"},
			{ID: "GHSA-xxxx", Severity: scan.Low, Package: "x/net", Installed: "0.1.0"},
		}, nil
	}
	t.Cleanup(func() { views.RunScan = old })
	return asked
}

// TestScanImage: v on an image scans it and lists the findings worst first,
// the title counting them by severity; enter shows one with where to read
// more (#11).
func TestScanImage(t *testing.T) {
	asked := fakeScan(t)
	a := newTestApp(t)
	loadImages(a)
	runCmd(a, step(a, key("v")))
	if a.view != style.ViewScan || *asked != "nginx:1.27" {
		t.Fatalf("v opened %v for %q", a.view, *asked)
	}
	out := render(a)
	crit, high, low := strings.Index(
		out,
		"CVE-2024-24790",
	), strings.Index(
		out,
		"GO-2023-2102",
	), strings.Index(
		out,
		"GHSA-xxxx",
	)
	if crit < 0 || high < 0 || low < 0 || crit > high || high > low {
		t.Fatalf("not worst first:\n%s", out)
	}
	if !strings.Contains(out, "1 critical 1 high 1 low") || !strings.Contains(out, "grype") {
		t.Errorf("title lacks the counts or the scanner:\n%s", out)
	}
	runCmd(a, step(a, key("enter")))
	if r := render(
		a,
	); a.view != style.ViewInspect ||
		!strings.Contains(r, "https://nvd.nist.gov/vuln/detail/CVE-2024-24790") ||
		!strings.Contains(r, "fixed in   1.21.11") {
		t.Errorf("finding report:\n%s", r)
	}
}

// TestScanContainerImage: v on a container scans the image it was created
// from, by ID — its tag may have moved to a newer image since.
func TestScanContainerImage(t *testing.T) {
	asked := fakeScan(t)
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: []docker.Container{
		{ID: "c1", Name: "web", Image: "nginx:1.27", ImageID: "sha256:aaaa", State: "running"},
	}})
	runCmd(a, step(a, key("v")))
	if *asked != "sha256:aaaa" {
		t.Errorf("scanned %q, want the container's image ID", *asked)
	}
}

// TestScanStopsWhenLeft: leaving a scan that is still running cancels it,
// so the scanner does not keep running behind the view; a result from a
// scan since restarted is dropped.
func TestScanStopsWhenLeft(t *testing.T) {
	canceled := make(chan struct{})
	old := views.RunScan
	views.RunScan = func(ctx context.Context, _, _ string) (string, []scan.Vuln, error) {
		<-ctx.Done()
		close(canceled)
		return "", nil, ctx.Err()
	}
	t.Cleanup(func() { views.RunScan = old })

	a := newTestApp(t)
	loadImages(a)
	cmd := step(a, key("v")) // opens the view; cmd is the scan itself
	// Run the scan off the UI goroutine, as bubbletea does, and hand its
	// result back only after: the app is never touched from two goroutines.
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	sv := typedView[*views.ScanView](a, style.ViewScan)
	if !sv.Scanning() {
		t.Fatal("the scan is not running")
	}
	step(a, key("esc"))
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("leaving the view did not cancel the scan")
	}
	step(a, <-result) // the canceled scan's result, after the view was left

	sv2 := views.NewScanView("x", "")
	sv2.Refresh() // gen 1
	sv2.Refresh() // gen 2: the first is stale
	sv2.Update(views.ScanResultMsg{Gen: 1, Vulns: []scan.Vuln{{ID: "stale"}}})
	if sv2.Count() != 0 || !sv2.Scanning() {
		t.Errorf("a stale result was taken: %d rows, scanning %v", sv2.Count(), sv2.Scanning())
	}
	sv2.Stop()
}
