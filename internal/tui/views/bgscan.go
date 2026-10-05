// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/scan"
)

// ImageScanMsg carries a background scan's result (#63), for the images
// view that asked. The app caches it whichever view is showing.
type ImageScanMsg struct {
	Err     error
	From    *ImagesView
	ImageID string
	Scanner string
	Vulns   []scan.Vuln
	Gen     int
}

// bgScan is the images view's background scanning (imageScans.background):
// one scan at a time, of an image with no fresh cached result, only while
// the view is showing — the app asks for the next with ScanNext and stops
// it with Stop.
type bgScan struct {
	cancel context.CancelFunc
	// failed are the images a scan failed on this session, not retried
	// until the view is built again — a scan that fails fails the same way
	// next time, and retrying would run the scanner back to back forever.
	failed  map[string]bool
	host    string
	current string
	gen     int
	on      bool
	// noScanner is set when neither scanner is installed: there is nothing
	// to retry until one is.
	noScanner bool
}

// SetBackgroundScan turns background scanning on or off, scanning on the
// daemon at host. Off stops a scan that is running.
func (v *ImagesView) SetBackgroundScan(on bool, host string) {
	v.bg.on, v.bg.host = on, host
	if !on {
		v.Stop()
	}
}

// BackgroundScanning is the image a background scan is running on; "" for
// none.
func (v *ImagesView) BackgroundScanning() string { return v.bg.current }

// nextToScan is the first listed image with no fresh cached result that has
// not failed this session, by ID — two tags of one image are one scan.
func (v *ImagesView) nextToScan() string {
	for i := range v.all {
		id := v.all[i].ID
		if id == "" || v.bg.failed[id] || imageScans.cache.Fresh(id) {
			continue
		}
		return id
	}
	return ""
}

// ScanNext starts a background scan of the next image that needs one, and
// is nil when background scanning is off, a scan is already running (one at
// a time), the list has not arrived, or nothing needs scanning. The app
// calls it only while the view is showing.
func (v *ImagesView) ScanNext() tea.Cmd {
	if !v.bg.on || v.bg.noScanner || v.bg.cancel != nil || imageScans.cache == nil {
		return nil
	}
	id := v.nextToScan()
	if id == "" {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	v.bg.gen++
	v.bg.cancel, v.bg.current = cancel, id
	gen, host := v.bg.gen, v.bg.host
	return func() tea.Msg {
		name, vs, err := RunScan(ctx, id, host)
		return ImageScanMsg{From: v, Gen: gen, ImageID: id, Scanner: name, Vulns: vs, Err: err}
	}
}

// Stop cancels a background scan: leaving the view must not leave the
// scanner running behind it. Its result, when it lands, is dropped.
func (v *ImagesView) Stop() {
	if v.bg.cancel != nil {
		v.bg.cancel()
		v.bg.cancel = nil
		v.bg.gen++
	}
	v.bg.current = ""
}

// scanDone folds a background scan's result in: the scan is over, an image
// it failed on is not tried again, and the rows show what the app cached.
func (v *ImagesView) scanDone(m ImageScanMsg) {
	if m.From != v || m.Gen != v.bg.gen {
		return
	}
	if v.bg.cancel != nil {
		v.bg.cancel()
	}
	v.bg.cancel, v.bg.current = nil, ""
	switch {
	case errors.Is(m.Err, scan.ErrNoScanner):
		v.bg.noScanner = true
	case m.Err != nil:
		if v.bg.failed == nil {
			v.bg.failed = map[string]bool{}
		}
		v.bg.failed[m.ImageID] = true
	}
	v.rebuildRows()
}

// Status names the image a background scan is on, for the title.
func (v *ImagesView) Status() string {
	if v.bg.current == "" {
		return ""
	}
	name := v.RefFor(v.bg.current)
	if name == "" || strings.HasPrefix(name, "<none>") {
		name = shortImageID(v.bg.current)
	}
	return "scanning " + name
}

// shortImageID is an image ID as docker images shows it.
func shortImageID(id string) string {
	if _, hex, ok := strings.Cut(id, ":"); ok {
		id = hex
	}
	return id[:min(len(id), 12)]
}

// VulnsChanged redraws the rows after the scan cache took a result.
func (v *ImagesView) VulnsChanged() { v.rebuildRows() }

// VulnsChanged redraws the rows after the scan cache took a result.
func (v *ContainersView) VulnsChanged() { v.rebuildRows() }
