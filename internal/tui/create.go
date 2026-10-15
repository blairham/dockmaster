// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// createDoneMsg reports a volume or network created from the create form,
// or why not.
type createDoneMsg struct {
	err  error
	kind string // views.CreateVolume or views.CreateNetwork
	name string
}

// openCreateForm opens the volume or network form over its list view —
// switching to that view first when `:create` was typed elsewhere, so esc
// and success both land on the list the new row appears in.
func (a *App) openCreateForm(kind string) (tea.Model, tea.Cmd) {
	var (
		list style.ViewType
		form *views.CreateFormView
	)
	switch kind {
	case views.CreateVolume:
		list, form = style.ViewVolumes, views.NewVolumeForm()
	case views.CreateNetwork:
		swarm := a.client != nil && a.client.SwarmManager
		list, form = style.ViewNetworks, views.NewNetworkForm(swarm)
	default:
		a.errFlash = "create makes a volume or a network — not " + kind
		return a, nil
	}
	var cmd tea.Cmd
	if a.view != list {
		cmd = a.switchView(list)
	}
	a.setView(style.ViewCreateForm, form)
	a.pushView(style.ViewCreateForm)
	return a, cmd
}

// createVolume runs the create in the background; the form stays up until
// the daemon answers, so a refusal lands in it.
func (a *App) createVolume(spec docker.VolumeSpec) tea.Cmd {
	a.flash = "creating volume…"
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		name, err := client.CreateVolume(ctx, spec)
		return createDoneMsg{kind: views.CreateVolume, name: name, err: err}
	}
}

// createNetwork is createVolume for a network.
func (a *App) createNetwork(spec docker.NetworkSpec) tea.Cmd {
	a.flash = "creating network " + spec.Name + "…"
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		_, err := client.CreateNetwork(ctx, spec)
		return createDoneMsg{kind: views.CreateNetwork, name: spec.Name, err: err}
	}
}

// handleCreateDone shows a failure in the form, or on success returns to
// the list and refreshes it so the new row is there.
func (a *App) handleCreateDone(msg createDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		err := docker.FormatUserError(msg.err).Error()
		if f := typedView[*views.CreateFormView](a, style.ViewCreateForm); f != nil && a.view == style.ViewCreateForm {
			f.SetError(err)
		} else {
			a.errFlash = err
		}
		return a, nil
	}
	if a.view == style.ViewCreateForm {
		a.popView()
	}
	a.flash = "created " + msg.kind + " " + msg.name
	cmd := a.refreshActiveView()
	return a, cmd
}
