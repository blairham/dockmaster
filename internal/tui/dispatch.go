// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/viewfsm"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// switchView jumps to a top-level view, clearing the drill stack. Used by
// the digit hotkeys and the `:` palette.
func (a *App) switchView(v style.ViewType) tea.Cmd {
	a.history.Visit(viewfsm.ViewID(v))
	return a.showView(v)
}

// showView is switchView without recording a history visit, for the
// history keys themselves.
func (a *App) showView(v style.ViewType) tea.Cmd {
	a.stopStoppableViews()
	// Fullscreen belongs to the log it was turned on in.
	a.setFullscreen(false)
	a.clearContainerScope()
	a.viewStack = nil
	a.view = v
	a.rememberView(v)
	a.showHelp = false
	a.closeAllBars()
	a.filter = ""
	a.setActiveFilter("")
	a.resizeActiveView()
	if pv := typedView[*views.PulsesView](a, style.ViewPulses); pv != nil && v == style.ViewPulses {
		// t in the containers view may have turned the poll off or on.
		pv.SetStatsEnabled(a.statsOn)
	}

	if av := a.activeView(); av != nil {
		return av.Refresh()
	}
	return nil
}

// pushView drills into a view, remembering where we came from.
func (a *App) pushView(v style.ViewType) {
	a.stopImageScan()
	a.viewStack = append(a.viewStack, a.view)
	a.view = v
	a.showHelp = false
	a.closeAllBars()
	a.filter = ""
	a.resizeActiveView()
}

// popView returns to the previous view.
//
// Stoppable teardown deliberately does *not* happen here: escaping out of
// an inspect view back onto a log tail must not kill the tail's stream.
// Teardown belongs to switchView (a real view change) and to shutdown.
func (a *App) popView() {
	if len(a.viewStack) == 0 {
		return
	}
	// Fullscreen belongs to the log it was turned on in.
	a.setFullscreen(false)
	if a.view == style.ViewContainers {
		a.clearContainerScope()
	}
	// The view being left is a drill-in; if it holds a stream, it is
	// genuinely finished with, so stop that one specifically.
	if s, ok := a.viewMap[a.view].(views.Stoppable); ok {
		s.Stop()
	}
	a.view = a.viewStack[len(a.viewStack)-1]
	a.viewStack = a.viewStack[:len(a.viewStack)-1]
	a.closeAllBars()
	a.filter = ""
	a.setActiveFilter("")
	a.resizeActiveView()
}

// closeAllBars closes every chrome bar so the next view never inherits a
// stale one.
func (a *App) closeAllBars() {
	a.commandBar.Close()
	a.filterBar.Close()
	a.prompt.Close()
	a.confirm.Close()
}

// stopStoppableViews tears down every view holding a background stream.
func (a *App) stopStoppableViews() {
	for _, v := range a.viewMap {
		if s, ok := v.(views.Stoppable); ok {
			s.Stop()
		}
	}
}

// contentSize is the inner size of the bordered content box, derived from
// the chrome so the table and the frame can never disagree about how many
// rows are available.
func (a *App) contentSize() (int, int) {
	return a.chrome.ContentInnerSize(
		a.width,
		a.height,
		a.filterBar.Active(),
		a.commandBar.Active() || a.prompt.Active(),
		a.confirm.Active(),
		a.errFlash != "" || a.flash != "",
	)
}

func (a *App) innerWidth() int  { w, _ := a.contentSize(); return w }
func (a *App) tableHeight() int { _, h := a.contentSize(); return h }

func (a *App) resizeActiveView() {
	if v := a.activeView(); v != nil {
		w, h := a.contentSize()
		v.Resize(w, h)
		a.sizedView, a.sizedW, a.sizedH = a.view, w, h
	}
}

// syncViewSize re-sizes the active view when the room it has changed since
// it was last sized. View calls it on every frame.
//
// The content box's height depends on which bars are up — confirm, command,
// filter, a status flash — and those open from many places: a key, an
// action, a confirm dispatched from an action, a message landing. Leaving
// each to remember resizeActiveView is how a confirm bar opened from an
// action came to sit above a table still sized for the whole screen: the box
// grew by three rows and its bottom border and the breadcrumb fell off the
// screen. Checking at render time cannot be forgotten.
func (a *App) syncViewSize() {
	w, h := a.contentSize()
	if a.view != a.sizedView || w != a.sizedW || h != a.sizedH {
		a.resizeActiveView()
	}
}

func (a *App) refreshActiveView() tea.Cmd {
	if v := a.activeView(); v != nil {
		return v.Refresh()
	}
	return nil
}

// polled is the set of views the periodic tick refreshes. Everything else
// loads when opened and reloads only on an explicit <r>.
//
// Two separate reasons, both load-bearing:
//
//   - Refresh() on a STREAM means "start over". LogsView.Refresh cancels
//     the tail and opens a new one; on the tick that would tear down and
//     re-open the stream every three seconds, losing the scrollback and
//     re-fetching the history each time. Inspect and Layers are documents,
//     and Layers is of an immutable object — there is nothing to poll.
//   - Images, volumes and networks are slow-changing inventory bought at a
//     high price. `docker images` on a development host here takes over two
//     minutes (docs/design/daemon-latency.md); polling it buys nothing and
//     holds a daemon connection open continuously.
//
// Containers and projects are the live, cheap, volatile things, so they are
// what the tick is for. Colima profiles are polled because their state moves
// on its own — a start runs for a minute — and listing them is a local
// read that never touches the daemon.
var polled = map[style.ViewType]bool{
	style.ViewContainers: true,
	style.ViewProjects:   true,
	style.ViewRuntimes:   true,
	// One docker ps -a, like the containers view; the tree keeps its place.
	style.ViewXray: true,
	// The dashboard samples on the tick: one docker ps -a and its stats,
	// with disk usage only every minute (views.PulsesView.Poll).
	style.ViewPulses: true,
}

// refreshPolledView is the tick's refresh: a no-op unless the active view
// is one that benefits from polling.
func (a *App) refreshPolledView() tea.Cmd {
	if !polled[a.view] && !a.inspectAutoRefresh() {
		return nil
	}
	if p, ok := a.activeView().(views.Poller); ok {
		return p.Poll()
	}
	return a.refreshActiveView()
}

func (a *App) updateActiveView(msg tea.Msg) tea.Cmd {
	if v := a.activeView(); v != nil {
		return v.Update(msg)
	}
	return nil
}

// updateActiveTable hands navigation keys to the active view's table or
// viewport, translating the keys bubbles does not know about.
func (a *App) updateActiveTable(msg tea.Msg) tea.Cmd {
	v := a.activeView()
	if v == nil {
		return nil
	}
	return v.UpdateTable(msg)
}

func (a *App) activeViewHandleKey(key string) (string, string) {
	v := a.activeView()
	if v == nil {
		return "", ""
	}
	action, param := v.HandleKey(key)
	// k9s's d (describe) and y (yaml) open what o opens here, in any view
	// that does not bind them itself (#3).
	if action == "" && (key == "d" || key == "y") {
		return v.HandleKey("o")
	}
	return action, param
}

func (a *App) setActiveFilter(filter string) {
	if v := a.activeView(); v != nil {
		v.SetFilter(filter)
	}
}

// activeFilter is the filter driving the view right now — the live value
// while the filter bar is open, the committed one otherwise.
func (a *App) activeFilter() string {
	if a.filterBar.Active() {
		return a.filterBar.Value()
	}
	return a.filter
}

// refreshMsgMatchesView reports whether a data message belongs to the
// active view. A stale message from a view the user already left must not
// clear the spinner on the one they are looking at.
func (a *App) refreshMsgMatchesView(msg tea.Msg) bool {
	switch msg.(type) {
	case views.ContainersRefreshMsg, views.ContainerStatsMsg:
		return a.view == style.ViewContainers
	case views.ImagesRefreshMsg:
		return a.view == style.ViewImages
	case views.VolumesRefreshMsg:
		return a.view == style.ViewVolumes
	case views.NetworksRefreshMsg:
		return a.view == style.ViewNetworks
	case views.ProjectsRefreshMsg:
		return a.view == style.ViewProjects
	case views.InspectRefreshMsg:
		return a.view == style.ViewInspect
	case views.LayersRefreshMsg:
		return a.view == style.ViewLayers
	case views.VolumeBrowseMsg:
		return a.view == style.ViewVolumeBrowse
	case views.NodeRefreshMsg:
		return a.view == style.ViewNode
	case views.TopRefreshMsg:
		return a.view == style.ViewTop
	case views.ContextsRefreshMsg:
		return a.view == style.ViewContexts
	case views.DumpsRefreshMsg:
		return a.view == style.ViewDumps
	case views.LintRefreshMsg:
		return a.view == style.ViewLint
	case views.ScanResultMsg:
		return a.view == style.ViewScan
	case views.DirRefreshMsg:
		return a.view == style.ViewDir
	case views.XrayRefreshMsg:
		return a.view == style.ViewXray
	case views.PulsesListMsg, views.PulsesStatsMsg, views.PulsesDiskMsg, views.PulsesEventsMsg:
		return a.view == style.ViewPulses
	case views.DiskUsageRefreshMsg:
		return a.view == style.ViewDiskUsage
	case views.PortForwardsRefreshMsg:
		return a.view == style.ViewPortForwards
	case views.EventsBatchMsg, views.EventsClosedMsg:
		return a.view == style.ViewEvents
	case views.LogBatchMsg, views.LogClosedMsg:
		return a.view == style.ViewLogs
	}
	return true
}

// clearContainerScope lifts a "used by" narrowing when the containers view
// it was opened for is left, so the next visit lists everything again.
func (a *App) clearContainerScope() {
	if cv := typedView[*views.ContainersView](a, style.ViewContainers); cv != nil {
		cv.ClearScope()
	}
}
