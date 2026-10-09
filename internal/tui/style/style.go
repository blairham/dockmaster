// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package style holds dockmaster's palette, view enum, and the pre-built
// lipgloss styles the views share. It is a thin layer over
// [github.com/blairham/tuikit/theme] — the colors flow from tuikit's
// [theme.Default], which paints its black canvas behind every cell. The
// whole frame is one color, rather than the terminal's own background
// showing around and between the panes.
//
// The [ViewType] enum and its helpers live here rather than in tuikit
// because they are dockmaster's own resource model.
package style

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/theme"
)

// base is the theme every dockmaster style derives from: tuikit's
// default — k9s's palette, so the two read the same side by side — or
// that with a skin over it (SetBase).
var base = theme.Default()

// onBase are the derived styles other packages keep, re-derived whenever
// the base changes.
var onBase []func()

// DefaultBase is the theme before any skin: what a skin is laid over, so a
// reload lays the new skin over this rather than over the old one.
func DefaultBase() theme.Theme { return theme.Default() }

// Base is the theme every dockmaster style derives from. The app hands it
// to chrome, table and loading so they agree with the views.
func Base() theme.Theme { return base }

// SetBase makes t the theme every style derives from — a skin, inverted
// or not — and re-derives every color and style below, and those other
// packages registered with OnBase. Call it before building the app.
func SetBase(t theme.Theme) {
	base = t
	apply(t)
	for _, fn := range onBase {
		fn()
	}
}

// OnBase registers fn to re-derive a package's own styles when the base
// changes, and runs it now.
func OnBase(fn func()) {
	onBase = append(onBase, fn)
	fn()
}

// Palette. The named docker colors are dockmaster's own; everything else
// tracks the theme, so a skin recolors it.
var (
	// ColorDockerBlue is Docker's brand blue (#2496ED).
	ColorDockerBlue color.Color = lipgloss.Color("#2496ED")
	// ColorWhaleGray is the muted hull gray used for secondary text.
	ColorWhaleGray color.Color = lipgloss.Color("#8899A6")
	ColorSeaGreen  color.Color = lipgloss.Color("#2E8B57")
	ColorSlateGray color.Color = lipgloss.Color("#778899")
	ColorPurple    color.Color = lipgloss.Color("#9370DB")

	ColorBg         color.Color
	ColorWhite      color.Color
	ColorGray       color.Color
	ColorGreen      color.Color
	ColorRed        color.Color
	ColorYellow     color.Color
	ColorBlue       color.Color
	ColorCyan       color.Color
	ColorOrange     color.Color
	ColorFuchsia    color.Color
	ColorPapayaWhip color.Color
	ColorSelection  color.Color
	ColorBorder     color.Color
	// ColorLogText is the log foreground: k9s's lightskyblue by default,
	// so a log pane reads the same in both tools, and a skin's
	// views.logs.fgColor when it sets one.
	ColorLogText color.Color
)

// Shared styles, derived from the palette by apply.
var (
	// Info panel (top-left).
	InfoLabel lipgloss.Style
	InfoValue lipgloss.Style
	// Logo is the ASCII-art style.
	Logo lipgloss.Style
	// General text styles.
	Title   lipgloss.Style
	Error   lipgloss.Style
	Success lipgloss.Style
	Muted   lipgloss.Style
	// State styles color the STATE column. Docker's container states map
	// onto k9s's pod-phase coloring: green for the one healthy steady
	// state, red for terminal failure, amber for anything mid-transition.
	StateRunning    lipgloss.Style
	StateExited     lipgloss.Style
	StateDead       lipgloss.Style
	StatePaused     lipgloss.Style
	StateRestarting lipgloss.Style
	StateCreated    lipgloss.Style
)

func init() { apply(base) }

// apply derives the palette and the shared styles from t.
func apply(t theme.Theme) {
	ColorBg, ColorWhite, ColorGray = t.Bg, t.Value, t.Muted
	ColorGreen, ColorRed, ColorYellow, ColorBlue = t.Status.OK, t.Status.Error, t.Filter, t.Border
	ColorCyan, ColorOrange, ColorFuchsia, ColorPapayaWhip = t.Accent, t.Logo, t.AccentAlt, t.AccentBold
	ColorSelection, ColorBorder = t.Selection, t.Border
	ColorLogText = t.LogTextColor()

	InfoLabel = lipgloss.NewStyle().Foreground(t.Label)
	InfoValue = lipgloss.NewStyle().Foreground(ColorWhite).Bold(true)
	Logo = lipgloss.NewStyle().Foreground(t.Logo).Bold(true)
	Title = lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)
	Error = lipgloss.NewStyle().Foreground(ColorRed).Bold(true)
	Success = lipgloss.NewStyle().Foreground(ColorGreen).Bold(true)
	Muted = lipgloss.NewStyle().Foreground(ColorGray)
	StateRunning = lipgloss.NewStyle().Foreground(ColorGreen)
	StateExited = lipgloss.NewStyle().Foreground(ColorGray)
	StateDead = lipgloss.NewStyle().Foreground(ColorRed)
	StatePaused = lipgloss.NewStyle().Foreground(ColorYellow)
	StateRestarting = lipgloss.NewStyle().Foreground(ColorYellow)
	StateCreated = lipgloss.NewStyle().Foreground(ColorBlue)
}

// StateStyle returns the style for a docker container state string.
func StateStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return StateRunning
	case "paused":
		return StatePaused
	case "restarting", "removing":
		return StateRestarting
	case "created":
		return StateCreated
	case "dead":
		return StateDead
	default: // exited
		return StateExited
	}
}

// HealthStyle returns the style for a healthcheck verdict.
func HealthStyle(health string) lipgloss.Style {
	switch health {
	case "healthy":
		return StateRunning
	case "unhealthy":
		return StateDead
	case "starting":
		return StateRestarting
	default:
		return Muted
	}
}

// ViewType identifies which view is active.
type ViewType int

// View type constants. The first six are the digit-hotkey top-level
// views; the rest are drill-ins reached with enter or a per-view key.
const (
	ViewContainers ViewType = iota
	ViewImages
	ViewVolumes
	ViewNetworks
	ViewProjects
	ViewRuntimes
	ViewLogs
	ViewInspect
	ViewLayers
	ViewContexts
	ViewRuntimeForm
	ViewDiskUsage
	ViewPortForwards
	ViewPods
	ViewEvents
	ViewNode
	ViewTop
	ViewRunForm
	ViewCopyForm
	ViewEditForm
	ViewVolumeBrowse
	ViewDumps
	ViewLint
	ViewScan
	ViewDir
	ViewXray
	ViewPulses
	ViewPluginForm
	ViewScaleForm
	ViewAliases
)

// ViewName returns the breadcrumb display name for a view.
func ViewName(v ViewType) string {
	if name, ok := viewNames[v]; ok {
		return name
	}
	return "Unknown"
}

var viewNames = map[ViewType]string{
	ViewContainers:   "Containers",
	ViewImages:       "Images",
	ViewVolumes:      "Volumes",
	ViewNetworks:     "Networks",
	ViewProjects:     "Projects",
	ViewRuntimes:     "Runtimes",
	ViewLogs:         "Logs",
	ViewInspect:      "Inspect",
	ViewLayers:       "Layers",
	ViewContexts:     "Contexts",
	ViewRuntimeForm:  "Machine",
	ViewDiskUsage:    "Disk Usage",
	ViewPortForwards: "Port Forwards",
	ViewPods:         "Pods",
	ViewEvents:       "Events",
	ViewNode:         "Node",
	ViewTop:          "Top",
	ViewRunForm:      "Run",
	ViewCopyForm:     "Copy",
	ViewEditForm:     "Edit",
	ViewVolumeBrowse: "Files",
	ViewDumps:        "Dumps",
	ViewLint:         "Lint",
	ViewScan:         "Scan",
	ViewDir:          "Dir",
	ViewXray:         "Xray",
	ViewPulses:       "Pulses",
	ViewPluginForm:   "Plugin",
	ViewScaleForm:    "Scale",
	ViewAliases:      "Aliases",
}

// ViewResource returns the singular resource noun for the border title,
// which renders as "<resource>s(<filter>)[<count>]".
func ViewResource(v ViewType) string {
	if name, ok := viewResources[v]; ok {
		return name
	}
	return ""
}

// The resource nouns more than one view shares.
const (
	resContainer = "container"
	resField     = "field"
)

var viewResources = map[ViewType]string{
	ViewContainers:   resContainer,
	ViewImages:       "image",
	ViewVolumes:      "volume",
	ViewNetworks:     "network",
	ViewProjects:     "project",
	ViewRuntimes:     "runtime",
	ViewLogs:         "line",
	ViewInspect:      "line",
	ViewLayers:       "layer",
	ViewContexts:     "context",
	ViewRuntimeForm:  resField,
	ViewDiskUsage:    "type",
	ViewPortForwards: "portforward",
	ViewPods:         "pod",
	ViewEvents:       "event",
	ViewNode:         resContainer,
	ViewTop:          "process",
	ViewRunForm:      resField,
	ViewCopyForm:     resField,
	ViewEditForm:     resField,
	ViewPluginForm:   resField,
	ViewScaleForm:    resField,
	ViewVolumeBrowse: "file",
	ViewDumps:        "dump",
	ViewLint:         resContainer,
	ViewScan:         "vulnerabilitie",
	ViewDir:          "entrie",
	ViewXray:         "node",
	ViewPulses:       "pulse",
	ViewAliases:      "aliase",
}
