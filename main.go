// Command dockyard is a k9s-style terminal UI for Docker: containers,
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

	"github.com/blairham/dockyard/internal/config"
	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/engines"
	"github.com/blairham/dockyard/internal/tui"
	"github.com/blairham/dockyard/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "dockyard: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "config" {
		return config.Command(os.Args[2:], os.Stdout)
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
		command     string
		refresh     int
		reqTimeout  = flag.Duration("request-timeout", 0,
			"how long one daemon request may take, as 30s or 2m (0 keeps each request's own: 20s for a list, 5m for images)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	const commandUsage = "view to open on: containers, images, volumes, networks, projects or runtimes"
	flag.StringVar(&command, "command", "", commandUsage)
	flag.StringVar(&command, "c", "", commandUsage+" (shorthand)")
	const refreshUsage = "auto-refresh interval in seconds (default 3)"
	flag.IntVar(&refresh, "refresh", 0, refreshUsage)
	flag.IntVar(&refresh, "r", 0, refreshUsage+" (shorthand)")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Printf("dockyard %s (%s, built %s)\n", version.Version, version.Commit, version.Date)
		return nil
	}

	cfgPath, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	// Flags set on the command line win over the file; flag.Visit sees
	// only those, so --readonly=false can switch off a readOnly: true.
	cfg = config.ApplyFlags(cfg, config.SetFlags(flag.CommandLine), config.FlagValues{
		ReadOnly: *readonly, ShowAll: *all, NoStats: *noStats, Logoless: *logoless,
		Splashless: *splashless, Headless: *headless, Crumbsless: *crumbsless, Command: command,
		Refresh: refresh, RequestTimeout: *reqTimeout,
	})
	if verr := cfg.Validate(); verr != nil {
		return verr
	}

	if cfg.DefaultView != "" {
		if _, ok := tui.ViewForCommand(cfg.DefaultView); !ok {
			src := "-c"
			if command == "" {
				src = "defaultView in " + cfgPath
			}
			return fmt.Errorf("unknown view %q for %s — one of: %s",
				cfg.DefaultView, src, strings.Join(tui.ViewCommandNames(), ", "))
		}
	}
	// config.yaml's context is a default --context: the command line and
	// the session's own DOCKER_HOST / DOCKER_CONTEXT are more specific.
	if *host == "" && *contextName == "" && os.Getenv("DOCKER_HOST") == "" && os.Getenv("DOCKER_CONTEXT") == "" {
		*contextName = cfg.Context
	}

	// A --context flag is resolved through the store the same way the
	// docker CLI resolves its own, so `dockyard --context colima` behaves
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

	client, err := docker.New(endpoint)
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
	// on the runtimes view, because starting it is the fix and dockyard can
	// do that.
	startOnRuntimes, notice := false, ""
	if err := client.Negotiate(ctx); err != nil {
		m, ok := engines.Owner(ctx, providers, client.Host)
		if !ok {
			return daemonError(err, client)
		}
		startOnRuntimes = true
		notice = fmt.Sprintf("no docker daemon at %s — %s %s is not running; <u> starts it",
			client.Host, m.Provider, m.Name)
	}
	if *contextName != "" {
		client.ContextName = *contextName
	}

	app := tui.NewApp(client, tui.Options{
		Version:         version.Version,
		ReadOnly:        cfg.ReadOnly,
		ShowAll:         cfg.ShowAll,
		NoStats:         cfg.NoStats,
		Logoless:        cfg.UI.Logoless,
		Splashless:      cfg.UI.Splashless,
		Headless:        cfg.UI.Headless,
		Crumbsless:      cfg.UI.Crumbsless,
		Command:         cfg.DefaultView,
		RefreshRate:     time.Duration(cfg.RefreshRate) * time.Second,
		LogTail:         cfg.Logger.Tail,
		LogShowTime:     cfg.Logger.ShowTime,
		RequestTimeout:  cfg.RequestTimeout,
		Engines:         providers,
		StartOnRuntimes: startOnRuntimes,
		Notice:          notice,
	})

	// Alt-screen and mouse mode are per-View in bubbletea v2 (set in
	// App.View), not program options.
	p := tea.NewProgram(app)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running dockyard: %w", err)
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
		"%w\n  endpoint: %s (from context %q)\n  `docker context ls` shows what dockyard resolves from",
		docker.FormatUserError(err),
		client.Host,
		client.ContextName,
	)
}

// progName is the name this binary was run as — dockyard, or its d6d alias
// — so the usage line shows what the user typed.
func progName() string {
	if name := filepath.Base(os.Args[0]); name != "" && name != "." {
		return name
	}
	return "dockyard"
}

// usageHead and usageTail bracket flag.PrintDefaults() in the help output.
const usageHead = `dockyard — a k9s-style TUI for Docker

Usage:
  %s [flags]

d6d is dockyard's short name (d + 6 letters + d, as k8s is Kubernetes);
make install puts both on PATH.

Flags:
`

const usageTail = `
Config:
  %[1]s config path    print where config.yaml is read from
  %[1]s config init    write a commented config.yaml with the defaults
  The file is $DOCKYARD_CONFIG_DIR/config.yaml, else
  $XDG_CONFIG_HOME/dockyard/config.yaml, else ~/.config/dockyard/config.yaml.
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
	_, err = c.API().Ping(ctx)
	return err == nil
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
