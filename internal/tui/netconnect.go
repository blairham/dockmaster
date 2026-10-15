// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// The network actions more than one place names.
const (
	actConfirmDisconnect = views.ActConfirmDisconnect
	actDisconnect        = "disconnect_network"
)

// netFormMsg carries the daemon's networks, to open the picker (#107).
type netFormMsg struct {
	err      error
	targets  []views.NetTarget
	networks []string
}

// netDoneMsg reports a connect or disconnect: what was done, to how many,
// and every failure, one per container.
type netDoneMsg struct {
	err     error
	verb    string
	subject string
}

// loadNetworkForm lists the networks; the picker opens when they arrive,
// so it never offers one that is gone.
func (a *App) loadNetworkForm(param string) tea.Cmd {
	targets, err := views.DecodeNetTargets(param)
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		list, err := client.Networks(ctx)
		names := make([]string, 0, len(list))
		for _, n := range list {
			names = append(names, n.Name)
		}
		return netFormMsg{targets: targets, networks: names, err: err}
	}
}

func (a *App) handleNetForm(msg netFormMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.errFlash = docker.FormatUserError(msg.err).Error()
		return a, nil
	}
	a.setView(style.ViewNetPicker, views.NewNetworkPicker(msg.targets, msg.networks))
	a.pushView(style.ViewNetPicker)
	return a, nil
}

// connectNetwork runs the picker's connect in the background; the form
// stays up until it is done, so a refusal lands in it.
func (a *App) connectNetwork(param string) tea.Cmd {
	c, err := views.DecodeNetChange(param)
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	if len(c.Targets) == 0 {
		a.errFlash = "already on " + c.Network
		return nil
	}
	a.flash = "connecting…"
	client := a.client
	return func() tea.Msg {
		return netChange(c, "connected", "to", func(ctx context.Context, t views.NetTarget) error {
			return client.ConnectNetwork(ctx, c.Network, docker.NetworkAttach{
				Container: t.ID, Aliases: c.Aliases, IP: c.IP,
			})
		}, client)
	}
}

// confirmDisconnect asks before a disconnect, naming the containers and
// the network: it can cut a container off from everything it talks to.
func (a *App) confirmDisconnect(param string) {
	c, err := views.DecodeNetChange(param)
	if err != nil || len(c.Targets) == 0 {
		a.errFlash = "nothing to disconnect"
		return
	}
	a.openConfirm(actDisconnect, param, fmt.Sprintf(
		"disconnect %s from %s? it loses every address and route that network gave it",
		targetNames(c.Targets), c.Network,
	))
}

// disconnectNetwork runs a confirmed disconnect.
func (a *App) disconnectNetwork(param string) tea.Cmd {
	c, err := views.DecodeNetChange(param)
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	client := a.client
	return func() tea.Msg {
		return netChange(c, "disconnected", "from", func(ctx context.Context, t views.NetTarget) error {
			return client.DisconnectNetwork(ctx, c.Network, t.ID)
		}, client)
	}
}

// netChange applies fn to every target, collecting failures rather than
// stopping at the first: connecting four of five and naming the one that
// refused is more use than connecting two and bailing.
func netChange(
	c views.NetChange, verb, prep string,
	fn func(context.Context, views.NetTarget) error, client *docker.Client,
) netDoneMsg {
	ctx, cancel := client.RequestContext(actionTimeout)
	defer cancel()
	var errs []error
	for _, t := range c.Targets {
		if err := fn(ctx, t); err != nil {
			errs = append(errs, docker.FormatUserError(err))
		}
	}
	return netDoneMsg{
		verb:    verb,
		subject: targetNames(c.Targets) + " " + prep + " " + c.Network,
		err:     errors.Join(errs...),
	}
}

func (a *App) handleNetDone(msg netDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		err := strings.ReplaceAll(msg.err.Error(), "\n", "; ")
		if f := typedView[*views.NetworkPickerView](a, style.ViewNetPicker); f != nil && a.view == style.ViewNetPicker {
			f.SetError(err)
		} else {
			a.errFlash = err
		}
		return a, nil
	}
	if a.view == style.ViewNetPicker {
		a.popView()
	}
	// As a bulk key does, a change made to the marked rows clears them.
	if cv := typedView[*views.ContainersView](a, style.ViewContainers); cv != nil {
		cv.ClearMarks()
	}
	a.flash = msg.verb + " " + msg.subject
	cmd := a.refreshActiveView()
	return a, cmd
}

// targetNames names the containers for a question or a flash, the first
// few by name.
func targetNames(ts []views.NetTarget) string {
	const show = 4
	names := make([]string, 0, show)
	for i, t := range ts {
		if i == show {
			names = append(names, fmt.Sprintf("and %d more", len(ts)-show))
			break
		}
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}
