package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// composeTimeout bounds a compose call. `up` pulls and builds what it has
// to; a cold one on a slow daemon runs for minutes.
const composeTimeout = 10 * time.Minute

// composeDoneMsg reports the outcome of a compose call.
type composeDoneMsg struct {
	err     error
	project string
	verb    string
}

// newCompose is the compose CLI handle for the daemon on screen. Tests set
// composeRunner to keep `docker` out of the loop.
func (a *App) newCompose() (*docker.Compose, error) {
	if a.composeRunner != nil {
		return docker.NewComposeWithRunner(a.composeRunner, hostOf(a.client)), nil
	}
	return docker.NewCompose(hostOf(a.client))
}

// composeProject looks a project up in the projects view's last listing.
func (a *App) composeProject(name string) (docker.Project, bool) {
	pv := typedView[*views.ProjectsView](a, style.ViewProjects)
	if pv == nil {
		return docker.Project{}, false
	}
	return pv.Project(name)
}

// composeRun marks a project busy and runs a compose verb against it in the
// background. busy is the present participle the row shows meanwhile.
func (a *App) composeRun(p docker.Project, busy, verb string,
	fn func(*docker.Compose, context.Context, docker.Project) error,
) tea.Cmd {
	c, err := a.newCompose()
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	if pv := typedView[*views.ProjectsView](a, style.ViewProjects); pv != nil {
		pv.SetBusy(p.Name, busy)
	}
	a.flash = fmt.Sprintf("%s %s…", busy, p.Name)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
		defer cancel()
		return composeDoneMsg{project: p.Name, verb: verb, err: fn(c, ctx, p)}
	}
}

func (a *App) handleComposeDone(msg composeDoneMsg) (tea.Model, tea.Cmd) {
	if pv := typedView[*views.ProjectsView](a, style.ViewProjects); pv != nil {
		pv.SetBusy(msg.project, "")
	}
	if msg.err != nil {
		a.errFlash = msg.err.Error()
	} else {
		a.flash = msg.verb + " " + msg.project
	}
	return a, a.refreshActiveView()
}

// composeUp brings a project up with `docker compose up -d`, which creates
// what is missing and recreates what changed — not just starting the
// containers that happen to exist. Without the compose files on this
// machine it falls back to starting those containers, and says so.
func (a *App) composeUp(name string) tea.Cmd {
	p, ok := a.composeProject(name)
	if !ok {
		return nil
	}
	if missing := p.MissingFile(); missing != "" {
		cmd := a.runProject(name, "started", func(ctx context.Context, id string) error {
			return a.client.StartContainer(ctx, id)
		})
		a.flash = "compose files unavailable (" + missing + ") — starting the existing containers"
		return cmd
	}
	return a.composeRun(p, "starting", "up", (*docker.Compose).Up)
}

func (a *App) composeRestart(name string) tea.Cmd {
	p, ok := a.composeProject(name)
	if !ok {
		return nil
	}
	if missing := p.MissingFile(); missing != "" {
		a.flash = "compose files unavailable (" + missing + ") — restarting the existing containers"
		return a.runProject(name, "restarted", func(ctx context.Context, id string) error {
			return a.client.RestartContainer(ctx, id, stopTimeout)
		})
	}
	return a.composeRun(p, "restarting", "restarted", (*docker.Compose).Restart)
}

func (a *App) composePull(name string) tea.Cmd {
	p, ok := a.composeProject(name)
	if !ok {
		return nil
	}
	if missing := p.MissingFile(); missing != "" {
		a.errFlash = "cannot pull " + name + ": " + missing
		return nil
	}
	return a.composeRun(p, "pulling", "pulled", (*docker.Compose).Pull)
}

// confirmComposeDown asks before `compose down`, or — without the compose
// files — before force-removing the containers one by one, which is all
// that is possible then.
func (a *App) confirmComposeDown(name string) {
	p, ok := a.composeProject(name)
	if !ok {
		return
	}
	if missing := p.MissingFile(); missing != "" {
		a.openConfirm("remove_project", name, fmt.Sprintf(
			"force-remove every container in %s? (compose files unavailable: %s)", name, missing,
		))
		return
	}
	a.openConfirm("compose_down", name, fmt.Sprintf(
		"compose down %s? its containers and networks are removed — volumes are kept", name,
	))
}

func (a *App) composeDown(name string) tea.Cmd {
	p, ok := a.composeProject(name)
	if !ok {
		return nil
	}
	return a.composeRun(p, "removing", "took down", (*docker.Compose).Down)
}
