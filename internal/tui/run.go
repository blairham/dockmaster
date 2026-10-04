// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// runDoneMsg reports a container started from the run form, or why not.
type runDoneMsg struct {
	err  error
	id   string
	spec docker.RunSpec
}

// openRunForm opens the run form for an image reference.
func (a *App) openRunForm(image string) (tea.Model, tea.Cmd) {
	a.setView(style.ViewRunForm, views.NewRunForm(image))
	a.pushView(style.ViewRunForm)
	return a, nil
}

// runImage starts the container the form describes. The form stays on
// screen until the daemon answers, so a failure lands in it to be fixed.
func (a *App) runImage(spec docker.RunSpec) tea.Cmd {
	a.flash = "starting " + spec.Image + "…"
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		id, err := client.Run(ctx, spec)
		return runDoneMsg{spec: spec, id: id, err: err}
	}
}

// handleRunDone shows a failure in the form, or on success goes where the
// new container is: the containers list, with its logs open.
func (a *App) handleRunDone(msg runDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		err := docker.FormatUserError(msg.err).Error()
		if f := typedView[*views.RunFormView](a, style.ViewRunForm); f != nil && a.view == style.ViewRunForm {
			f.SetError(err)
		} else {
			a.errFlash = err
		}
		return a, nil
	}
	name := msg.spec.Name
	if name == "" {
		name = msg.spec.Image
	}
	refresh := a.switchView(style.ViewContainers)
	open := a.openLogs(views.NewLogsView(a.client, msg.id, name))
	a.flash = "started " + name
	return a, tea.Batch(refresh, open)
}
