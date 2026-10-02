// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// editStateMsg carries a container's current settings, to open the form.
type editStateMsg struct {
	err   error
	id    string
	state docker.EditState
}

// editDoneMsg reports an applied edit, or why it failed.
type editDoneMsg struct {
	err  error
	name string
}

// loadEditForm reads the container's settings; the form opens when they
// arrive, so every field starts at what the container has now.
func (a *App) loadEditForm(id string) tea.Cmd {
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		s, err := client.EditState(ctx, id)
		return editStateMsg{id: id, state: s, err: err}
	}
}

func (a *App) handleEditState(msg editStateMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.errFlash = docker.FormatUserError(msg.err).Error()
		return a, nil
	}
	a.setView(style.ViewEditForm, views.NewEditForm(msg.id, msg.state))
	a.pushView(style.ViewEditForm)
	return a, nil
}

// applyEdit runs docker update / rename in the background; the form stays
// up until it is done, so a refusal lands in it.
func (a *App) applyEdit(e views.EditApply) tea.Cmd {
	a.flash = "updating…"
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		return editDoneMsg{name: e.Spec.Name, err: client.Edit(ctx, e.ID, e.Spec)}
	}
}

func (a *App) handleEditDone(msg editDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		err := docker.FormatUserError(msg.err).Error()
		if f := typedView[*views.EditFormView](a, style.ViewEditForm); f != nil && a.view == style.ViewEditForm {
			f.SetError(err)
		} else {
			a.errFlash = err
		}
		return a, nil
	}
	if a.view == style.ViewEditForm {
		a.popView()
	}
	a.flash = "updated " + msg.name + " — no restart needed"
	return a, a.refreshActiveView()
}
