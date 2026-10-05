// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/table"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/blairham/dockmaster/internal/scan"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// VulnTitle is the images and containers views' vulnerability summary
// column (#63), read from cached scans.
const VulnTitle = "VULN"

// vulnWidth is the VULN column's width. A summary is at most
// vulnBudget wide, plus the stale mark.
const (
	vulnWidth  = 11
	vulnBudget = 10
	staleMark  = "~"
)

// imageScans is imageScans: as the app last set it — the cache the VULN
// column reads and whether the column is shown by default. Like the column
// layouts it is package state, read and written on the UI goroutine; the
// cache itself is safe to share with a scan's goroutine.
var imageScans struct {
	cache   *scan.Cache
	enabled bool
}

// SetImageScans sets the cache the VULN column reads and whether it is
// shown without views.yaml naming it. Views already built take it up on
// their next layout; Relayouter applies it at once.
func SetImageScans(c *scan.Cache, enabled bool) {
	imageScans.cache, imageScans.enabled = c, enabled
}

// vulnShown is whether the view of this views.yaml key shows VULN: when
// imageScans.enable is on, or when its views.yaml columns name it.
func vulnShown(layoutKey string) bool {
	if imageScans.enabled {
		return true
	}
	l, ok := columnLayouts[layoutKey]
	return ok && slices.Contains(l.Columns, VulnTitle)
}

// vulnColumns is cols without VULN when the view does not show it, so a
// view with image scans off lays out exactly as it did before there was a
// VULN column. ColumnTitles still lists it, for views.yaml to name.
func vulnColumns(layoutKey string, cols []table.Column) []table.Column {
	if vulnShown(layoutKey) {
		return cols
	}
	return slices.DeleteFunc(slices.Clone(cols), func(c table.Column) bool { return c.Title == VulnTitle })
}

// withVuln is row with the VULN cell inserted at col when the view shows
// the column, matching vulnColumns.
func withVuln(layoutKey string, row table.Row, col int, imageID string) table.Row {
	if !vulnShown(layoutKey) {
		return row
	}
	return slices.Insert(row, col, vulnCell(imageID))
}

// VulnSummary is a cached scan's VULN cell, unstyled: the non-zero counts
// worst first, critical and high always, medium and low only while the
// cell stays within vulnBudget — "C2 H5 M10" — and never a lower one after
// a higher one left out; "0" when the scan found nothing at those four. A
// stale result ends in "~".
func VulnSummary(r scan.Result, stale bool) string {
	c := r.Counts
	var parts []string
	n := 0
	// full is set once a count is left out for room: a lower one after it
	// would read as though the left-out one were zero.
	full := false
	add := func(letter string, count int, always bool) {
		if count == 0 || full {
			return
		}
		p := letter + strconv.Itoa(count)
		w := len(p)
		if n > 0 {
			w++
		}
		if !always && n+w > vulnBudget {
			full = true
			return
		}
		parts = append(parts, p)
		n += w
	}
	add("C", c.Critical, true)
	add("H", c.High, true)
	add("M", c.Medium, false)
	add("L", c.Low, false)
	s := strings.Join(parts, " ")
	if s == "" {
		s = "0"
	}
	if stale {
		s += staleMark
	}
	return s
}

// vulnCell is image id's VULN cell: empty when it was never scanned,
// colored by its worst severity, muted when stale.
func vulnCell(id string) string {
	c := imageScans.cache
	r, ok := c.Get(id)
	if !ok {
		return ""
	}
	stale := c.Stale(r)
	text := VulnSummary(r, stale)
	switch {
	case stale:
		return style.Muted.Render(text)
	case r.Counts.Critical > 0:
		return style.StateDead.Bold(true).Render(text)
	case r.Counts.High > 0:
		return style.StateDead.Render(text)
	case r.Counts.Medium > 0:
		return style.StatePaused.Render(text)
	}
	return text
}

// vulnKey reads a VULN cell back for sorting: whether it holds a scan at
// all, then its critical, high, medium and low counts. A count the cell
// left out for room reads as zero.
func vulnKey(cell string) (bool, [4]int) {
	var k [4]int
	s := strings.TrimSuffix(strings.TrimSpace(xansi.Strip(cell)), staleMark)
	if s == "" {
		return false, k
	}
	for f := range strings.FieldsSeq(s) {
		i := strings.Index("CHML", f[:1])
		if n, err := strconv.Atoi(f[1:]); i >= 0 && err == nil {
			k[i] = n
		}
	}
	return true, k
}

// vulnCompare orders VULN cells by severity: never scanned first, then by
// critical count, then high, medium and low — so a descending sort puts
// the worst image on top, whatever the text would say ("C1" sorts above
// "H90").
func vulnCompare(a, b string) int {
	sa, ka := vulnKey(a)
	sb, kb := vulnKey(b)
	if sa != sb {
		if sa {
			return 1
		}
		return -1
	}
	for i := range ka {
		if c := cmp.Compare(ka[i], kb[i]); c != 0 {
			return c
		}
	}
	return 0
}
