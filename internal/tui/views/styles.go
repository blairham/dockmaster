// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
	tktable "github.com/blairham/tuikit/table"
	"github.com/blairham/tuikit/theme"

	"github.com/blairham/dockmaster/internal/tui/style"
)

// pkgTheme is dockmaster's theme — style.Base, skin included. Painting
// the canvas, tables render in tuikit's full paint mode: every cell
// carries the background, and FixSelectedRow re-asserts the selection
// across per-cell resets.
var pkgTheme theme.Theme

func init() { style.OnBase(applyTheme) }

// applyTheme derives every style the views keep at package level from the
// current base theme. A package-level style must be set here, not in its
// declaration, or a skin would never reach it — TestNoPackageStyleSkipsTheSkin
// holds every file to that.
func applyTheme() {
	pkgTheme = style.Base()
	eventTimeStyle = style.Muted
	eventTypeStyle = lipgloss.NewStyle().Foreground(style.ColorCyan)
	eventNameStyle = lipgloss.NewStyle().Foreground(style.ColorWhite).Bold(true)
	jsonKeyStyle = lipgloss.NewStyle().Foreground(style.ColorDockerBlue)
	jsonStrStyle = lipgloss.NewStyle().Foreground(style.ColorGreen)
	jsonNumStyle = lipgloss.NewStyle().Foreground(style.ColorPapayaWhip)
	jsonNilStyle = lipgloss.NewStyle().Foreground(style.ColorSlateGray)
	logTextFg = sgrFor(style.ColorLogText)
	dirStyle = lipgloss.NewStyle().Foreground(style.ColorDockerBlue).Bold(true)
}

// Key names shared across views. Spelled here once so a rebind is a
// one-line change rather than a grep.
const (
	KeyEnter  = "enter"
	KeyCtrlD  = "ctrl+d"
	KeyDelete = "delete"
)

// rowFilter is a regex row filter with leading-`!` negation and an
// invalid-regex literal fallback. Re-exported from tuikit/table.
type rowFilter = tktable.RowFilter

func parseFilter(s string) rowFilter { return tktable.ParseFilter(s) }

// tableKeyMap returns the bubbles table keymap with j/k stripped (the app
// translates those itself so they work uniformly across tables and
// viewports).
func tableKeyMap() table.KeyMap { return tktable.KeyMap() }

func tableStyles() table.Styles { return tktable.Styles(pkgTheme) }

// tableStylesWithWidth pads Selected to the full table width so the
// highlight runs edge to edge.
func tableStylesWithWidth(width int) table.Styles {
	return tktable.StylesWithWidth(pkgTheme, width)
}

// fixSelectedRow repaints a table after bubbles renders it, in the
// theme's own colors — a skin's selection and canvas, not the defaults —
// so the selected row is found and styled cells keep the canvas behind
// them. Under NoPaintBackground unselected rows pass through untouched.
func fixSelectedRow(view string) string { return tktable.FixRows(view, pkgTheme) }

// truncate shortens s to maxLen terminal cells, appending "…" when it was
// cut — by display width, so a cut never tears a multi-byte rune.
func truncate(s string, maxLen int) string { return tktable.Truncate(s, maxLen) }

// Theme exposes the package theme to the app, which needs the same value
// for chrome and the tail viewport background.
func Theme() theme.Theme { return pkgTheme }
