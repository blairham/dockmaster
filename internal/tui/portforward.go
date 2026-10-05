// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

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

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// forwardTimeout bounds starting a forward: the first one pulls the helper
// image.
const forwardTimeout = 2 * time.Minute

// forwardStartedMsg reports a forward starting, or why it did not.
type forwardStartedMsg struct {
	err error
	pf  docker.PortForward
}

// promptForward forwards ports to a container. Its labels may preset the
// prompt (docker.LabelPortForwards) or replace it with a confirm
// (docker.LabelAutoPortForwards), k9s's FastForwards; without them the
// prompt is prefilled with its first TCP port. A label that does not parse
// is reported in the prompt, which opens as if the label were not there:
// a container's labels are its author's, and they never start a forward
// the user did not see and agree to.
func (a *App) promptForward(id string) tea.Cmd {
	c, ok := a.containerByID(id)
	if !ok {
		return nil
	}
	specs, auto, labelErr := docker.LabelForwards(c)
	if labelErr == nil && auto {
		a.confirmLabelForwards(c, specs)
		return nil
	}
	prefill := docker.DefaultForwardSpec(c)
	if labelErr == nil && specs != nil {
		prefill = docker.JoinForwardSpecs(specs)
	}
	a.promptDispatch = func(value string) (string, tea.Cmd) {
		specs, err := docker.ParseForwardSpecs(value)
		if err != nil {
			return err.Error(), nil
		}
		return "", a.startForwards(c, specs)
	}
	cmd := a.prompt.Open(prefill, chrome.OpenOpts{
		Prompt:      fmt.Sprintf("⇄ forward %s%s  local:container ", c.Name, a.forwardExposure()),
		Placeholder: "8080:80",
	})
	if labelErr != nil {
		a.prompt.SetError(labelErr.Error())
	}
	a.resizeActiveView()
	return cmd
}

// forwardExposure is what the prompt and the confirm add when a forward
// would not be on loopback: the address, and that the network can reach it.
func (a *App) forwardExposure() string {
	if docker.ForwardIsLocal(a.forwardAddr) {
		return ""
	}
	return fmt.Sprintf(" on %s ⚠ reachable from the network", a.forwardAddr)
}

// confirmLabelForwards asks before starting the forwards a container's
// auto-port-forwards label names — the label skips typing them, never the
// user's say-so.
func (a *App) confirmLabelForwards(c docker.Container, specs []docker.ForwardSpec) {
	where := a.forwardExposure()
	if where == "" {
		where = " on localhost"
	}
	a.confirmDispatch = func(yes bool) (string, tea.Cmd) {
		if !yes {
			return "", nil
		}
		return "", a.startForwards(c, specs)
	}
	a.confirm.Open(fmt.Sprintf("forward %s %s%s, as its %s label asks?",
		c.Name, strings.ReplaceAll(docker.JoinForwardSpecs(specs), ",", " "), where, docker.LabelAutoPortForwards))
}

// startForwards starts each forward asked for, side by side.
func (a *App) startForwards(c docker.Container, specs []docker.ForwardSpec) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(specs))
	for _, s := range specs {
		cmds = append(cmds, a.startForward(c, s.Local, s.Remote))
	}
	if len(specs) > 1 {
		a.flash = fmt.Sprintf("forwarding %d ports to %s…", len(specs), c.Name)
	}
	return tea.Batch(cmds...)
}

func (a *App) startForward(c docker.Container, local, remote int) tea.Cmd {
	start := a.startForwardFn
	if start == nil {
		if a.client == nil {
			a.errFlash = "not connected to a docker daemon"
			return nil
		}
		start = a.client.StartPortForward
	}
	addr := a.forwardAddr
	listen := docker.PortForward{Address: addr, Local: local}.Listen()
	a.flash = fmt.Sprintf("forwarding %s → %s:%d…", listen, c.Name, remote)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), forwardTimeout)
		defer cancel()
		pf, err := start(ctx, c, addr, local, remote)
		return forwardStartedMsg{pf: pf, err: err}
	}
}

// showForwards opens :pf narrowed to one container's forwards (k9s's f),
// as a drill-in: esc goes back, and the narrowing lifts when it is left.
func (a *App) showForwards(param string) tea.Cmd {
	id, name, _ := strings.Cut(param, "\x00")
	pv := typedView[*views.PortForwardsView](a, style.ViewPortForwards)
	if pv == nil {
		return nil
	}
	pv.SetScope(id, name)
	a.pushView(style.ViewPortForwards)
	a.setActiveFilter("")
	return pv.Refresh()
}

// clearForwardScope lifts showForwards' narrowing.
func (a *App) clearForwardScope() {
	if pv := typedView[*views.PortForwardsView](a, style.ViewPortForwards); pv != nil {
		pv.ClearScope()
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
// outlast dockmaster and keep their ports. Best effort, bounded: quitting must
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
	//nolint:gosec // fixed opener; url is a localhost URL dockmaster built
}

// containerByID finds a container in the containers view's last listing.
func (a *App) containerByID(id string) (docker.Container, bool) {
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	if cv == nil {
		return docker.Container{}, false
	}
	return cv.ByID(id)
}
