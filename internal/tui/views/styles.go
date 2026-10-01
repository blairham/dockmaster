package views

import (
	"charm.land/bubbles/v2/table"
	tktable "github.com/blairham/tuikit/table"
	"github.com/blairham/tuikit/theme"

	"github.com/blairham/dockyard/internal/tui/style"
)

// pkgTheme is dockyard's theme. It paints the canvas, so tables render in
// tuikit's full paint mode: every cell carries the background, and
// FixSelectedRow re-asserts the selection across per-cell resets.
var pkgTheme = style.Base()

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

// fixSelectedRow repaints selected rows after bubbles renders them. Under
// NoPaintBackground the paint mode is None, so unselected rows pass
// through untouched.
func fixSelectedRow(view string) string {
	return tktable.FixSelectedRow(view, tktable.PaintModeFor(pkgTheme))
}

// truncate shortens s to maxLen terminal cells, appending "…" when it was
// cut — by display width, so a cut never tears a multi-byte rune.
func truncate(s string, maxLen int) string { return tktable.Truncate(s, maxLen) }

// Theme exposes the package theme to the app, which needs the same value
// for chrome and the tail viewport background.
func Theme() theme.Theme { return pkgTheme }
