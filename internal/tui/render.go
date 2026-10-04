// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/chrome"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// View renders the whole screen. The frame — top section, bars, help
// overlay, status bar, breadcrumb footer — belongs to tuikit/chrome; this
// only populates a Frame.
func (a *App) View() tea.View {
	if a.width == 0 || a.splashActive {
		v := tea.NewView(a.renderSplash())
		v.AltScreen = true
		return v
	}

	a.syncViewSize()
	info := a.renderInfoPanel()
	shortcuts := a.renderShortcuts(info)
	frame := chrome.Frame{
		Width:       a.width,
		Height:      a.height,
		InfoLines:   info,
		Shortcuts:   shortcuts,
		Content:     a.renderContent(),
		Breadcrumb:  a.breadcrumb(),
		HelpVisible: a.showHelp,
		Help:        a.helpPanel(),
	}

	switch {
	case a.errFlash != "":
		frame.StatusBar = a.errFlash
		frame.StatusBarLevel = chrome.LevelError
	case a.flash != "":
		frame.StatusBar = a.flash
		frame.StatusBarLevel = chrome.LevelInfo
	}

	switch {
	case a.confirm.Active():
		frame.Confirm = a.confirm.Prompt()
	case a.prompt.Active():
		frame.Command = a.prompt.Input()
	case a.filterBar.Active():
		frame.Filter = a.filterBar.Input()
	case a.commandBar.Active():
		frame.Command = a.commandBar.Input()
	}

	v := tea.NewView(a.headerChrome().Render(frame))
	v.AltScreen = true
	// Cell-motion mouse mode gives the tables and log viewport wheel
	// scrolling. It is set per-View in bubbletea v2, not as a program
	// option.
	if !a.noMouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

func (a *App) renderSplash() string {
	lines := make([]string, 0, len(logoLines)+4)
	lines = append(lines, "")
	for _, l := range logoLines {
		lines = append(lines, style.Logo.Render(l))
	}
	lines = append(lines, "", a.loader.View(), "")

	w, h := a.width, a.height
	if w == 0 {
		w = 80
	}
	if h == 0 {
		h = 24
	}
	return lipgloss.NewStyle().
		Width(w).
		Height(h).
		Align(lipgloss.Center, lipgloss.Center).
		Background(style.ColorBg).
		Foreground(style.ColorOrange).
		Render(strings.Join(lines, "\n"))
}

// renderInfoPanel is the top-left key/value block.
func (a *App) renderInfoPanel() []string {
	ctxName, host, version := "—", "—", "—"
	if a.client != nil {
		ctxName, host, version = a.client.ContextName, a.client.Host, a.client.Version
		if a.client.OSArch != "" {
			version += " " + a.client.OSArch
		}
	}
	return a.renderInfoPanelWith(ctxName, host, version)
}

// renderInfoPanelWith lays out the info panel for the given values.
func (a *App) renderInfoPanelWith(ctxName, host, version string) []string {
	label, value := style.InfoLabel, style.InfoValue

	// Every value is clipped at the end to the room the info panel has, so
	// none wraps the panel or pushes the shortcuts over. The beginning is
	// what identifies a value — an endpoint's scheme and path, a context
	// name — so that is what stays; the overflow is simply cut, with no
	// ellipsis taking a cell of it.
	room := a.infoValueRoom()
	const roBadge = " [RO]"
	ctxRoom := room
	if a.readonly {
		ctxRoom -= len(roBadge)
	}

	ctxLine := label.Render("Context:  ") + value.Render(clipEnd(ctxName, ctxRoom))
	if a.readonly {
		ctxLine += " " + lipgloss.NewStyle().Foreground(style.ColorOrange).Bold(true).Render("[RO]")
	}

	return []string{
		ctxLine,
		label.Render("Endpoint: ") + value.Render(clipEnd(host, room)),
		label.Render("Engine:   ") + value.Render(clipEnd(version, room)),
		label.Render("Counts:   ") + value.Render(clipEnd(a.renderCounts(), room)),
		a.chrome.VersionLine("DM Rev:   ", a.version, ""),
	}
}

// renderCounts is a one-line inventory summary: running/total containers
// and the other object counts, each from whichever view has fetched them.
// Views that have not loaded yet contribute nothing rather than a zero —
// "0 images" on a host with 651 of them is worse than silence.
func (a *App) renderCounts() string {
	parts := make([]string, 0, 4)

	if v := typedView[*views.ContainersView](a, style.ViewContainers); v != nil && !v.Loading() {
		run, total := v.RunningTotal()
		parts = append(parts, fmt.Sprintf("%d/%d ctr", run, total))
	}
	if v := typedView[*views.ImagesView](a, style.ViewImages); v != nil && !v.Loading() {
		parts = append(parts, fmt.Sprintf("%d img", v.Total()))
	}
	if v := typedView[*views.VolumesView](a, style.ViewVolumes); v != nil && !v.Loading() {
		parts = append(parts, fmt.Sprintf("%d vol", v.Total()))
	}
	if v := typedView[*views.NetworksView](a, style.ViewNetworks); v != nil && !v.Loading() {
		parts = append(parts, fmt.Sprintf("%d net", v.Total()))
	}
	if len(parts) == 0 {
		return "loading…"
	}
	return strings.Join(parts, "  ")
}

// renderShortcuts is the k9s-style grid: view-switch digits on the left,
// per-view plus always-on actions on the right.
func (a *App) renderShortcuts(info []string) []string {
	viewKeys := []chrome.Shortcut{
		{Key: "<0>", Desc: "Containers"},
		{Key: "<1>", Desc: "Images"},
		{Key: "<2>", Desc: "Volumes"},
		{Key: "<3>", Desc: "Networks"},
		{Key: "<4>", Desc: "Projects"},
		{Key: "<5>", Desc: "Runtimes"},
		{Key: "<6>", Desc: "Events"},
	}

	// In a log the digits pick a time range (k9s), so the digit column
	// says so instead of listing views the keys no longer switch to.
	if a.view == style.ViewLogs {
		viewKeys = viewKeys[:0]
		for _, r := range views.LogRanges {
			viewKeys = append(viewKeys, chrome.Shortcut{Key: "<" + r.Key + ">", Desc: r.Label})
		}
	}

	var actions []chrome.Shortcut //nolint:prealloc // each case assigns a fresh literal
	switch a.view {
	case style.ViewContainers:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Logs"},
			{Key: "<o>", Desc: "Inspect"},
			{Key: "<H>", Desc: "Health"},
			{Key: "<T>", Desc: "Top"},
			{Key: "<D>", Desc: "Diff"},
			{Key: "<S>", Desc: "Stats"},
			{Key: "<s>", Desc: "Shell"},
			{Key: "<u>", Desc: "Start"},
			{Key: "<x>", Desc: "Stop"},
			{Key: "<R>", Desc: "Restart"},
			{Key: "<a>", Desc: "All"},
			{Key: "<shift-f>", Desc: "Port-Forward"},
			{Key: "<ctrl-d>", Desc: "Remove"},
		}
	case style.ViewImages:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Layers"},
			{Key: "<u>", Desc: "Run"},
			{Key: "<o>", Desc: "Inspect"},
			{Key: "<U>", Desc: "Used by"},
			{Key: "<v>", Desc: "Scan"},
			{Key: "<a>", Desc: "All"},
			{Key: "<P>", Desc: "Prune"},
			{Key: "<ctrl-d>", Desc: "Remove"},
		}
	case style.ViewVolumes:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Browse"},
			{Key: "<o>", Desc: "Inspect"},
			{Key: "<U>", Desc: "Used by"},
			{Key: "<z>", Desc: "Sizes"},
			{Key: "<P>", Desc: "Prune"},
			{Key: "<ctrl-d>", Desc: "Remove"},
		}
	case style.ViewNetworks:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Inspect"},
			{Key: "<U>", Desc: "Used by"},
			{Key: "<P>", Desc: "Prune"},
			{Key: "<ctrl-d>", Desc: "Remove"},
		}
	case style.ViewProjects:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Containers"},
			{Key: "<l>", Desc: "Logs"},
			{Key: "<u>", Desc: "Up"},
			{Key: "<e>", Desc: "Edit"},
			{Key: "<x>", Desc: "Stop"},
			{Key: "<R>", Desc: "Restart"},
			{Key: "<p>", Desc: "Pull"},
			{Key: "<ctrl-d>", Desc: "Down"},
		}
	case style.ViewRuntimes:
		actions = []chrome.Shortcut{
			{Key: "<n>", Desc: "New"},
			{Key: "<e>", Desc: "Edit"},
			{Key: "<o>", Desc: "Inspect"},
			{Key: "<enter>", Desc: "Connect"},
			{Key: "<s>", Desc: "Shell"},
			{Key: "<u>", Desc: "Start"},
			{Key: "<x>", Desc: "Stop"},
			{Key: "<R>", Desc: "Restart"},
			{Key: "<K>", Desc: "Kubernetes"},
			{Key: "<ctrl-d>", Desc: "Delete"},
		}
	case style.ViewEvents:
		actions = []chrome.Shortcut{
			{Key: "<f>", Desc: "Follow"},
		}
	case style.ViewPods:
		actions = []chrome.Shortcut{
			{Key: "<o>", Desc: "Inspect"},
			{Key: "<u>", Desc: "Start"},
			{Key: "<x>", Desc: "Stop"},
			{Key: "<R>", Desc: "Restart"},
			{Key: "<ctrl-d>", Desc: "Remove"},
		}
	case style.ViewPortForwards:
		actions = []chrome.Shortcut{
			{Key: "<b>", Desc: "Open"},
			{Key: "<ctrl-d>", Desc: "Stop"},
		}
	case style.ViewDiskUsage:
		actions = []chrome.Shortcut{
			{Key: "<P>", Desc: "Prune"},
		}
	case style.ViewRuntimeForm:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Save"},
			{Key: "<tab>", Desc: "Next field"},
			{Key: "<esc>", Desc: "Cancel"},
		}
	case style.ViewEditForm:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Apply"},
			{Key: "<tab>", Desc: "Next field"},
			{Key: "<esc>", Desc: "Cancel"},
		}
	case style.ViewCopyForm:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Copy"},
			{Key: "<tab>", Desc: "Next field"},
			{Key: "<esc>", Desc: "Cancel"},
		}
	case style.ViewRunForm:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Run"},
			{Key: "<tab>", Desc: "Next field"},
			{Key: "<esc>", Desc: "Cancel"},
		}
	case style.ViewLogs:
		actions = []chrome.Shortcut{
			{Key: "<s>", Desc: "Autoscroll"},
			{Key: "<t>", Desc: "Timestamps"},
			{Key: "<w>", Desc: "Wrap"},
			{Key: "<f>", Desc: "Fullscreen"},
			{Key: "<c>", Desc: "Copy"},
			{Key: "<ctrl-s>", Desc: "Save"},
			{Key: "<shift-c>", Desc: "Clear"},
			{Key: "<m>", Desc: "Mark"},
			{Key: "<o>", Desc: "Inspect"},
			{Key: "<esc>", Desc: "Back"},
		}
	case style.ViewNode:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Logs"},
			{Key: "<o>", Desc: "Inspect"},
			{Key: "<s>", Desc: "Shell"},
			{Key: "<a>", Desc: "Toggle Exited"},
			{Key: "<ctrl-d>", Desc: "Remove Exited"},
			{Key: "<esc>", Desc: "Back"},
		}
	case style.ViewInspect:
		actions = []chrome.Shortcut{
			{Key: "</>", Desc: "Search"},
			{Key: "<n>/<N>", Desc: "Next/Prev match"},
			{Key: "<c>", Desc: "Copy"},
			{Key: "<ctrl-s>", Desc: "Save"},
			{Key: "<f>", Desc: "Fullscreen"},
			{Key: "<a>", Desc: "Auto-refresh"},
			{Key: "<esc>", Desc: "Back"},
		}
	case style.ViewLayers:
		actions = []chrome.Shortcut{
			{Key: "<ctrl-f>", Desc: "PgDn"},
			{Key: "<ctrl-b>", Desc: "PgUp"},
			{Key: "<esc>", Desc: "Back"},
		}
	case style.ViewVolumeBrowse:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Open"},
			{Key: "<esc>", Desc: "Up/Back"},
		}
	case style.ViewContexts:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Switch"},
			{Key: "<esc>", Desc: "Back"},
		}
	case style.ViewDir:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Open"},
			{Key: "<u>", Desc: "Compose up"},
			{Key: "<e>", Desc: "Edit, then up"},
			{Key: "<esc>", Desc: "Up/Back"},
		}
	case style.ViewScan:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Details"},
			{Key: "<c>", Desc: "Copy ID"},
			{Key: "<r>", Desc: "Rescan"},
			{Key: "<esc>", Desc: "Back"},
		}
	case style.ViewLint:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Findings"},
			{Key: "<o>", Desc: "Inspect"},
			{Key: "<esc>", Desc: "Back"},
		}
	case style.ViewDumps:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "View"},
			{Key: "<ctrl-d>", Desc: "Delete"},
			{Key: "<esc>", Desc: "Back"},
		}
	default:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Select"},
			{Key: "<esc>", Desc: "Back"},
		}
	}

	actions = append(actions, a.pluginShortcuts()...)
	actions = append(
		actions,
		chrome.Shortcut{Key: "</>", Desc: "Filter"},
		chrome.Shortcut{Key: "<r>", Desc: "Refresh"},
		chrome.Shortcut{Key: "<?>", Desc: "Help"},
		chrome.Shortcut{Key: "<:q>", Desc: "Quit"},
	)
	sortShortcuts(actions)

	return a.shortcutGrid(info, viewKeys, actions)
}

// shortcutGrid lays out the view-switch keys and the actions k9s-style with
// tuikit's grid: columns of ShortcutRows entries, the digits first, then the
// actions down the columns after them.
//
// The logo shows only when every shortcut column fits beside it. It is
// 55 cells wide; giving it priority would shed shortcuts on any terminal
// short of ~175 columns, and the shortcuts are the part you use. When even
// the logo-less header is too narrow, whole columns are shed rather than
// letting tuikit cut every row short — the view digits first, since the
// command palette and help both cover them, then trailing action columns.
// Everything shed is still listed under <?>.
func (a *App) shortcutGrid(info []string, views, actions []chrome.Shortcut) []string {
	rows := min(a.chrome.ShortcutRows, a.chrome.TopSectionRows())
	out := a.chrome.ShortcutGrid(views, actions)

	a.headerLogo = a.logoShown() && rowsWidth(out) <= a.shortcutBudget(info, true)
	budget := a.shortcutBudget(info, a.headerLogo)

	if len(views) > 0 && len(actions) > 0 && rowsWidth(out) > budget {
		// tuikit keeps an empty views column in place so the actions do
		// not move; passing the actions as the first list drops it. Key
		// colors are per key, so the actions still render as actions.
		views = nil
		out = a.chrome.ShortcutGrid(actions, nil)
	}
	for len(actions) > rows && rowsWidth(out) > budget {
		actions = actions[:(len(actions)-1)/rows*rows]
		if len(views) == 0 {
			out = a.chrome.ShortcutGrid(actions, nil)
		} else {
			out = a.chrome.ShortcutGrid(views, actions)
		}
	}
	return out
}

// rowsWidth is the widest of a set of rendered rows.
func rowsWidth(rows []string) int {
	w := 0
	for _, r := range rows {
		w = max(w, lipgloss.Width(r))
	}
	return w
}

// logoShown mirrors tuikit's rule for when the header carries the logo.
func (a *App) logoShown() bool {
	return len(a.chrome.Logo) > 0 && a.width >= a.chrome.MinLogoWidth
}

// shortcutBudget is the shortcut grid's room in tuikit's k9s header layout:
// the width less the info block (inset, widest info line and gap, capped at
// InfoLabelWidth) and the one-cell right inset, and with the logo less the
// logo and the blank cell tuikit keeps before it.
func (a *App) shortcutBudget(info []string, logo bool) int {
	infoW := 0
	for _, l := range info {
		infoW = max(infoW, lipgloss.Width(l))
	}
	infoW = min(1+infoW+infoGap, a.chrome.InfoLabelWidth)
	budget := a.width - infoW - 1
	if logo {
		logoW := 0
		for _, l := range a.chrome.Logo {
			logoW = max(logoW, lipgloss.Width(l))
		}
		budget -= logoW + 1
	}
	return max(budget, 0)
}

// infoGap is the blank space tuikit leaves between the info panel and the
// first shortcut column.
const infoGap = 2

// headerChrome is the chrome for this frame: the logo comes off when the
// shortcut grid needs its room.
func (a *App) headerChrome() chrome.Chrome {
	c := a.chrome
	if !a.headerLogo {
		c.Logo = nil
	}
	return c
}

// renderContent assembles the bordered content box. When help is showing,
// chrome swaps in its own overlay, so return empty.
func (a *App) renderContent() string {
	if a.showHelp {
		return ""
	}
	_, innerH := a.contentSize()
	body := a.renderActiveView()
	box := a.chrome.BorderedContent(body, a.width, innerH)
	return chrome.InjectBorderTitle(box, a.renderResourceTitle(), a.chrome.Theme)
}

func (a *App) renderActiveView() string {
	if a.activeViewLoading() {
		w, h := a.contentSize()
		return a.loader.Centered(w, h)
	}
	if v := a.activeView(); v != nil {
		return v.View()
	}
	return ""
}

func (a *App) activeViewLoading() bool {
	if a.loading {
		return true
	}
	if v := a.activeView(); v != nil {
		return v.Loading()
	}
	return false
}

// viewName is what the active view shows: its resource ("containers"), or
// for a drill-in its subject — "nginx" is what you need to see above a log
// tail, not "lines".
func (a *App) viewName() string {
	if t, ok := a.activeView().(views.Titler); ok && t.Title() != "" {
		return t.Title()
	}
	return style.ViewResource(a.view) + "s"
}

// renderResourceTitle is the centered pill on the content box's top
// border: "containers(all)[7]", plus a filter tag when one is active.
func (a *App) renderResourceTitle() string {
	count := 0
	if v := a.activeView(); v != nil {
		count = v.Count()
	}

	filter := "all"
	if f := a.activeFilter(); f != "" {
		filter = f
	}

	nameStyle := lipgloss.NewStyle().Foreground(style.ColorCyan).Bold(true)
	filterStyle := lipgloss.NewStyle().Foreground(style.ColorFuchsia).Bold(true)
	countStyle := lipgloss.NewStyle().Foreground(style.ColorPapayaWhip).Bold(true)

	name := a.viewName()
	if v, ok := a.activeView().(views.TitleStatus); ok {
		if s := v.Status(); s != "" {
			name += " " + s
		}
	}

	title := nameStyle.Render(name) +
		nameStyle.Render("(") + filterStyle.Render(filter) + nameStyle.Render(")") +
		nameStyle.Render("[") + countStyle.Render(fmt.Sprintf("%d", count)) + nameStyle.Render("]")

	if f := a.activeFilter(); f != "" {
		tag := lipgloss.NewStyle().
			Background(style.ColorSeaGreen).
			Foreground(style.ColorBg).
			Bold(true).
			Padding(0, 1).
			Render("</" + f + ">")
		title += " " + tag
	}
	return title
}

// breadcrumb turns the drill stack into footer crumbs, leaf last.
func (a *App) breadcrumb() []chrome.Crumb {
	crumbs := make([]chrome.Crumb, 0, len(a.viewStack)+1)
	for _, v := range a.viewStack {
		crumbs = append(crumbs, chrome.Crumb{Label: style.ViewName(v)})
	}
	return append(crumbs, chrome.Crumb{Label: style.ViewName(a.view), Leaf: true})
}

// helpPanel is the static help overlay.
func (a *App) helpPanel() chrome.HelpPanel {
	panel := chrome.HelpPanel{
		Sections: []chrome.HelpSection{
			{
				Title: "RESOURCE",
				Entries: []chrome.HelpEntry{
					{Key: "<0>", Desc: "Containers"},
					{Key: "<1>", Desc: "Images"},
					{Key: "<2>", Desc: "Volumes"},
					{Key: "<3>", Desc: "Networks"},
					{Key: "<4>", Desc: "Projects"},
					{Key: "<5>", Desc: "Runtimes"},
					{Key: "<6>", Desc: "Events"},
					{Key: "<enter>", Desc: "Drill in"},
					{Key: "<o>", Desc: "Inspect"},
					{Key: "<d>", Desc: "Describe (inspect)"},
					{Key: "<y>", Desc: "YAML (inspect)"},
					{Key: "<U>", Desc: "Used by"},
					{Key: "<J>", Desc: "Jump to project"},
					{Key: "<a>", Desc: "Toggle stopped/all"},
					{Key: "<ctrl-k>", Desc: "K8s containers"},
					{Key: "<ctrl-z>", Desc: "Faults"},
					{Key: "<ctrl-w>", Desc: "Wide columns"},
					{Key: "<z>", Desc: "Volume sizes"},
					{Key: "<u>", Desc: "Run image"},
					{Key: "<K>", Desc: "Kubernetes on/off"},
					{Key: "<:ctx>", Desc: "Docker contexts"},
					{Key: "<:sd>", Desc: "Saved dumps"},
					{Key: "<:lint>", Desc: "Container lint"},
					{Key: "<:dir>", Desc: "Compose files"},
					{Key: "<v>", Desc: "Scan image"},
				},
			},
			{
				Title: "CONTAINER",
				Entries: []chrome.HelpEntry{
					{Key: "<u>", Desc: "Start"},
					{Key: "<x>", Desc: "Stop"},
					{Key: "<R>", Desc: "Restart"},
					{Key: "<K>", Desc: "Kill (SIGKILL)"},
					{Key: "<n>", Desc: "Node containers ⎈"},
					{Key: "<H>", Desc: "Health"},
					{Key: "<T>", Desc: "Top (processes)"},
					{Key: "<D>", Desc: "Diff (files)"},
					{Key: "<S>", Desc: "Stats"},
					{Key: "<C>", Desc: "Copy files"},
					{Key: "<e>", Desc: "Edit limits, name"},
					{Key: "<b>", Desc: "Browse port"},
					{Key: "<p>", Desc: "Pause/unpause"},
					{Key: "<s>", Desc: "Shell into it"},
					{Key: "<A>", Desc: "Attach"},
					{Key: "<l>", Desc: "Logs"},
					{Key: "<t>", Desc: "CPU/MEM poll"},
					{Key: "<ctrl-d>", Desc: "Remove"},
					{Key: "<P>", Desc: "Prune"},
				},
			},
			generalHelp(),
			chrome.NavigationHelp(),
		},
	}
	// In a log the CONTAINER column gives way to the log's own keys, as
	// k9s's help follows the view it is opened over.
	if a.view == style.ViewLogs {
		panel.Sections[1] = logsHelp()
	}
	if pl, ok := a.pluginsHelp(); ok {
		panel.Sections = append(panel.Sections, pl)
	}
	if hk, ok := a.hotKeysHelp(); ok {
		panel.Sections = append(panel.Sections, hk)
	}
	for i := range panel.Sections {
		sortHelpEntries(panel.Sections[i].Entries)
	}
	return panel
}

// logsHelp is the LOGS column: every key a log view binds.
func logsHelp() chrome.HelpSection {
	s := chrome.HelpSection{Title: "LOGS"}
	for _, r := range views.LogRanges {
		desc := "Last " + r.Label
		if r.Since == 0 {
			desc = "Usual backlog"
		}
		s.Entries = append(s.Entries, chrome.HelpEntry{Key: "<" + r.Key + ">", Desc: desc})
	}
	s.Entries = append(
		s.Entries,
		chrome.HelpEntry{Key: "<s>", Desc: "Autoscroll"},
		chrome.HelpEntry{Key: "<t>", Desc: "Timestamps"},
		chrome.HelpEntry{Key: "<w>", Desc: "Wrap"},
		chrome.HelpEntry{Key: "<f>", Desc: "Fullscreen"},
		chrome.HelpEntry{Key: "<c>", Desc: "Copy"},
		chrome.HelpEntry{Key: "<ctrl-s>", Desc: "Save to file"},
		chrome.HelpEntry{Key: "<shift-c>", Desc: "Clear"},
		chrome.HelpEntry{Key: "<m>", Desc: "Mark"},
		chrome.HelpEntry{Key: "<o>", Desc: "Inspect"},
	)
	return s
}

// generalHelp is tuikit's shared GENERAL column — every key in it is bound
// in keys.go — plus dockmaster's own: r as well as ctrl-r, the log toggles,
// :logo, and ctrl-c.
func generalHelp() chrome.HelpSection {
	g := chrome.GeneralHelp()
	g.Entries = append(
		g.Entries,
		chrome.HelpEntry{Key: "<r>", Desc: "Reload"},
		chrome.HelpEntry{Key: "<ctrl-s>", Desc: "Save"},
		chrome.HelpEntry{Key: "<c>", Desc: "Copy name"},
		chrome.HelpEntry{Key: "<i>", Desc: "Copy ID"},
		chrome.HelpEntry{Key: "<space>", Desc: "Mark"},
		chrome.HelpEntry{Key: "<ctrl-space>", Desc: "Mark range"},
		chrome.HelpEntry{Key: "<ctrl-\\>", Desc: "Clear marks"},
		chrome.HelpEntry{Key: "<shift-←/→>", Desc: "Sort column"},
		chrome.HelpEntry{Key: "<shift-↑/↓>", Desc: "Sort direction"},
		chrome.HelpEntry{Key: "<:logo>", Desc: "Toggle logo"},
		chrome.HelpEntry{Key: "<ctrl-c>", Desc: "Quit"},
	)
	return g
}

// isDigitKey reports whether key is a "<N>" view-switch hotkey.
func isDigitKey(key string) bool {
	if len(key) < 3 || key[0] != '<' || key[len(key)-1] != '>' {
		return false
	}
	for i := 1; i < len(key)-1; i++ {
		if key[i] < '0' || key[i] > '9' {
			return false
		}
	}
	return true
}

func digitKeyValue(key string) int {
	n := 0
	for i := 1; i < len(key)-1; i++ {
		n = n*10 + int(key[i]-'0')
	}
	return n
}

// sortByDigitThenDesc is k9s's ordering: digit-keyed entries first in
// numeric order, then the rest alphabetically by description.
func sortByDigitThenDesc(ki, di, kj, dj string) bool {
	ai, aj := isDigitKey(ki), isDigitKey(kj)
	if ai != aj {
		return ai
	}
	if ai {
		return digitKeyValue(ki) < digitKeyValue(kj)
	}
	return strings.ToLower(di) < strings.ToLower(dj)
}

func sortShortcuts(s []chrome.Shortcut) {
	sort.SliceStable(s, func(i, j int) bool {
		return sortByDigitThenDesc(s[i].Key, s[i].Desc, s[j].Key, s[j].Desc)
	})
}

func sortHelpEntries(s []chrome.HelpEntry) {
	sort.SliceStable(s, func(i, j int) bool {
		return sortByDigitThenDesc(s[i].Key, s[i].Desc, s[j].Key, s[j].Desc)
	})
}

// infoLabelCells is the width of every info-panel label ("Endpoint: ").
const infoLabelCells = 10

// infoValueRoom is how many cells an info-panel value may take: the panel's
// cap, less tuikit's one-cell inset, the gap before the shortcuts, and the
// label.
func (a *App) infoValueRoom() int {
	return max(a.chrome.InfoLabelWidth-1-infoGap-infoLabelCells, 8)
}

// clipEnd cuts s to n cells, keeping the beginning, with no ellipsis.
func clipEnd(s string, n int) string { return xansi.Truncate(s, n, "") }
