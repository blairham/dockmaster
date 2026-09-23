// Command dockyard is a k9s-style terminal UI for Docker: containers,
// images, volumes, networks, and Compose projects in one navigable frame,
// with live logs, inspect, and the full lifecycle keymap.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockyard/internal/docker"
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
	var (
		host        = flag.String("host", "", "docker daemon endpoint (overrides DOCKER_HOST and the active context)")
		contextName = flag.String("context", "", "docker context to use (overrides the active one)")
		readonly    = flag.Bool("readonly", false, "refuse every mutating action")
		all         = flag.Bool("all", false, "start with stopped containers listed (docker ps -a)")
		noStats     = flag.Bool("no-stats", false, "disable the CPU/MEM poll (one request per running container)")
		logoless    = flag.Bool("logoless", false, "hide the header logo")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Printf("dockyard %s (%s, built %s)\n", version.Version, version.Commit, version.Date)
		return nil
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

	// Connect before starting the TUI. A daemon that is not there is a
	// plain one-line error on stderr, not an alt-screen full of empty
	// tables that the user then has to quit out of to read.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Negotiate(ctx); err != nil {
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
	if *contextName != "" {
		client.ContextName = *contextName
	}

	app := tui.NewApp(client, tui.Options{
		Version:  version.Version,
		ReadOnly: *readonly,
		ShowAll:  *all,
		NoStats:  *noStats,
		Logoless: *logoless,
	})

	// Alt-screen and mouse mode are per-View in bubbletea v2 (set in
	// App.View), not program options.
	p := tea.NewProgram(app)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running dockyard: %w", err)
	}
	return nil
}

// usageHead and usageTail bracket flag.PrintDefaults() in the help output.
const usageHead = `dockyard — a k9s-style TUI for Docker

Usage:
  dockyard [flags]

Flags:
`

const usageTail = `
Endpoint resolution (same precedence as the docker CLI):
  --host, then $DOCKER_HOST, then --context / $DOCKER_CONTEXT, then the
  currentContext in ~/.docker/config.json, then the default socket.

Keys:
  1-5  switch resource    enter  drill in     o  inspect      /  filter
  s/x  start/stop         R  restart          K  kill         p  pause
  e    shell into it      a  toggle all       t  cpu/mem      ?  help
  :    command palette    ctrl-d  remove      P  prune        r  refresh
`

func usage() {
	out := flag.CommandLine.Output()
	// A failed write to the usage stream is unrecoverable — the process is
	// on its way out and stderr is where the complaint would have gone.
	fmt.Fprint(out, usageHead) //nolint:errcheck // unrecoverable; see above
	flag.PrintDefaults()
	fmt.Fprint(out, usageTail) //nolint:errcheck // unrecoverable; see above
}
