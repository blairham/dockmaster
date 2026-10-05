// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Command dockmaster is a k9s-style terminal UI for Docker: containers,
// images, volumes, networks, and Compose projects in one navigable frame,
// with live logs, inspect, and the full lifecycle keymap.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/theme"

	"github.com/blairham/dockmaster/internal/applog"
	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/info"
	"github.com/blairham/dockmaster/internal/tui"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
	"github.com/blairham/dockmaster/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "dockmaster: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "config" {
		return config.Command(os.Args[2:], os.Stdout)
	}
	if len(os.Args) > 1 && os.Args[1] == "info" {
		return info.Command(os.Args[2:], os.Stdout)
	}

	var (
		host        = flag.String("host", "", "docker daemon endpoint (overrides DOCKER_HOST and the active context)")
		contextName = flag.String("context", "", "docker context to use (overrides the active one)")
		readonly    = flag.Bool("readonly", false, "refuse every mutating action")
		all         = flag.Bool("all", false, "start with stopped containers listed (docker ps -a)")
		noStats     = flag.Bool("no-stats", false, "disable the CPU/MEM poll (one request per running container)")
		logoless    = flag.Bool("logoless", false, "hide the header logo")
		splashless  = flag.Bool("splashless", false, "skip the startup splash")
		headless    = flag.Bool("headless", false, "hide the header (info, shortcuts, logo)")
		crumbsless  = flag.Bool("crumbsless", false, "hide the breadcrumbs")
		invert      = flag.Bool("invert", false, "invert the skin, dark to light or light to dark, keeping its colors")
		command     string
		refresh     int
		reqTimeout  = flag.Duration("request-timeout", 0,
			"how long one daemon request may take, as 30s or 2m (0 keeps each request's own: 20s for a list, 5m for images)")
		showVersion = flag.Bool("version", false, "print version and exit")
		logLevel    = flag.String("log-level", applog.DefaultLevel, "log level: "+strings.Join(applog.Levels, ", "))
		logFile     = flag.String(
			"log-file",
			"",
			"log file (default <state dir>/"+applog.FileName+"; dockmaster info shows it)",
		)
	)
	commandUsage := "view or : command to open on (" + strings.Join(tui.ViewCommandNames(), ", ") + ", xray, an alias…)"
	flag.StringVar(&command, "command", "", commandUsage)
	flag.StringVar(&command, "c", "", commandUsage+" (shorthand)")
	const refreshUsage = "auto-refresh interval in seconds (default 3)"
	flag.IntVar(&refresh, "refresh", 0, refreshUsage)
	flag.IntVar(&refresh, "r", 0, refreshUsage+" (shorthand)")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Printf("dockmaster %s (%s, built %s)\n", version.Version, version.Commit, version.Date)
		return nil
	}

	logger, logCloser, err := applog.Open(*logFile, *logLevel)
	if err != nil {
		return err
	}
	defer logCloser.Close() //nolint:errcheck // process is exiting

	cfgPath, err := config.Path()
	if err != nil {
		return err
	}
	// Flags set on the command line win over the file; flag.Visit sees
	// only those, so --readonly=false can switch off a readOnly: true. A
	// live reload (ui.reactive) applies the same flags again.
	flagSet := config.SetFlags(flag.CommandLine)
	flagVals := config.FlagValues{
		ReadOnly: *readonly, ShowAll: *all, NoStats: *noStats, Logoless: *logoless,
		Splashless: *splashless, Headless: *headless, Crumbsless: *crumbsless, Invert: *invert, Command: command,
		Refresh: refresh, RequestTimeout: *reqTimeout,
	}
	st, err := loadSettings(cfgPath, flagSet, flagVals)
	if err != nil {
		return err
	}
	cfg := st.cfg

	// The skin goes in before anything is built: every style derives from
	// the base theme.
	style.SetBase(st.theme)

	if cfg.DefaultView != "" {
		if verr := tui.ValidateCommand(cfg.DefaultView, st.aliases); verr != nil {
			src := "-c"
			if command == "" {
				src = "defaultView in " + cfgPath
			}
			return fmt.Errorf("%s: %w", src, verr)
		}
	}
	// config.yaml's context is a default --context: the command line and
	// the session's own DOCKER_HOST / DOCKER_CONTEXT are more specific.
	if *host == "" && *contextName == "" && os.Getenv("DOCKER_HOST") == "" && os.Getenv("DOCKER_CONTEXT") == "" {
		*contextName = cfg.Context
	}

	// A --context flag is resolved through the store the same way the
	// docker CLI resolves its own, so `dockmaster --context colima` behaves
	// like `docker --context colima`.
	endpoint := *host
	if endpoint == "" && *contextName != "" {
		found := false
		for _, c := range docker.Contexts() {
			if c.Name == *contextName {
				endpoint, found = c.Host, true
				break
			}
		}
		if !found {
			return fmt.Errorf("no docker context named %q — `docker context ls` shows the available ones", *contextName)
		}
	}

	newClient := docker.New
	if *host == "" && *contextName != "" {
		// The context's TLS material as well as its host (#33).
		newClient = func(h string) (*docker.Client, error) { return docker.NewForContext(*contextName, h) }
	}
	client, err := newClient(endpoint)
	if err != nil {
		return err
	}
	defer client.Close() //nolint:errcheck // process is exiting

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The container runtimes installed here — colima, podman, Docker
	// Desktop, Rancher Desktop, OrbStack — for the runtimes view. All are
	// optional; with none, the view says what to install.
	providers := engines.Detect(ctx, engines.SystemEnv(pingDaemon, contextHost))

	// Connect before starting the TUI. A daemon that is not there is a
	// plain one-line error on stderr, not an alt-screen full of empty
	// tables that the user then has to quit out of to read — unless the
	// endpoint belongs to a runtime's machine, in which case the TUI opens
	// on the runtimes view, because starting it is the fix and dockmaster can
	// do that.
	startOnRuntimes, notice := false, ""
	if err := client.Negotiate(ctx); err != nil {
		m, ok := engines.Owner(ctx, providers, client.Host)
		if !ok {
			logger.Error("no daemon", "endpoint", client.Host, "error", err)
			return daemonError(err, client)
		}
		startOnRuntimes = true
		notice = fmt.Sprintf("no docker daemon at %s — %s %s is not running; <u> starts it",
			client.Host, m.Provider, m.Name)
	}
	if *contextName != "" {
		client.ContextName = *contextName
	}
	logger.Info("start", "version", version.Version, "context", client.ContextName, "endpoint", client.Host)

	opts := settingsOptions(st)
	opts.Version = version.Version
	opts.ShowAll, opts.NoStats = cfg.ShowAll, cfg.NoStats
	opts.Splashless = cfg.UI.Splashless
	opts.Command = cfg.DefaultView
	opts.RequestTimeout = cfg.RequestTimeout
	opts.Engines = providers
	opts.StartOnRuntimes = startOnRuntimes
	opts.Notice = notice
	opts.HistoryFile = historyFile()
	opts.ContextStateFile = stateFile("contexts.json")
	opts.ScanCacheDir = stateFile("scans")
	opts.CommandFromFlag = flagSet["c"] || flagSet["command"]
	opts.Logger = logger
	if cfg.UI.Reactive {
		opts.WatchDir = filepath.Dir(cfgPath)
		opts.Reload = func() (tui.Reloaded, error) {
			next, err := loadSettings(cfgPath, flagSet, flagVals)
			if err != nil {
				return tui.Reloaded{}, err
			}
			var restart []string
			if next.cfg.Context != cfg.Context {
				restart = append(restart, "context")
			}
			if next.cfg.RequestTimeout != cfg.RequestTimeout {
				restart = append(restart, "requestTimeout")
			}
			return tui.Reloaded{Options: settingsOptions(next), Theme: next.theme, NeedsRestart: restart}, nil
		}
	}
	app := tui.NewApp(client, opts)

	// Alt-screen and mouse mode are per-View in bubbletea v2 (set in
	// App.View), not program options.
	p := tea.NewProgram(app)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running dockmaster: %w", err)
	}
	return nil
}

// daemonError is the user-facing error for a daemon that did not answer.
func daemonError(err error, client *docker.Client) error {
	return fmt.Errorf(
		// FormatUserError already poses the "is it running?" question on a
		// connection failure. Add only the endpoint it actually tried —
		// the part the user cannot otherwise see, and the whole point of
		// resolving through the context store.
		"%w\n  endpoint: %s (from context %q)\n  `docker context ls` shows what dockmaster resolves from",
		docker.FormatUserError(err),
		client.Host,
		client.ContextName,
	)
}

// progName is the name this binary was run as — dockmaster, or its dm alias
// — so the usage line shows what the user typed.
func progName() string {
	if name := filepath.Base(os.Args[0]); name != "" && name != "." {
		return name
	}
	return "dockmaster"
}

// usageHead and usageTail bracket flag.PrintDefaults() in the help output.
const usageHead = `dockmaster — a k9s-style TUI for Docker

Usage:
  %s [flags]

dm is dockmaster's short name; make install puts both on PATH.

Flags:
`

const usageTail = `
Config:
  %[1]s config path    print where config.yaml is read from
  %[1]s config init    write a commented config.yaml with the defaults
  %[1]s info           print the config, state, log and skins paths and the docker endpoint
  The file is $DOCKMASTER_CONFIG_DIR/config.yaml, else
  $XDG_CONFIG_HOME/dockmaster/config.yaml, else ~/.config/dockmaster/config.yaml.
  Flags given on the command line override it.

Endpoint resolution (same precedence as the docker CLI):
  --host, then $DOCKER_HOST, then --context / $DOCKER_CONTEXT, then
  context in config.yaml, then the currentContext in ~/.docker/config.json,
  then the default socket.

Keys:
  0-6  switch resource    enter  drill in     o  inspect      /  filter
  u/x  start/stop         R  restart          K  kill         p  pause
  s    shell into it      a  toggle all       t  cpu/mem      ?  help
  :    command palette    ctrl-d  remove      P  prune        r  refresh
`

func usage() {
	out := flag.CommandLine.Output()
	// A failed write to the usage stream is unrecoverable — the process is
	// on its way out and stderr is where the complaint would have gone.
	fmt.Fprintf(out, usageHead, progName()) //nolint:errcheck // unrecoverable; see above
	flag.PrintDefaults()
	fmt.Fprintf(out, usageTail, progName()) //nolint:errcheck // unrecoverable; see above
}

// pingDaemon reports whether a docker daemon answers at host — how the
// single-engine runtimes' status is read.
func pingDaemon(ctx context.Context, host string) bool {
	c, err := docker.New(host)
	if err != nil {
		return false
	}
	defer c.Close() //nolint:errcheck // probe client
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.Ping(ctx) == nil
}

// contextHost is a docker context's endpoint, from the context store.
func contextHost(name string) (string, bool) {
	for _, c := range docker.Contexts() {
		if c.Name == name {
			return c.Host, true
		}
	}
	return "", false
}

// historyFile is where the command and filter bars remember what was typed
// between runs, "" when there is no state directory to keep it in.
func historyFile() string { return stateFile("history.json") }

// stateFile is name in the state directory, "" when there is none.
func stateFile(name string) string {
	dir, err := config.StateDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, name)
}

// behaviorOptions carries config.yaml's k9s behavior keys (#17) into the
// app's options — noExitOnCtrlC, screenDumpDir, the log view's opening
// state and the mouse.
func behaviorOptions(cfg config.Config, o *tui.Options) {
	o.NoExitOnCtrlC = cfg.NoExitOnCtrlC
	o.DumpDir = config.ExpandHome(cfg.ScreenDumpDir)
	o.LogWrap = cfg.Logger.TextWrap
	o.LogPaused = cfg.Logger.DisableAutoscroll
	o.LogFullscreen = cfg.UI.DefaultsToFullScreen
	o.NoMouse = !cfg.UI.EnableMouse
	o.Shell = cfg.Shell
	o.HostShellImage = cfg.HostShell.Image
}

// settings is everything read from the config directory: config.yaml with
// the command line's flags over it, the aliases, hotkeys, plugins and view
// columns, and the skin's theme — validated, as dockmaster will not start
// on a bad file.
type settings struct {
	theme theme.Theme
	// ctxThemes are the contexts: entries' skins, by context name.
	ctxThemes map[string]theme.Theme
	aliases   map[string]string
	cfg       config.Config
	hotKeys   []tui.HotKey
	plugins   []tui.Plugin
	columns   map[string]views.ColumnLayout
	// forceReadOnly is --readonly given on the command line, which beats
	// every context's readOnly.
	forceReadOnly bool
}

// loadSettings reads and validates the config directory. Startup and a live
// reload (ui.reactive) both go through it, so a reload can never accept
// what startup would refuse.
func loadSettings(cfgPath string, flagSet map[string]bool, flagVals config.FlagValues) (settings, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return settings{}, err
	}
	cfg = config.ApplyFlags(cfg, flagSet, flagVals)
	if verr := cfg.Validate(); verr != nil {
		return settings{}, verr
	}
	aliases, err := config.LoadAliases()
	if err != nil {
		return settings{}, err
	}
	if aerr := tui.ValidateAliases(aliases); aerr != nil {
		return settings{}, aerr
	}
	hotKeyFile, err := config.LoadHotKeys()
	if err != nil {
		return settings{}, err
	}
	hotKeys, err := tui.HotKeys(hotKeyFile, aliases)
	if err != nil {
		return settings{}, err
	}
	pluginFile, err := config.LoadPlugins()
	if err != nil {
		return settings{}, err
	}
	plugins, err := tui.Plugins(pluginFile, hotKeys)
	if err != nil {
		return settings{}, err
	}
	viewFile, err := config.LoadViews()
	if err != nil {
		return settings{}, err
	}
	columns, err := tui.ColumnLayouts(viewFile)
	if err != nil {
		return settings{}, err
	}
	th, err := cfg.Theme(style.DefaultBase())
	if err != nil {
		return settings{}, err
	}
	// Each context's skin loads now, as ui.skin does, and its defaultView
	// is checked as -c is: a bad entry stops startup and a reload.
	ctxThemes, err := cfg.ContextThemes(style.DefaultBase())
	if err != nil {
		return settings{}, err
	}
	for name, cs := range cfg.Contexts {
		if cs.DefaultView == "" {
			continue
		}
		if verr := tui.ValidateContextCommand(cs.DefaultView, aliases); verr != nil {
			return settings{}, fmt.Errorf("contexts.%s.defaultView: %w", name, verr)
		}
	}
	return settings{
		cfg: cfg, aliases: aliases, hotKeys: hotKeys, plugins: plugins, columns: columns, theme: th,
		ctxThemes: ctxThemes, forceReadOnly: flagSet["readonly"] && flagVals.ReadOnly,
	}, nil
}

// settingsOptions is the part of the app's options that comes from the
// config directory — what a live reload can change.
func settingsOptions(st settings) tui.Options {
	cfg := st.cfg
	o := tui.Options{
		ReadOnly:      cfg.ReadOnly,
		Logoless:      cfg.UI.Logoless,
		Headless:      cfg.UI.Headless,
		Crumbsless:    cfg.UI.Crumbsless,
		RefreshRate:   time.Duration(cfg.RefreshRate) * time.Second,
		LogTail:       cfg.Logger.Tail,
		LogShowTime:   cfg.Logger.ShowTime,
		LogBuffer:     cfg.Logger.Buffer,
		LogSince:      time.Duration(max(cfg.Logger.SinceSeconds, 0)) * time.Second,
		LiveRefresh:   cfg.LiveViewAutoRefresh,
		Aliases:       st.aliases,
		HotKeys:       st.hotKeys,
		Plugins:       st.plugins,
		ColumnLayouts: st.columns,
		Thresholds:    tui.ThresholdsFrom(cfg.Thresholds),
		ImageScans: tui.ImageScans{
			Enable: cfg.ImageScans.Enable, Background: cfg.ImageScans.Background, TTL: cfg.ImageScans.TTL,
		},
	}
	behaviorOptions(cfg, &o)
	o.ForceReadOnly = st.forceReadOnly
	o.Contexts = contextOptions(cfg.Contexts, st.ctxThemes)
	return o
}

// contextOptions resolves config.yaml's contexts: block for the app: each
// context's loaded skin, its readOnly and defaultView.
func contextOptions(
	in map[string]config.ContextSettings,
	themes map[string]theme.Theme,
) map[string]tui.ContextSettings {
	out := make(map[string]tui.ContextSettings, len(in))
	for name, cs := range in {
		o := tui.ContextSettings{ReadOnly: cs.ReadOnly, DefaultView: cs.DefaultView}
		if th, ok := themes[name]; ok {
			o.Theme = &th
		}
		out[name] = o
	}
	return out
}
