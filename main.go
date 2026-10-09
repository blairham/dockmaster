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
	"io"
	"log/slog"
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
	if cmd := subcommand(os.Args); cmd != nil {
		return cmd(os.Args[2:], os.Stdout)
	}

	f := parseFlags()
	if f.showVersion {
		fmt.Printf("dockmaster %s (%s, built %s)\n", version.Version, version.Commit, version.Date)
		return nil
	}

	logger, logCloser, err := applog.Open(f.logFile, f.logLevel)
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
	flagVals := f.values()
	st, err := loadSettings(cfgPath, flagSet, flagVals)
	if err != nil {
		return err
	}
	cfg := st.cfg

	// The skin goes in before anything is built: every style derives from
	// the base theme.
	style.SetBase(st.theme)

	if verr := checkDefaultView(cfg, st.aliases, f.command, cfgPath); verr != nil {
		return verr
	}
	// config.yaml's context is a default --context: the command line and
	// the session's own DOCKER_HOST / DOCKER_CONTEXT are more specific.
	if f.host == "" && f.contextName == "" && !dockerEnvSet() {
		f.contextName = cfg.Context
	}

	client, err := newDockerClient(f.host, f.contextName)
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

	startOnRuntimes, notice, err := connect(ctx, client, providers, logger)
	if err != nil {
		return err
	}
	if f.contextName != "" {
		client.ContextName = f.contextName
	}
	logger.Info("start", "version", version.Version, "context", client.ContextName, "endpoint", client.Host)

	opts := startupOptions(st, startup{
		providers: providers, startOnRuntimes: startOnRuntimes, notice: notice, logger: logger,
		cfgPath: cfgPath, flagSet: flagSet, flagVals: flagVals,
	})
	app := tui.NewApp(client, opts)

	// Alt-screen and mouse mode are per-View in bubbletea v2 (set in
	// App.View), not program options.
	p := tea.NewProgram(app)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running dockmaster: %w", err)
	}
	return nil
}

// subcommand is the `config` or `info` subcommand args names, or nil for the
// TUI.
func subcommand(args []string) func([]string, io.Writer) error {
	if len(args) < 2 {
		return nil
	}
	switch args[1] {
	case "config":
		return config.Command
	case "info":
		return info.Command
	}
	return nil
}

// dockerEnvSet reports whether the session names its own daemon, which is
// more specific than config.yaml's context.
func dockerEnvSet() bool {
	return os.Getenv("DOCKER_HOST") != "" || os.Getenv("DOCKER_CONTEXT") != ""
}

// startup is what run learned before building the app.
type startup struct {
	logger          *slog.Logger
	flagSet         map[string]bool
	cfgPath, notice string
	providers       []engines.Provider
	flagVals        config.FlagValues
	startOnRuntimes bool
}

// startupOptions is the app's options at startup: the config directory's,
// plus what only startup knows.
func startupOptions(st settings, su startup) tui.Options {
	cfg := st.cfg
	opts := settingsOptions(st)
	opts.Version = version.Version
	opts.ShowAll, opts.NoStats = cfg.ShowAll, cfg.NoStats
	opts.Splashless = cfg.UI.Splashless
	opts.Command = cfg.DefaultView
	opts.RequestTimeout = cfg.RequestTimeout
	opts.Engines = su.providers
	opts.StartOnRuntimes = su.startOnRuntimes
	opts.Notice = su.notice
	opts.HistoryFile = historyFile()
	opts.ContextStateFile = stateFile("contexts.json")
	opts.ScanCacheDir = stateFile("scans")
	opts.CommandFromFlag = su.flagSet["c"] || su.flagSet["command"]
	opts.Logger = su.logger
	if cfg.UI.Reactive {
		opts.WatchDir = filepath.Dir(su.cfgPath)
		opts.Reload = reloader(su.cfgPath, su.flagSet, su.flagVals, cfg)
	}
	return opts
}

// cliFlags is the command line, parsed.
type cliFlags struct {
	host, contextName, command, logLevel, logFile string
	reqTimeout                                    time.Duration
	refresh                                       int
	readonly, all, noStats, logoless, splashless  bool
	headless, crumbsless, invert, showVersion     bool
}

// parseFlags defines the flags on flag.CommandLine and parses them.
func parseFlags() *cliFlags {
	var f cliFlags
	flag.StringVar(&f.host, "host", "", "docker daemon endpoint (overrides DOCKER_HOST and the active context)")
	flag.StringVar(&f.contextName, "context", "", "docker context to use (overrides the active one)")
	flag.BoolVar(&f.readonly, "readonly", false, "refuse every mutating action")
	flag.BoolVar(&f.all, "all", false, "start with stopped containers listed (docker ps -a)")
	flag.BoolVar(&f.noStats, "no-stats", false, "disable the CPU/MEM poll (one request per running container)")
	flag.BoolVar(&f.logoless, "logoless", false, "hide the header logo")
	flag.BoolVar(&f.splashless, "splashless", false, "skip the startup splash")
	flag.BoolVar(&f.headless, "headless", false, "hide the header (info, shortcuts, logo)")
	flag.BoolVar(&f.crumbsless, "crumbsless", false, "hide the breadcrumbs")
	flag.BoolVar(&f.invert, "invert", false, "invert the skin, dark to light or light to dark, keeping its colors")
	flag.DurationVar(&f.reqTimeout, "request-timeout", 0,
		"how long one daemon request may take, as 30s or 2m (0 keeps each request's own: 20s for a list, 5m for images)")
	flag.BoolVar(&f.showVersion, "version", false, "print version and exit")
	flag.StringVar(&f.logLevel, "log-level", applog.DefaultLevel, "log level: "+strings.Join(applog.Levels, ", "))
	flag.StringVar(
		&f.logFile,
		"log-file",
		"",
		"log file (default <state dir>/"+applog.FileName+"; dockmaster info shows it)",
	)
	commandUsage := "view or : command to open on (" + strings.Join(tui.ViewCommandNames(), ", ") + ", xray, an alias…)"
	flag.StringVar(&f.command, "command", "", commandUsage)
	flag.StringVar(&f.command, "c", "", commandUsage+" (shorthand)")
	const refreshUsage = "auto-refresh interval in seconds (default 3)"
	flag.IntVar(&f.refresh, "refresh", 0, refreshUsage)
	flag.IntVar(&f.refresh, "r", 0, refreshUsage+" (shorthand)")
	flag.Usage = usage
	flag.Parse()
	return &f
}

// values is the part of the command line config.yaml can also set.
func (f *cliFlags) values() config.FlagValues {
	return config.FlagValues{
		ReadOnly: f.readonly, ShowAll: f.all, NoStats: f.noStats, Logoless: f.logoless,
		Splashless: f.splashless, Headless: f.headless, Crumbsless: f.crumbsless, Invert: f.invert,
		Command: f.command, Refresh: f.refresh, RequestTimeout: f.reqTimeout,
	}
}

// checkDefaultView refuses a -c (or config.yaml defaultView) that names no
// view, naming where it came from.
func checkDefaultView(cfg config.Config, aliases map[string]string, command, cfgPath string) error {
	if cfg.DefaultView == "" {
		return nil
	}
	verr := tui.ValidateCommand(cfg.DefaultView, aliases)
	if verr == nil {
		return nil
	}
	src := "-c"
	if command == "" {
		src = "defaultView in " + cfgPath
	}
	return fmt.Errorf("%s: %w", src, verr)
}

// newDockerClient connects to --host, or to a --context resolved through the
// store the same way the docker CLI resolves its own, so `dockmaster
// --context colima` behaves like `docker --context colima`.
func newDockerClient(host, contextName string) (*docker.Client, error) {
	if host != "" || contextName == "" {
		return docker.New(host)
	}
	for _, c := range docker.Contexts() {
		if c.Name == contextName {
			// The context's TLS material as well as its host (#33).
			return docker.NewForContext(contextName, c.Host)
		}
	}
	return nil, fmt.Errorf("no docker context named %q — `docker context ls` shows the available ones", contextName)
}

// connect reaches the daemon before starting the TUI. A daemon that is not
// there is a plain one-line error on stderr, not an alt-screen full of empty
// tables that the user then has to quit out of to read — unless the endpoint
// belongs to a runtime's machine, in which case the TUI opens on the runtimes
// view, because starting it is the fix and dockmaster can do that.
func connect(
	ctx context.Context,
	client *docker.Client,
	providers []engines.Provider,
	logger *slog.Logger,
) (startOnRuntimes bool, notice string, err error) {
	nerr := client.Negotiate(ctx)
	if nerr == nil {
		return false, "", nil
	}
	m, ok := engines.Owner(ctx, providers, client.Host)
	if !ok {
		logger.Error("no daemon", "endpoint", client.Host, "error", nerr)
		return false, "", daemonError(nerr, client)
	}
	return true, fmt.Sprintf("no docker daemon at %s — %s %s is not running; <u> starts it",
		client.Host, m.Provider, m.Name), nil
}

// reloader re-reads the config directory for a live reload (ui.reactive),
// naming the settings that only take effect on a restart.
func reloader(
	cfgPath string,
	flagSet map[string]bool,
	flagVals config.FlagValues,
	cfg config.Config,
) func() (tui.Reloaded, error) {
	return func() (tui.Reloaded, error) {
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
// state, the mouse, shell and portForwardAddress.
func behaviorOptions(cfg config.Config, o *tui.Options) {
	o.NoExitOnCtrlC = cfg.NoExitOnCtrlC
	o.DumpDir = config.ExpandHome(cfg.ScreenDumpDir)
	o.LogWrap = cfg.Logger.TextWrap
	o.LogPaused = cfg.Logger.DisableAutoscroll
	o.LogFullscreen = cfg.UI.DefaultsToFullScreen
	o.NoMouse = !cfg.UI.EnableMouse
	o.Shell = cfg.Shell
	o.ForwardAddress = cfg.ForwardAddress()
	o.HostShellImage = cfg.HostShell.Image
}

// settings is everything read from the config directory: config.yaml with
// the command line's flags over it, the aliases, hotkeys, plugins, jumps
// and view columns, and the skin's theme — validated, as dockmaster will not start
// on a bad file.
type settings struct {
	theme theme.Theme
	// ctxThemes are the contexts: entries' skins, by context name.
	ctxThemes map[string]theme.Theme
	aliases   map[string]string
	cfg       config.Config
	hotKeys   []tui.HotKey
	plugins   []tui.Plugin
	jumps     []tui.Jump
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
	st := settings{cfg: cfg, forceReadOnly: flagSet["readonly"] && flagVals.ReadOnly}
	if err := st.loadBindings(); err != nil {
		return settings{}, err
	}
	if err := st.loadThemes(); err != nil {
		return settings{}, err
	}
	return st, nil
}

// loadBindings reads the aliases, hotkeys, plugins, jumps and view columns,
// each validated against what it may refer to.
func (st *settings) loadBindings() error {
	aliases, err := config.LoadAliases()
	if err != nil {
		return err
	}
	if verr := tui.ValidateAliases(aliases); verr != nil {
		return verr
	}
	hotKeyFile, err := config.LoadHotKeys()
	if err != nil {
		return err
	}
	hotKeys, err := tui.HotKeys(hotKeyFile, aliases)
	if err != nil {
		return err
	}
	pluginFile, err := config.LoadPlugins()
	if err != nil {
		return err
	}
	plugins, err := tui.Plugins(pluginFile, hotKeys)
	if err != nil {
		return err
	}
	st.aliases, st.hotKeys, st.plugins = aliases, hotKeys, plugins
	return st.loadLayout()
}

// loadLayout reads the jumps and the view columns.
func (st *settings) loadLayout() error {
	jumpFile, err := config.LoadJumps()
	if err != nil {
		return err
	}
	jumps, err := tui.Jumps(jumpFile)
	if err != nil {
		return err
	}
	viewFile, err := config.LoadViews()
	if err != nil {
		return err
	}
	columns, err := tui.ColumnLayouts(viewFile)
	if err != nil {
		return err
	}
	st.jumps, st.columns = jumps, columns
	return nil
}

// loadThemes loads the skin and each context's skin, as ui.skin does, and
// checks each context's defaultView as -c is: a bad entry stops startup and a
// reload.
func (st *settings) loadThemes() error {
	th, err := st.cfg.Theme(style.DefaultBase())
	if err != nil {
		return err
	}
	ctxThemes, err := st.cfg.ContextThemes(style.DefaultBase())
	if err != nil {
		return err
	}
	for name, cs := range st.cfg.Contexts {
		if cs.DefaultView == "" {
			continue
		}
		if verr := tui.ValidateContextCommand(cs.DefaultView, st.aliases); verr != nil {
			return fmt.Errorf("contexts.%s.defaultView: %w", name, verr)
		}
	}
	st.theme, st.ctxThemes = th, ctxThemes
	return nil
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
		Jumps:         st.jumps,
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
