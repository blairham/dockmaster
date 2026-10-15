// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// The registry commands' names, as the palette, the aliases view and -c
// spell them.
const (
	cmdRegistries = "registries"
	cmdLogin      = "login"
)

// registryLoginFn checks credentials with the daemon and stores them; a
// var so tests log in without a registry.
var registryLoginFn = func(ctx context.Context, c *docker.Client, server, user, pass string) (string, error) {
	if c == nil {
		return docker.NormalizeRegistry(
				server,
			), errors.New(
				"not connected to a daemon — logging in asks it to check the credentials",
			)
	}
	return c.Login(ctx, server, user, pass)
}

// registryLogoutFn erases a stored credential; a var for tests.
var registryLogoutFn = docker.Logout

// registryLoginDoneMsg reports a login: the key it was filed under, or why
// it was refused.
type registryLoginDoneMsg struct {
	err    error
	server string
}

// registryLogin submits the login form. The credentials are read from the
// form here, never passed through an action param, and the password is
// held only by the command that sends it.
func (a *App) registryLogin() tea.Cmd {
	f := typedView[*views.LoginFormView](a, style.ViewLoginForm)
	if f == nil {
		return nil
	}
	server, user, pass := f.Credentials()
	a.flash = "logging in to " + docker.NormalizeRegistry(server) + "…"
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		key, err := registryLoginFn(ctx, client, server, user, pass)
		return registryLoginDoneMsg{server: key, err: err}
	}
}

// handleRegistryLoginDone closes the form on success and refreshes the
// registries view; a refusal lands in the form, to be fixed.
func (a *App) handleRegistryLoginDone(msg registryLoginDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		err := docker.FormatUserError(msg.err).Error()
		if f := typedView[*views.LoginFormView](a, style.ViewLoginForm); f != nil && a.view == style.ViewLoginForm {
			f.SetError(err)
			a.flash = ""
		} else {
			a.errFlash = err
		}
		return a, nil
	}
	if a.view == style.ViewLoginForm {
		a.popView()
	}
	a.flash = "logged in to " + msg.server
	if v := a.viewMap[style.ViewRegistries]; v != nil {
		return a, v.Refresh()
	}
	return a, nil
}

// registryLogout runs docker logout's work in the background and refreshes
// the registries view after.
func (a *App) registryLogout(server string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDoneMsg{
			verb: "logged out of", subject: server, err: registryLogoutFn(ctx, server),
			refresh: style.ViewRegistries, hasRefresh: true,
		}
	}
}
