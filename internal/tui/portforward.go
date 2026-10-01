package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
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

// openPublished opens a container's published port in the browser: at
// once when it publishes one, after asking which when it publishes more.
// The URL is https for a TLS port and names the daemon's host when the
// daemon is remote.
func (a *App) openPublished(id string) tea.Cmd {
	c, ok := a.containerByID(id)
	if !ok {
		return nil
	}
	ports := docker.WebPorts(c)
	host := "localhost"
	if a.client != nil {
		host = docker.BrowseHost(a.client.Host)
	}
	switch len(ports) {
	case 0:
		a.errFlash = c.Name + " publishes no ports — <shift-f> forwards one"
		return nil
	case 1:
		return a.openURL(docker.PortURL(host, ports[0]))
	}
	a.promptDispatch = func(value string) (string, tea.Cmd) {
		n, err := strconv.Atoi(strings.TrimSpace(value))
		for _, p := range ports {
			if err == nil && int(p.Public) == n {
				return "", a.openURL(docker.PortURL(host, p))
			}
		}
		return fmt.Sprintf("%q is not one of %s's published ports: %s", value, c.Name, docker.PortChoices(ports)), nil
	}
	cmd := a.prompt.Open(strconv.Itoa(int(ports[0].Public)), chrome.OpenOpts{
		Prompt:      fmt.Sprintf("🌐 open %s port (%s) ", c.Name, docker.PortChoices(ports)),
		Placeholder: strconv.Itoa(int(ports[0].Public)),
	})
	a.resizeActiveView()
	return cmd
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
