package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/chrome"
	tktheme "github.com/blairham/tuikit/theme"

	"github.com/blairham/dockyard/internal/tui/style"
	"github.com/blairham/dockyard/internal/tui/views"
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

	frame := chrome.Frame{
		Width:       a.width,
		Height:      a.height,
		InfoLines:   a.renderInfoPanel(),
		Shortcuts:   a.renderShortcuts(),
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

	v := tea.NewView(a.chrome.Render(frame))
	v.AltScreen = true
	// Cell-motion mouse mode gives the tables and log viewport wheel
	// scrolling. It is set per-View in bubbletea v2, not as a program
	// option.
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (a *App) renderSplash() string {
	lines := make([]string, 0, len(logoBigLines)+4)
	lines = append(lines, "")
	for _, l := range logoBigLines {
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
		Foreground(style.ColorDockerBlue).
		Render(strings.Join(lines, "\n"))
}

// renderInfoPanel is the top-left key/value block.
func (a *App) renderInfoPanel() []string {
	label, value := style.InfoLabel, style.InfoValue

	ctxName, host, version := "—", "—", "—"
	if a.client != nil {
		ctxName, host, version = a.client.ContextName, a.client.Host, a.client.Version
		if a.client.OSArch != "" {
			version += " " + a.client.OSArch
		}
	}
	// The endpoint is the single most useful thing on screen when
	// something is wrong, and it is also the longest — show the tail,
	// which is the part that differs between contexts.
	if len(host) > 34 {
		host = "…" + host[len(host)-33:]
	}

	ctxLine := label.Render("Context:  ") + value.Render(ctxName)
	if a.readonly {
		ctxLine += " " + lipgloss.NewStyle().Foreground(style.ColorOrange).Bold(true).Render("[RO]")
	}

	return []string{
		ctxLine,
		label.Render("Endpoint: ") + value.Render(host),
		label.Render("Engine:   ") + value.Render(version),
		label.Render("Counts:   ") + value.Render(a.renderCounts()),
		a.chrome.VersionLine("Dockyard: ", a.version, ""),
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
func (a *App) renderShortcuts() []string {
	viewKeys := []chrome.Shortcut{
		{Key: "<1>", Desc: "Containers"},
		{Key: "<2>", Desc: "Images"},
		{Key: "<3>", Desc: "Volumes"},
		{Key: "<4>", Desc: "Networks"},
		{Key: "<5>", Desc: "Projects"},
	}

	var actions []chrome.Shortcut //nolint:prealloc // each case assigns a fresh literal
	switch a.view {
	case style.ViewContainers:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Logs"},
			{Key: "<o>", Desc: "Inspect"},
			{Key: "<e>", Desc: "Shell"},
			{Key: "<s>", Desc: "Start"},
			{Key: "<x>", Desc: "Stop"},
			{Key: "<R>", Desc: "Restart"},
			{Key: "<a>", Desc: "All"},
			{Key: "<ctrl-d>", Desc: "Remove"},
		}
	case style.ViewImages:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Layers"},
			{Key: "<o>", Desc: "Inspect"},
			{Key: "<a>", Desc: "All"},
			{Key: "<P>", Desc: "Prune"},
			{Key: "<ctrl-d>", Desc: "Remove"},
		}
	case style.ViewVolumes:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Inspect"},
			{Key: "<z>", Desc: "Sizes"},
			{Key: "<P>", Desc: "Prune"},
			{Key: "<ctrl-d>", Desc: "Remove"},
		}
	case style.ViewNetworks:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Inspect"},
			{Key: "<P>", Desc: "Prune"},
			{Key: "<ctrl-d>", Desc: "Remove"},
		}
	case style.ViewProjects:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Containers"},
			{Key: "<s>", Desc: "Start all"},
			{Key: "<x>", Desc: "Stop all"},
			{Key: "<ctrl-d>", Desc: "Remove all"},
		}
	case style.ViewLogs:
		actions = []chrome.Shortcut{
			{Key: "<f>", Desc: "Follow"},
			{Key: "<T>", Desc: "Timestamps"},
			{Key: "<o>", Desc: "Inspect"},
			{Key: "<e>", Desc: "Shell"},
			{Key: "<esc>", Desc: "Back"},
		}
	case style.ViewInspect, style.ViewLayers:
		actions = []chrome.Shortcut{
			{Key: "<ctrl-f>", Desc: "PgDn"},
			{Key: "<ctrl-b>", Desc: "PgUp"},
			{Key: "<esc>", Desc: "Back"},
		}
	case style.ViewContexts:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Switch"},
			{Key: "<esc>", Desc: "Back"},
		}
	default:
		actions = []chrome.Shortcut{
			{Key: "<enter>", Desc: "Select"},
			{Key: "<esc>", Desc: "Back"},
		}
	}

	actions = append(
		actions,
		chrome.Shortcut{Key: "</>", Desc: "Filter"},
		chrome.Shortcut{Key: "<r>", Desc: "Refresh"},
		chrome.Shortcut{Key: "<?>", Desc: "Help"},
		chrome.Shortcut{Key: "<:q>", Desc: "Quit"},
	)
	sortShortcuts(actions)

	return a.shortcutGrid(viewKeys, actions)
}

// Shortcut-grid column widths. tuikit's own ShortcutGrid hardcodes the
// first description column at 10 cells, and "Containers" is exactly 10 —
// so it butts straight against the next key with no separating space, and
// those widths are not configurable. The grid is composed here instead,
// reusing tuikit's styles so the coloring still matches the chrome.
// Worth pushing upstream as a configurable width.
const (
	shortcutKeyWidth  = 9
	shortcutDescWidth = 12
)

// shortcutGrid lays view-switch keys in the left column and actions in the
// right, k9s-style, one pair per row.
func (a *App) shortcutGrid(views, actions []chrome.Shortcut) []string {
	rows := len(views)
	if len(actions) > rows {
		rows = len(actions)
	}
	keyCol := lipgloss.NewStyle().Width(shortcutKeyWidth)
	descCol := lipgloss.NewStyle().Width(shortcutDescWidth)
	t := a.chrome.Theme

	out := make([]string, rows)
	for i := range out {
		var v, act chrome.Shortcut
		if i < len(views) {
			v = views[i]
		}
		if i < len(actions) {
			act = actions[i]
		}
		out[i] = shortcutKeyStyle(t, v.Key).Render(keyCol.Render(v.Key)) +
			t.ShortcutDesc.Render(descCol.Render(v.Desc)) +
			shortcutKeyStyle(t, act.Key).Render(keyCol.Render(act.Key)) +
			t.ShortcutDesc.Render(act.Desc)
	}
	return out
}

// shortcutKeyStyle mirrors tuikit's own rule: view-switch digit keys get
// the view color, every other key the action color.
func shortcutKeyStyle(t tktheme.Theme, key string) lipgloss.Style {
	if isDigitKey(key) {
		return t.ShortcutView
	}
	return t.ShortcutKey
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

// renderResourceTitle is the centered pill on the content box's top
// border: "containers(all)[7]", plus a filter tag when one is active.
func (a *App) renderResourceTitle() string {
	resource := style.ViewResource(a.view)
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

	name := resource + "s"
	// Drill-ins name their subject instead of their resource: "nginx" is
	// what you need to see above a log tail, not "lines".
	if t, ok := a.activeView().(views.Titler); ok && t.Title() != "" {
		name = t.Title()
	}
	if v := typedView[*views.LogsView](a, style.ViewLogs); v != nil && a.view == style.ViewLogs {
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
				Title:      "RESOURCE",
				TitleColor: style.ColorFuchsia,
				Entries: []chrome.HelpEntry{
					{Key: "<1>", Desc: "Containers"},
					{Key: "<2>", Desc: "Images"},
					{Key: "<3>", Desc: "Volumes"},
					{Key: "<4>", Desc: "Networks"},
					{Key: "<5>", Desc: "Projects"},
					{Key: "<enter>", Desc: "Drill in"},
					{Key: "<o>", Desc: "Inspect"},
					{Key: "<a>", Desc: "Toggle stopped/all"},
					{Key: "<z>", Desc: "Volume sizes"},
					{Key: "<:ctx>", Desc: "Docker contexts"},
				},
			},
			{
				Title:      "CONTAINER",
				TitleColor: style.ColorFuchsia,
				Entries: []chrome.HelpEntry{
					{Key: "<s>", Desc: "Start"},
					{Key: "<x>", Desc: "Stop"},
					{Key: "<R>", Desc: "Restart"},
					{Key: "<K>", Desc: "Kill (SIGKILL)"},
					{Key: "<p>", Desc: "Pause/unpause"},
					{Key: "<e>", Desc: "Shell into it"},
					{Key: "<l>", Desc: "Logs"},
					{Key: "<t>", Desc: "Toggle CPU/MEM poll"},
					{Key: "<ctrl-d>", Desc: "Remove"},
					{Key: "<P>", Desc: "Prune"},
				},
			},
			{
				Title: "GENERAL",
				Entries: []chrome.HelpEntry{
					{Key: "<:cmd>", Desc: "Command mode"},
					{Key: "</>", Desc: "Filter (! negates)"},
					{Key: "<esc>", Desc: "Back / clear filter"},
					{Key: "<r>", Desc: "Refresh"},
					{Key: "<f>", Desc: "Toggle log follow"},
					{Key: "<T>", Desc: "Toggle timestamps"},
					{Key: "<?>", Desc: "Help"},
					{Key: "<ctrl-c>", Desc: "Quit"},
				},
			},
			{
				Title: "NAVIGATION",
				Entries: []chrome.HelpEntry{
					{Key: "<j>", Desc: "Down"},
					{Key: "<k>", Desc: "Up"},
					{Key: "<g>", Desc: "Top"},
					{Key: "<G>", Desc: "Bottom"},
					{Key: "<ctrl-f>", Desc: "Page down"},
					{Key: "<ctrl-b>", Desc: "Page up"},
				},
			},
		},
	}
	for i := range panel.Sections {
		sortHelpEntries(panel.Sections[i].Entries)
	}
	return panel
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
