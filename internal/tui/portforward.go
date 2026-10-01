package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
	"github.com/blairham/dockyard/internal/tui/views"
)

// forwardTimeout bounds starting a forward: the first one pulls the helper
// image.
const forwardTimeout = 2 * time.Minute

// forwardStartedMsg reports a forward starting, or why it did not.
type forwardStartedMsg struct {
	err error
	pf  docker.PortForward
}

// promptForward asks for the ports to forward to a container, prefilled
// with its first TCP port.
func (a *App) promptForward(id string) tea.Cmd {
	c, ok := a.containerByID(id)
	if !ok {
		return nil
	}
	a.promptDispatch = func(value string) (string, tea.Cmd) {
		local, remote, err := docker.ParseForwardSpec(value)
		if err != nil {
			return err.Error(), nil
		}
		return "", a.startForward(c, local, remote)
	}
	cmd := a.prompt.Open(docker.DefaultForwardSpec(c), chrome.OpenOpts{
		Prompt:      fmt.Sprintf("⇄ forward %s  local:container ", c.Name),
		Placeholder: "8080:80",
	})
	a.resizeActiveView()
	return cmd
}

func (a *App) startForward(c docker.Container, local, remote int) tea.Cmd {
	a.flash = fmt.Sprintf("forwarding localhost:%d → %s:%d…", local, c.Name, remote)
	client := a.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), forwardTimeout)
		defer cancel()
		pf, err := client.StartPortForward(ctx, c, local, remote)
		return forwardStartedMsg{pf: pf, err: err}
	}
}

func (a *App) handleForwardStarted(msg forwardStartedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.errFlash = docker.FormatUserError(msg.err).Error()
		return a, nil
	}
	a.forwards = append(a.forwards, msg.pf.ID)
	a.flash = fmt.Sprintf("forwarding %s — :pf lists forwards, b opens it", views.ForwardLabel(msg.pf))
	return a, a.refreshActiveView()
}

// stopSessionForwards removes the forwards this session started, as k9s's
// end with k9s. The helpers live on the daemon, so without this they would
// outlast dockyard and keep their ports. Best effort, bounded: quitting must
// not hang on a slow daemon.
func (a *App) stopSessionForwards() {
	stop := a.stopForward
	if stop == nil && a.client != nil {
		stop = a.client.StopPortForward
	}
	if stop == nil || len(a.forwards) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, id := range a.forwards {
		_ = stop(ctx, id) //nolint:errcheck // exiting; nothing to report to
	}
	a.forwards = nil
}

// openPublished opens a container's first published TCP port in the
// browser.
func (a *App) openPublished(id string) tea.Cmd {
	c, ok := a.containerByID(id)
	if !ok {
		return nil
	}
	for _, p := range c.PortList {
		if p.Public != 0 && (p.Type == "" || p.Type == "tcp") {
			return a.openURL("http://localhost:" + strconv.Itoa(int(p.Public)))
		}
	}
	a.errFlash = c.Name + " publishes no ports — <F> forwards one"
	return nil
}

// openURL opens a URL with the platform's opener. Tests replace urlOpener.
func (a *App) openURL(url string) tea.Cmd {
	if err := a.urlOpener(url); err != nil {
		a.errFlash = "opening " + url + ": " + err.Error()
		return nil
	}
	a.flash = "opened " + url
	return nil
}

// openInBrowser starts the platform's URL opener without waiting for it.
func openInBrowser(url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.CommandContext(context.Background(), name, url).
		Start()
	//nolint:gosec // fixed opener; url is a localhost URL dockyard built
}

// containerByID finds a container in the containers view's last listing.
func (a *App) containerByID(id string) (docker.Container, bool) {
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	if cv == nil {
		return docker.Container{}, false
	}
	return cv.ByID(id)
}
