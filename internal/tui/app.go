// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package tui is dockmaster's bubbletea layer: one root App model, a map of
// views keyed by [style.ViewType], and the tuikit chrome around them.
//
// Views render and report intent; the App
// owns every mutation, which is what makes --readonly a single check
// rather than a flag threaded through nine views.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"
	"github.com/blairham/tuikit/loading"
	"github.com/blairham/tuikit/theme"
	"github.com/blairham/tuikit/viewfsm"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

const (
	// pollInterval is how often the active view auto-refreshes unless
	// config.yaml sets refreshRate. Three seconds rather than k9s's two:
	// `docker ps` against a VM-backed daemon (Colima, Rancher Desktop,
	// Docker Desktop) is measured in whole seconds, and views
	// single-flight their refreshes anyway.
	pollInterval = 3 * time.Second

	// splashDuration is how long the startup logo holds before the first
	// view takes over.
	splashDuration = 1500 * time.Millisecond

	// stopTimeout is the grace period handed to the daemon on stop and
	// restart, matching docker's own default.
	stopTimeout = 10
)

// tickMsg drives the periodic refresh.
type tickMsg time.Time

// splashDoneMsg fires when the splash duration elapses.
type splashDoneMsg struct{}

// logoLines is dockmaster's splash logo, in the figlet "Graffiti" font — the font
// k9s draws its own logo in, so the two read as the same family side by
// side. It is generated, not drawn: `figlet -f graffiti dockmaster` (or
// pyfiglet) reproduces it. Every line is padded to the same width so the
// block aligns. The header and the splash use the same art.
var logoLines = []string{
	`    .___             __                            __                `,
	`  __| _/____   ____ |  | __ _____ _____    _______/  |_  ___________ `,
	` / __ |/  _ \_/ ___\|  |/ //     \\__  \  /  ___/\   __\/ __ \_  __ \`,
	`/ /_/ (  <_> )  \___|    <|  Y Y  \/ __ \_\___ \  |  | \  ___/|  | \/`,
	`\____ |\____/ \___  >__|_ \__|_|  (____  /____  > |__|  \___  >__|   `,
	`     \/           \/     \/     \/     \/     \/            \/       `,
}

// dmLogo is the logo the header draws: DM, dockmaster's short name, in
// the same Graffiti font. The full name is 69 columns and the header only
// had room for it from 186; DM fits beside every column at k9s's widths.
// The splash keeps the full name (logoLines). `figlet -f graffiti DM`
// reproduces it; it is written as quoted strings because the art contains
// a backtick, which a raw string cannot.
var dmLogo = []string{
	"________      _____   ",
	"\\______ \\    /     \\  ",
	" |    |  \\  /  \\ /  \\ ",
	" |    `   \\/    Y    \\",
	"/_______  /\\____|__  /",
	"        \\/         \\/ ",
}

// loadingTips rotate beside the spinner. Index 0 doubles as the splash
// caption, so it has to be the one that is true at startup.
var loadingTips = []string{
	"Connecting to the Docker daemon...",
	"Reading the container list...",
	"Tip: press ? for the keymap",
	"Tip: <a> toggles stopped containers into the list",
	"Tip: <t> turns the CPU/MEM poll off — it costs one request per container",
	"A compose project is just a label: com.docker.compose.project",
	"Tip: </> filters; a leading ! negates the match",
	"Tip: <s> drops you into a shell inside the selected container",
	"Tip: <ctrl-d> removes; every destructive key asks first",
	"Tip: <:ctx> switches docker contexts without leaving dockmaster",
	"Dangling images are the ones no tag points at any more",
	"Tip: <f> pauses log follow so you can scroll back",
}

// pendingAction is a destructive action parked awaiting confirmation.
type pendingAction struct {
	action string
	param  string
}

// App is the root bubbletea model.
type App struct {
	client  *docker.Client
	viewMap map[style.ViewType]views.View
	// refresh is the auto-refresh interval (config refreshRate).
	refresh time.Duration
	// logTail and logShowTime configure new log views (config logger).
	logTail     int
	logShowTime bool
	logBuffer   int
	logSince    time.Duration
	liveRefresh bool
	aliases     map[string]string
	hotKeys     []HotKey
	plugins     []Plugin
	// requestTimeout is --request-timeout, carried onto each client a
	// context switch dials.
	requestTimeout time.Duration
	// thresholds survive a context switch, which rebuilds the views.
	thresholds    views.Thresholds
	dumpDir       string
	noExitOnCtrlC bool
	logWrap       bool
	logPaused     bool
	logFullscreen bool
	shell         string
	noMouse       bool
	reload        func() (Reloaded, error)
	watchDir      string
	watchFP       string
	pendingFP     string
	// applied is the config last read, so a reload changes a runtime
	// toggle only when its value in the file changed.
	applied         Options
	historyFile     string
	commandBar      *chrome.CommandBar
	filterBar       *chrome.FilterBar
	prompt          *chrome.Prompt
	confirm         *chrome.Confirm
	promptDispatch  chrome.Dispatch
	confirmDispatch chrome.ConfirmDispatch
	filter          string
	errFlash        string
	flash           string
	version         string
	viewStack       []style.ViewType
	// history is the top-level views visited, for [ ] and -.
	history viewfsm.History
	// fullscreen hides header and crumbs for a log (F); preFullscreen
	// is what they were, to put back.
	fullscreen    bool
	preFullscreen [2]bool
	loader        loading.Model
	chrome        chrome.Chrome
	view          style.ViewType
	width         int
	height        int
	loading       bool
	// forwards are the port-forward helpers this session started; quitting
	// stops them.
	forwards []string
	// urlOpener opens a URL in the browser; tests replace it.
	urlOpener func(string) error
	// stopForward replaces the daemon call that stops a forward; tests only.
	stopForward func(context.Context, string) error
	// headerLogo is whether this frame's header carries the logo; see
	// shortcutGrid.
	headerLogo bool
	// composeRunner replaces the docker CLI for compose verbs; tests only.
	composeRunner docker.ComposeRunner
	// execProcess replaces tea.ExecProcess, which hands a child the
	// terminal; tests only.
	execProcess func(*exec.Cmd, tea.ExecCallback) tea.Cmd
	// sizedView, sizedW and sizedH record what the active view was last
	// sized to; see syncViewSize.
	sizedView    style.ViewType
	sizedW       int
	sizedH       int
	splashActive bool
	showHelp     bool
	readonly     bool
	logoless     bool
	statsOn      bool
	showAll      bool
}

// Options configures a new App.
type Options struct {
	// Engines are the container runtimes detected on this machine (colima,
	// podman, Docker Desktop, ...); empty when there are none.
	Engines []engines.Provider
	// Notice is shown as an error flash on the first frame. main sets it
	// when the daemon did not answer and dockmaster started anyway.
	Notice   string
	Version  string
	ReadOnly bool
	ShowAll  bool
	NoStats  bool
	Logoless bool
	// Command is the view to open on, by palette name (-c). Empty means
	// containers. StartOnRuntimes wins over it.
	Command string
	// StartOnRuntimes opens on the runtimes view instead of containers: the
	// daemon is a stopped runtime's, and that view is where it starts.
	StartOnRuntimes bool
	// Splashless skips the startup splash.
	Splashless bool
	// Headless hides the whole header: info panel, shortcuts and logo.
	Headless bool
	// Crumbsless hides the breadcrumb footer.
	Crumbsless bool
	// RefreshRate is the auto-refresh interval; zero means pollInterval.
	RefreshRate time.Duration
	// LogTail is the backlog a log view opens with; zero means the view's
	// default. LogShowTime starts log views with timestamps on.
	LogTail     int
	LogShowTime bool
	// LogBuffer caps the lines a log view keeps (0: no cap); LogSince is
	// how far back one opens (0: the last LogTail lines).
	LogBuffer int
	LogSince  time.Duration
	// LiveRefresh refreshes inspect views on the tick.
	LiveRefresh bool
	// NoExitOnCtrlC makes ctrl-c a no-op; :q still quits.
	NoExitOnCtrlC bool
	// DumpDir replaces the state directory as where saves go and :sd looks.
	DumpDir string
	// LogWrap, LogPaused and LogFullscreen are how log views open:
	// wrapped, not following, fullscreen.
	LogWrap, LogPaused, LogFullscreen bool
	// NoMouse leaves mouse reporting off.
	NoMouse bool
	// Shell is the shell s opens in a container when it has it, before the
	// usual bash-then-sh.
	Shell string
	// Reload re-reads the config directory, for ui.reactive; nil turns
	// live reload off. WatchDir is the directory it watches.
	Reload   func() (Reloaded, error)
	WatchDir string
	// HistoryFile is where the command and filter bars' history is kept
	// between runs; "" keeps it for this run only.
	HistoryFile string
	// Aliases are the user's `:` command names (aliases.yaml), validated by
	// ValidateAliases.
	Aliases map[string]string
	// HotKeys are the user's hotkeys (hotkeys.yaml), validated by HotKeys.
	HotKeys []HotKey
	// Plugins are the user's plugins (plugins.yaml), validated by Plugins.
	Plugins []Plugin
	// ColumnLayouts are the user's per-view columns (views.yaml), by view,
	// validated by ColumnLayouts. nil keeps every view's own.
	ColumnLayouts map[string]views.ColumnLayout
	// RequestTimeout overrides every daemon request's own deadline when
	// non-zero (--request-timeout).
	RequestTimeout time.Duration
	// Thresholds colour the containers view's CPU% and MEM; zero is k9s's
	// 70/90.
	Thresholds views.Thresholds
}

// ThresholdsFrom converts config.yaml's thresholds block for the views.
func ThresholdsFrom(t config.Thresholds) views.Thresholds {
	return views.Thresholds{
		CPUWarn: float64(t.CPU.Warn), CPUCritical: float64(t.CPU.Critical),
		MemWarn: float64(t.Memory.Warn), MemCritical: float64(t.Memory.Critical),
	}
}

// containersView builds the root view with the configured thresholds.
func containersView(client *docker.Client, opts Options) *views.ContainersView {
	v := views.NewContainersView(client, opts.ShowAll, !opts.NoStats)
	v.SetThresholds(opts.Thresholds)
	return v
}

// NewApp builds the root model.
func NewApp(client *docker.Client, opts Options) *App {
	statsOn := !opts.NoStats
	refresh := opts.RefreshRate
	if refresh <= 0 {
		refresh = pollInterval
	}
	vm := map[style.ViewType]views.View{
		style.ViewContainers:   containersView(client, opts),
		style.ViewImages:       views.NewImagesView(client, false),
		style.ViewVolumes:      views.NewVolumesView(client),
		style.ViewNetworks:     views.NewNetworksView(client),
		style.ViewProjects:     views.NewProjectsView(client),
		style.ViewRuntimes:     views.NewRuntimesView(opts.Engines, hostOf(client)),
		style.ViewDiskUsage:    views.NewDiskUsageView(client),
		style.ViewPortForwards: views.NewPortForwardsView(client),
		style.ViewPods:         views.NewPodsView(podmanOf(opts.Engines)),
		style.ViewEvents:       views.NewEventsView(client),
		style.ViewPulses:       views.NewPulsesView(client, statsOn, refresh),
	}
	startView := style.ViewContainers
	if vt, ok := ViewForCommand(opts.Command); ok {
		startView = vt
	}
	if opts.StartOnRuntimes {
		startView = style.ViewRuntimes
	}

	if client != nil && opts.RequestTimeout > 0 {
		client.RequestTimeout = opts.RequestTimeout
	}
	a := &App{
		client:         client,
		viewMap:        vm,
		dumpDir:        opts.DumpDir,
		noExitOnCtrlC:  opts.NoExitOnCtrlC,
		logWrap:        opts.LogWrap,
		logPaused:      opts.LogPaused,
		logFullscreen:  opts.LogFullscreen,
		shell:          opts.Shell,
		noMouse:        opts.NoMouse,
		reload:         opts.Reload,
		watchDir:       opts.WatchDir,
		watchFP:        fingerprint(opts.WatchDir),
		applied:        opts,
		historyFile:    opts.HistoryFile,
		urlOpener:      openInBrowser,
		version:        opts.Version,
		errFlash:       opts.Notice,
		view:           startView,
		loading:        true,
		splashActive:   !opts.Splashless,
		readonly:       opts.ReadOnly,
		logoless:       opts.Logoless,
		statsOn:        statsOn,
		showAll:        opts.ShowAll,
		refresh:        refresh,
		logTail:        opts.LogTail,
		logShowTime:    opts.LogShowTime,
		logBuffer:      opts.LogBuffer,
		logSince:       opts.LogSince,
		liveRefresh:    opts.LiveRefresh,
		aliases:        opts.Aliases,
		hotKeys:        opts.HotKeys,
		plugins:        opts.Plugins,
		requestTimeout: opts.RequestTimeout,
		thresholds:     opts.Thresholds,
	}
	views.SetColumnLayouts(opts.ColumnLayouts)
	a.buildChrome(style.Base(), opts.Headless, opts.Crumbsless)
	a.history.Visit(viewfsm.ViewID(startView))
	a.loadHistory(opts.HistoryFile)
	return a
}

// buildChrome builds the frame and the bars on theme t. NewApp builds them
// once; a reskin (ui.reactive) builds them again, keeping the bars' history
// and the header and crumbs as they are. The command bar's suggestions read
// the app's aliases, so they follow a reload too.
func (a *App) buildChrome(t theme.Theme, headless, crumbsless bool) {
	var cmdHistory, filterHistory []string
	if a.commandBar != nil {
		cmdHistory, filterHistory = a.commandBar.History(), a.filterBar.History()
	}
	a.commandBar = chrome.NewCommandBar(t, chrome.CommandBarOpts{
		Prompt:    "🐳:",
		CharLimit: 128,
		SuggestFn: func(value string) []string {
			if best := fuzzyMatch(value, aliasNames(a.aliases)...); best != "" {
				return []string{best}
			}
			return nil
		},
	})
	a.filterBar = chrome.NewFilterBar(t, chrome.FilterBarOpts{Prompt: "🔍/", CharLimit: 128})
	a.commandBar.SetHistory(cmdHistory)
	a.filterBar.SetHistory(filterHistory)

	cfg := chrome.Config{
		Theme:         t,
		InfoPanelRows: 5, // Context, Host, Version, Containers, Dockmaster Rev
		// Six view digits, one per row; tuikit would otherwise budget five
		// when logoless and the sixth would overflow the header.
		ShortcutRows: 6,
		MinLogoWidth: 120,
	}
	if !a.logoless {
		cfg.Logo = dmLogo
	}
	a.chrome = chrome.New(cfg)
	a.chrome.HeaderHidden, a.chrome.CrumbsHidden = headless, crumbsless
	a.prompt = chrome.NewPrompt(t, chrome.PromptOpts{CharLimit: 256})
	a.confirm = chrome.NewConfirm(t)
	a.loader = loading.New(t, loadingTips)
}

// activeView returns the current view, or nil when unregistered.
func (a *App) activeView() views.View { return a.viewMap[a.view] }

// setView registers a view under a view type.
func (a *App) setView(vt style.ViewType, v views.View) { a.viewMap[vt] = v }

// typedView returns a view cast to its concrete type, or the zero value.
func typedView[T views.View](a *App, vt style.ViewType) T {
	if v, ok := a.viewMap[vt].(T); ok {
		return v
	}
	var zero T
	return zero
}

// Init starts the first fetch, the splash timer, and the spinner.
func (a *App) Init() tea.Cmd {
	if !a.splashActive {
		// Splashless: straight to the first frame, so the poll starts now
		// rather than when a splash that never showed would have ended.
		return tea.Batch(a.activeView().Init(), a.loader.Tick(), a.tick())
	}
	return tea.Batch(
		a.activeView().Init(),
		a.loader.Tick(),
		a.splashTimer(),
	)
}

func (a *App) splashTimer() tea.Cmd {
	return tea.Tick(splashDuration, func(_ time.Time) tea.Msg { return splashDoneMsg{} })
}

func (a *App) tick() tea.Cmd {
	return tea.Tick(a.refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Update is the flat message dispatch.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) { //nolint:gocyclo,gocognit // flat message dispatch
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.resizeActiveView()
		return a, nil

	case splashDoneMsg:
		a.splashActive = false
		a.loading = false
		a.resizeActiveView()
		return a, a.tick()

	case tickMsg:
		// Only the volatile views poll (see `polled`), and those
		// single-flight their own refresh — so a tick landing while a slow
		// `docker ps` is outstanding is a no-op, not a second request.
		return a, tea.Batch(a.refreshPolledView(), a.checkReload(), a.tick())

	case reloadedMsg:
		a.applyReload(msg)
		return a, nil

	case loading.TickMsg:
		return a, a.loader.Update(msg)

	case tea.KeyMsg:
		return a.handleKey(msg)

	case tea.PasteMsg:
		switch {
		case a.commandBar.Active():
			_, cmd := a.commandBar.Update(msg, a.dispatchCommand)
			return a, cmd
		case a.filterBar.Active():
			_, cmd := a.filterBar.Update(msg, a.onFilterChange)
			return a, cmd
		case a.prompt.Active():
			_, cmd := a.prompt.Update(msg, a.promptDispatch)
			return a, cmd
		}
		if c, ok := a.activeView().(views.InputCapturer); ok && c.CapturesInput() {
			return a, a.updateActiveTable(msg)
		}
		return a, nil

	case actionDoneMsg:
		return a.handleActionDone(msg)

	case runDoneMsg:
		return a.handleRunDone(msg)

	case copyDoneMsg:
		return a.handleCopyDone(msg)

	case editStateMsg:
		return a.handleEditState(msg)

	case editDoneMsg:
		return a.handleEditDone(msg)

	case execDoneMsg:
		if msg.err != nil {
			a.errFlash = msg.err.Error()
		}
		// The terminal was handed to the child process; reclaim the
		// screen and resize the view to whatever size came back.
		return a, tea.Batch(a.refreshActiveView(), tea.ClearScreen)

	case switchContextMsg:
		return a.applySwitchContext(msg)

	case runtimeDoneMsg:
		return a.handleRuntimeDone(msg)

	case composeDoneMsg:
		return a.handleComposeDone(msg)

	case pluginDoneMsg:
		return a.handlePluginDone(msg)

	case composeEditedMsg:
		return a.handleComposeEdited(msg)

	case forwardStartedMsg:
		return a.handleForwardStarted(msg)

	// The runtimes listing goes to the runtimes view whichever view is active.
	// Delivered only to the active view, a listing that lands after the
	// user has moved away is dropped, the view's single-flight guard never
	// clears, and it never refreshes again — including the poll that shows
	// a start finishing.
	case views.PodsRefreshMsg:
		if a.view == style.ViewPods && !a.splashActive {
			a.loading = false
		}
		if pv := typedView[*views.PodsView](a, style.ViewPods); pv != nil {
			return a, pv.Update(msg)
		}
		return a, nil

	case views.RuntimesRefreshMsg:
		if a.view == style.ViewRuntimes && !a.splashActive {
			a.loading = false
		}
		if cv := a.runtimesView(); cv != nil {
			return a, cv.Update(msg)
		}
		return a, nil

	// The dashboard's messages go to it whichever view is showing: a
	// listing that landed on another view would leave its single-flight
	// guard set for good, and an event count dropped would end its drain.
	case views.PulsesListMsg, views.PulsesStatsMsg, views.PulsesDiskMsg, views.PulsesEventsMsg:
		if !a.splashActive && a.refreshMsgMatchesView(msg) {
			a.loading = false
		}
		if pv := typedView[*views.PulsesView](a, style.ViewPulses); pv != nil {
			return a, pv.Update(msg)
		}
		return a, nil

	// Data refresh messages: clear the spinner when they belong to the
	// active view, then hand them down.
	case views.ContainersRefreshMsg, views.ContainerStatsMsg,
		views.ImagesRefreshMsg, views.VolumesRefreshMsg,
		views.NetworksRefreshMsg, views.ProjectsRefreshMsg,
		views.InspectRefreshMsg, views.LayersRefreshMsg,
		views.ContextsRefreshMsg, views.DiskUsageRefreshMsg, views.PortForwardsRefreshMsg, views.NodeRefreshMsg, views.TopRefreshMsg,
		views.LogBatchMsg, views.LogClosedMsg, views.EventsBatchMsg, views.EventsClosedMsg:
		if !a.splashActive && a.refreshMsgMatchesView(msg) {
			a.loading = false
		}
		return a, a.updateActiveView(msg)

	default:
		return a, a.updateActiveView(msg)
	}
}

// shutdown is the single teardown path (ctrl-c, :q).
func (a *App) shutdown() {
	_ = a.saveHistory(a.historyFile) //nolint:errcheck // a convenience; the process is exiting
	a.stopStoppableViews()
	a.stopSessionForwards()
	if a.client != nil {
		_ = a.client.Close() //nolint:errcheck // process is exiting
	}
}

// switchContextMsg carries the result of a context switch.
type switchContextMsg struct {
	err    error
	client *docker.Client
	name   string
	// keepView leaves the user where they are rather than landing them on
	// containers: a reconnect after starting a colima profile happens while
	// they are looking at the profiles, possibly to start another.
	keepView bool
	// land, from `:<view> @context`, is the view (and filter) the switch
	// opens instead of containers.
	land *landing
}

// landing is where a context switch puts the user.
type landing struct {
	filter string
	view   style.ViewType
}

// applySwitchContext swaps in a client for a different docker context and
// rebuilds every view against it. The old client is closed; the views are
// rebuilt rather than mutated because each holds its own cached rows, and
// showing one daemon's containers under another daemon's name for a tick
// is exactly the kind of thing that gets the wrong container killed.
func (a *App) applySwitchContext(msg switchContextMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.errFlash = docker.FormatUserError(msg.err).Error()
		return a, nil
	}

	a.stopStoppableViews()
	if a.client != nil {
		_ = a.client.Close() //nolint:errcheck // replaced client; close error is not actionable
	}
	if msg.client != nil {
		msg.client.RequestTimeout = a.requestTimeout
	}
	a.client = msg.client

	// The runtimes view survives the rebuild: it lists VMs, not daemon
	// objects, and dropping it would lose the busy state of a machine that
	// is still mid-start.
	cv := a.runtimesView()
	if cv == nil {
		cv = views.NewRuntimesView(nil, "")
	}
	cv.SetConnectedHost(a.client.Host)

	a.viewMap = map[style.ViewType]views.View{
		style.ViewContainers: containersView(
			a.client,
			Options{ShowAll: a.showAll, NoStats: !a.statsOn, Thresholds: a.thresholds},
		),
		style.ViewImages:       views.NewImagesView(a.client, false),
		style.ViewVolumes:      views.NewVolumesView(a.client),
		style.ViewNetworks:     views.NewNetworksView(a.client),
		style.ViewProjects:     views.NewProjectsView(a.client),
		style.ViewRuntimes:     cv,
		style.ViewDiskUsage:    views.NewDiskUsageView(a.client),
		style.ViewPortForwards: views.NewPortForwardsView(a.client),
		style.ViewPods:         views.NewPodsView(podmanOf(cv.Providers())),
		style.ViewEvents:       views.NewEventsView(a.client),
		style.ViewPulses:       views.NewPulsesView(a.client, a.statsOn, a.refresh),
	}
	a.flash = fmt.Sprintf("switched to context %s", msg.name)
	if msg.keepView && a.view == style.ViewRuntimes {
		a.viewStack = nil
		a.resizeActiveView()
		return a, cv.Refresh()
	}
	if msg.land != nil && msg.land.view != style.ViewContainers {
		cmd := a.switchView(msg.land.view)
		a.filter = msg.land.filter
		a.setActiveFilter(a.filter)
		return a, cmd
	}
	a.viewStack = nil
	a.view = style.ViewContainers
	a.filter = ""
	a.closeAllBars()
	if msg.land != nil {
		a.filter = msg.land.filter
		a.setActiveFilter(a.filter)
	}
	a.resizeActiveView()
	return a, a.viewMap[style.ViewContainers].Init()
}

// doSwitchContext dials a context's endpoint on a background goroutine.
func doSwitchContext(name, host string) tea.Cmd { return doSwitchContextKeep(name, host, false) }

// doSwitchContextKeep is doSwitchContext with control over whether the
// switch lands the user on the containers view.
func doSwitchContextKeep(name, host string, keepView bool) tea.Cmd {
	return dialContext(switchContextMsg{name: name, keepView: keepView}, host)
}

// doSwitchContextTo is doSwitchContext landing on land rather than on
// containers: `:<view> @context`.
func doSwitchContextTo(name, host string, land *landing) tea.Cmd {
	return dialContext(switchContextMsg{name: name, land: land}, host)
}

// dialContext dials msg.name's endpoint and returns msg with the client,
// or the error, filled in.
func dialContext(msg switchContextMsg, host string) tea.Cmd {
	return func() tea.Msg {
		c, err := docker.NewForContext(msg.name, host)
		if err != nil {
			msg.err = err
			return msg
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.Negotiate(ctx); err != nil {
			_ = c.Close() //nolint:errcheck // dial failed; nothing to salvage
			msg.err = err
			return msg
		}
		c.ContextName = msg.name
		msg.client = c
		return msg
	}
}

// hostOf is the endpoint a client dials, or "" for none.
func hostOf(c *docker.Client) string {
	if c == nil {
		return ""
	}
	return c.Host
}
