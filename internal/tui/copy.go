// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// copyDoneMsg reports a finished copy, or why it failed.
type copyDoneMsg struct {
	err  error
	spec views.CopySpec
}

// openCopyForm opens docker cp for a container.
func (a *App) openCopyForm(id string) (tea.Model, tea.Cmd) {
	a.setView(style.ViewCopyForm, views.NewCopyForm(id, a.containerName(id)))
	a.pushView(style.ViewCopyForm)
	return a, nil
}

// runCopy copies in the background; the form stays up until it is done,
// so a bad path lands in it to be fixed.
func (a *App) runCopy(spec views.CopySpec) tea.Cmd {
	a.flash = "copying…"
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		var err error
		if spec.Into {
			err = client.CopyInto(ctx, spec.ID, spec.Local, spec.Container)
		} else {
			err = client.CopyFrom(ctx, spec.ID, spec.Container, spec.Local)
		}
		return copyDoneMsg{spec: spec, err: err}
	}
}

// handleCopyDone reports the copy: in the form when it failed, and on
// success back on the containers view with where it went.
func (a *App) handleCopyDone(msg copyDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		err := docker.FormatUserError(msg.err).Error()
		if f := typedView[*views.CopyFormView](a, style.ViewCopyForm); f != nil && a.view == style.ViewCopyForm {
			f.SetError(err)
		} else {
			a.errFlash = err
		}
		return a, nil
	}
	if a.view == style.ViewCopyForm {
		a.popView()
	}
	s := msg.spec
	if s.Into {
		a.flash = "copied " + s.Local + " → " + s.Name + ":" + s.Container
	} else {
		local, err := docker.LocalPath(s.Local)
		if err != nil {
			local = s.Local
		}
		a.flash = "copied " + s.Name + ":" + s.Container + " → " + local
	}
	return a, nil
}
