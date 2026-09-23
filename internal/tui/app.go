// Package tui is dockyard's bubbletea layer: one root App model, a map of
// views keyed by [style.ViewType], and the tuikit chrome around them.
//
// Views render and report intent; the App
// owns every mutation, which is what makes --readonly a single check
// rather than a flag threaded through nine views.
package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"
	"github.com/blairham/tuikit/loading"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
	"github.com/blairham/dockyard/internal/tui/views"
)

const (
	// pollInterval is how often the active view auto-refreshes. Three
	// seconds rather than k9s's two: `docker ps` against a VM-backed
	// daemon (Colima, Rancher Desktop, Docker Desktop) is measured in
	// whole seconds, and views single-flight their refreshes anyway.
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

// logoLines is the compact header logo, shown top-right on a wide
// terminal. All lines are padded to equal width so the block aligns.
var logoLines = []string{
	`     __         __   `,
	` ___/ /__  ____/ /__ `,
	`/ _  / _ \/ __/  '_/ `,
	`\_,_/\___/\__/_/\_\  `,
	`  ~~~~~~~~~~~~~~~~   `,
	`                     `,
}

// logoBigLines is the startup splash logo.
var logoBigLines = []string{
	`    __           __                       __ `,
	`ت__/ /__  ____/ /_ ___  ____ _____ ___  / / `,
	`/ _  / _ \/ __/ //_// _ \/ __ ` + "`" + `/ ___/ _ \/ /  `,
	`/ /_/ /  __/ /_/ ,<  /  __/ /_/ / /  / /_/ /   `,
	`\____/\___/\__/_/|_| \___/\__,_/_/   \____/  `,
	`   ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~   `,
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
	"Tip: <e> drops you into a shell inside the selected container",
	"Tip: <ctrl-d> removes; every destructive key asks first",
	"Tip: <:ctx> switches docker contexts without leaving dockyard",
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
	client          *docker.Client
	viewMap         map[style.ViewType]views.View
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
	loader          loading.Model
	chrome          chrome.Chrome
	view            style.ViewType
	width           int
	height          int
	loading         bool
	splashActive    bool
	showHelp        bool
	readonly        bool
	logoless        bool
	statsOn         bool
	showAll         bool
}

// Options configures a new App.
type Options struct {
	Version  string
	ReadOnly bool
	ShowAll  bool
	NoStats  bool
	Logoless bool
}

// NewApp builds the root model.
func NewApp(client *docker.Client, opts Options) *App {
	t := style.Base()

	commandBar := chrome.NewCommandBar(t, chrome.CommandBarOpts{
		Prompt:    "🐳:",
		CharLimit: 128,
		SuggestFn: func(value string) []string {
			if best := fuzzyMatch(value); best != "" {
				return []string{best}
			}
			return nil
		},
	})
	filterBar := chrome.NewFilterBar(t, chrome.FilterBarOpts{Prompt: "🔍/", CharLimit: 128})

	chromeCfg := chrome.Config{
		Theme:         t,
		InfoPanelRows: 5, // Context, Host, Version, Containers, Dockyard Rev
		MinLogoWidth:  120,
	}
	if !opts.Logoless {
		chromeCfg.Logo = logoLines
	}

	statsOn := !opts.NoStats
	vm := map[style.ViewType]views.View{
		style.ViewContainers: views.NewContainersView(client, opts.ShowAll, statsOn),
		style.ViewImages:     views.NewImagesView(client, false),
		style.ViewVolumes:    views.NewVolumesView(client),
		style.ViewNetworks:   views.NewNetworksView(client),
		style.ViewProjects:   views.NewProjectsView(client),
	}

	return &App{
		client:       client,
		viewMap:      vm,
		commandBar:   commandBar,
		filterBar:    filterBar,
		prompt:       chrome.NewPrompt(t, chrome.PromptOpts{CharLimit: 256}),
		confirm:      chrome.NewConfirm(t),
		chrome:       chrome.New(chromeCfg),
		loader:       loading.New(t, loadingTips),
		version:      opts.Version,
		view:         style.ViewContainers,
		loading:      true,
		splashActive: true,
		readonly:     opts.ReadOnly,
		logoless:     opts.Logoless,
		statsOn:      statsOn,
		showAll:      opts.ShowAll,
	}
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
	return tea.Batch(
		a.viewMap[style.ViewContainers].Init(),
		a.loader.Tick(),
		a.splashTimer(),
	)
}

func (a *App) splashTimer() tea.Cmd {
	return tea.Tick(splashDuration, func(_ time.Time) tea.Msg { return splashDoneMsg{} })
}

func (a *App) tick() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
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
		return a, tea.Batch(a.refreshPolledView(), a.tick())

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
		return a, nil

	case actionDoneMsg:
		return a.handleActionDone(msg)

	case execDoneMsg:
		if msg.err != nil {
			a.errFlash = msg.err.Error()
		}
		// The terminal was handed to the child process; reclaim the
		// screen and resize the view to whatever size came back.
		return a, tea.Batch(a.refreshActiveView(), tea.ClearScreen)

	case switchContextMsg:
		return a.applySwitchContext(msg)

	// Data refresh messages: clear the spinner when they belong to the
	// active view, then hand them down.
	case views.ContainersRefreshMsg, views.ContainerStatsMsg,
		views.ImagesRefreshMsg, views.VolumesRefreshMsg,
		views.NetworksRefreshMsg, views.ProjectsRefreshMsg,
		views.InspectRefreshMsg, views.LayersRefreshMsg,
		views.ContextsRefreshMsg,
		views.LogBatchMsg, views.LogClosedMsg:
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
	a.stopStoppableViews()
	if a.client != nil {
		_ = a.client.Close() //nolint:errcheck // process is exiting
	}
}

// switchContextMsg carries the result of a context switch.
type switchContextMsg struct {
	err    error
	client *docker.Client
	name   string
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
	a.client = msg.client

	a.viewMap = map[style.ViewType]views.View{
		style.ViewContainers: views.NewContainersView(a.client, a.showAll, a.statsOn),
		style.ViewImages:     views.NewImagesView(a.client, false),
		style.ViewVolumes:    views.NewVolumesView(a.client),
		style.ViewNetworks:   views.NewNetworksView(a.client),
		style.ViewProjects:   views.NewProjectsView(a.client),
	}
	a.viewStack = nil
	a.view = style.ViewContainers
	a.filter = ""
	a.closeAllBars()
	a.resizeActiveView()
	a.flash = fmt.Sprintf("switched to context %s", msg.name)
	return a, a.viewMap[style.ViewContainers].Init()
}

// doSwitchContext dials a context's endpoint on a background goroutine.
func doSwitchContext(name, host string) tea.Cmd {
	return func() tea.Msg {
		c, err := docker.New(host)
		if err != nil {
			return switchContextMsg{name: name, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.Negotiate(ctx); err != nil {
			_ = c.Close() //nolint:errcheck // dial failed; nothing to salvage
			return switchContextMsg{name: name, err: err}
		}
		c.ContextName = name
		return switchContextMsg{name: name, client: c}
	}
}
