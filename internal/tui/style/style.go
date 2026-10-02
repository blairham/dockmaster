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

// Base is the theme every dockmaster style derives from. Exported so the
// app can hand the same value to chrome, table, and loading and be sure
// they agree.
func Base() theme.Theme {
	// tuikit's default is k9s's palette (orange logo and labels, aqua
	// titles, light-sky-blue focused border), and dockmaster keeps it as is so
	// the two read the same side by side.
	return theme.Default()
}

var t = theme.Default()

// Palette. The named docker colors are literals; everything else tracks
// the tuikit theme so a tuikit recolor propagates.
var (
	// ColorDockerBlue is Docker's brand blue (#2496ED).
	ColorDockerBlue color.Color = lipgloss.Color("#2496ED")
	// ColorWhaleGray is the muted hull gray used for secondary text.
	ColorWhaleGray color.Color = lipgloss.Color("#8899A6")

	ColorBg    = t.Bg
	ColorWhite = t.Value
	ColorGray  = t.Muted

	ColorGreen  color.Color = t.Status.OK
	ColorRed    color.Color = t.Status.Error
	ColorYellow color.Color = t.Filter
	ColorBlue               = t.Border
	ColorCyan               = t.Accent

	ColorOrange     color.Color = t.Logo
	ColorFuchsia                = t.AccentAlt
	ColorPapayaWhip             = t.AccentBold
	ColorSelection              = t.Selection
	ColorBorder                 = t.Border
	ColorSeaGreen   color.Color = lipgloss.Color("#2E8B57")
	ColorSlateGray  color.Color = lipgloss.Color("#778899")
	ColorPurple     color.Color = lipgloss.Color("#9370DB")
	// ColorLogText is k9s's default log foreground (lightskyblue), so a
	// log pane reads the same in both tools.
	ColorLogText color.Color = lipgloss.Color("#87CEFA")
)

// Info panel (top-left).
var (
	InfoLabel = lipgloss.NewStyle().Foreground(t.Label)
	InfoValue = lipgloss.NewStyle().Foreground(ColorWhite).Bold(true)
)

// Logo is the ASCII-art style.
var Logo = lipgloss.NewStyle().Foreground(t.Logo).Bold(true)

// General text styles.
var (
	Title   = lipgloss.NewStyle().Foreground(ColorCyan).Bold(true)
	Error   = lipgloss.NewStyle().Foreground(ColorRed).Bold(true)
	Success = lipgloss.NewStyle().Foreground(ColorGreen).Bold(true)
	Muted   = lipgloss.NewStyle().Foreground(ColorGray)
)

// State styles color the STATE column. Docker's container states map onto
// k9s's pod-phase coloring: green for the one healthy steady state, red
// for terminal failure, amber for anything mid-transition.
var (
	StateRunning    = lipgloss.NewStyle().Foreground(ColorGreen)
	StateExited     = lipgloss.NewStyle().Foreground(ColorGray)
	StateDead       = lipgloss.NewStyle().Foreground(ColorRed)
	StatePaused     = lipgloss.NewStyle().Foreground(ColorYellow)
	StateRestarting = lipgloss.NewStyle().Foreground(ColorYellow)
	StateCreated    = lipgloss.NewStyle().Foreground(ColorBlue)
)

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
)

// ViewName returns the breadcrumb display name for a view.
func ViewName(v ViewType) string {
	switch v {
	case ViewContainers:
		return "Containers"
	case ViewImages:
		return "Images"
	case ViewVolumes:
		return "Volumes"
	case ViewNetworks:
		return "Networks"
	case ViewProjects:
		return "Projects"
	case ViewRuntimes:
		return "Runtimes"
	case ViewLogs:
		return "Logs"
	case ViewInspect:
		return "Inspect"
	case ViewLayers:
		return "Layers"
	case ViewContexts:
		return "Contexts"
	case ViewRuntimeForm:
		return "Machine"
	case ViewDiskUsage:
		return "Disk Usage"
	case ViewPortForwards:
		return "Port Forwards"
	case ViewPods:
		return "Pods"
	case ViewEvents:
		return "Events"
	case ViewNode:
		return "Node"
	case ViewTop:
		return "Top"
	case ViewRunForm:
		return "Run"
	case ViewCopyForm:
		return "Copy"
	case ViewEditForm:
		return "Edit"
	default:
		return "Unknown"
	}
}

// ViewResource returns the singular resource noun for the border title,
// which renders as "<resource>s(<filter>)[<count>]".
func ViewResource(v ViewType) string {
	switch v {
	case ViewContainers:
		return "container"
	case ViewImages:
		return "image"
	case ViewVolumes:
		return "volume"
	case ViewNetworks:
		return "network"
	case ViewProjects:
		return "project"
	case ViewRuntimes:
		return "runtime"
	case ViewLogs:
		return "line"
	case ViewInspect:
		return "line"
	case ViewLayers:
		return "layer"
	case ViewContexts:
		return "context"
	case ViewRuntimeForm:
		return "field"
	case ViewDiskUsage:
		return "type"
	case ViewPortForwards:
		return "portforward"
	case ViewPods:
		return "pod"
	case ViewEvents:
		return "event"
	case ViewNode:
		return "container"
	case ViewTop:
		return "process"
	case ViewRunForm, ViewCopyForm, ViewEditForm:
		return "field"
	default:
		return ""
	}
}
