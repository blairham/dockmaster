// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/scan"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// ImageScans is config.yaml's imageScans block (#63).
//
//   - Enable shows the VULN column in the images and containers views,
//     from cached scans. Off, views.yaml can still name it.
//   - Background scans images with no fresh result, one at a time, while
//     the images view is showing. It needs Enable.
//   - TTL is how long a cached result is fresh; zero is scan.DefaultTTL.
type ImageScans struct {
	TTL        time.Duration
	Enable     bool
	Background bool
}

// applyImageScans puts imageScans into effect: the cache's TTL, the column,
// and background scanning on the images view. NewApp, a context switch
// (which builds the images view again) and a reload call it.
func (a *App) applyImageScans(s ImageScans) {
	a.imageScans = s
	a.scans.SetTTL(s.TTL)
	views.SetImageScans(a.scans, s.Enable)
	if iv := typedView[*views.ImagesView](a, style.ViewImages); iv != nil {
		iv.SetBackgroundScan(s.Enable && s.Background, a.dockerHostArg())
	}
}

// scanNextImage starts the images view's next background scan, only while
// that view is the one showing. The scan runs as a command, off the UI
// goroutine, so it never holds up the poll.
func (a *App) scanNextImage() tea.Cmd {
	if a.view != style.ViewImages {
		return nil
	}
	if iv := typedView[*views.ImagesView](a, style.ViewImages); iv != nil {
		return iv.ScanNext()
	}
	return nil
}

// stopImageScan stops a background scan when the images view is covered
// by a drill-in — a `v` scan most of all, which would otherwise run beside
// it. Returning to the view resumes on the next tick.
func (a *App) stopImageScan() {
	if a.view != style.ViewImages {
		return
	}
	if iv := typedView[*views.ImagesView](a, style.ViewImages); iv != nil {
		iv.Stop()
	}
}

// recordScan caches a finished scan of image id, from `v` or the
// background, and redraws the VULN cells. Writing the file is
// best-effort: a failure is logged, never flashed — the result is in
// memory for this run either way.
func (a *App) recordScan(id, scanner string, vs []scan.Vuln, err error, background bool) {
	if err != nil {
		if background && !errors.Is(err, context.Canceled) {
			a.log.Warn("background scan failed", "image", id, "error", err)
		}
		return
	}
	if id == "" {
		return
	}
	r := scan.Result{ScannedAt: time.Now().UTC(), Scanner: scanner, Counts: scan.TallyOf(vs)}
	if werr := a.scans.Put(id, r); werr != nil {
		a.log.Warn("scan result not cached", "image", id, "error", werr)
	}
	if iv := typedView[*views.ImagesView](a, style.ViewImages); iv != nil {
		iv.VulnsChanged()
	}
	if cv := typedView[*views.ContainersView](a, style.ViewContainers); cv != nil {
		cv.VulnsChanged()
	}
}

// handleImageScan caches a background scan's result, hands it to the
// images view, and starts the next while that view is showing.
func (a *App) handleImageScan(m views.ImageScanMsg) (tea.Model, tea.Cmd) {
	a.recordScan(m.ImageID, m.Scanner, m.Vulns, m.Err, true)
	if iv := typedView[*views.ImagesView](a, style.ViewImages); iv != nil {
		iv.Update(m)
	}
	return a, a.scanNextImage()
}

// scanImageID is the ID of the image a `v` scan's param names: the param
// itself when it is an ID (a container's image, an untagged one), else the
// images view's image of that reference; "" when neither says.
func (a *App) scanImageID(param string) string {
	if strings.HasPrefix(param, "sha256:") {
		return param
	}
	if iv := typedView[*views.ImagesView](a, style.ViewImages); iv != nil {
		return iv.IDFor(param)
	}
	return ""
}
