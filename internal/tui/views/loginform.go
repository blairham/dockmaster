// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
)

// LoginFormView is `docker login`: a registry, a username and a password
// (masked). Submitting returns registry_login with no param — the app reads
// the credentials from the form itself, so the password never travels in
// an action's param, which other code may show or log.
type LoginFormView struct {
	form
}

// NewLoginForm builds the login form for server; empty means Docker Hub.
func NewLoginForm(server string) *LoginFormView {
	v := &LoginFormView{}
	v.valueWidth = runInputWidth + 4
	in := func(value string, limit int) formField {
		f := formField{input: newFormInput(value, limit)}
		f.input.SetWidth(runInputWidth)
		return f
	}
	if server == "" {
		server = "docker.io"
	}
	reg, user, pass := in(server, 256), in("", 256), in("", 4096)
	reg.key, reg.label, reg.hint = "registry", "Registry", "docker.io, ghcr.io, localhost:5001…"
	user.key, user.label = "username", "Username"
	pass.key, pass.label, pass.hint = "password", "Password", "or an access token"
	pass.input.EchoMode = textinput.EchoPassword
	pass.input.EchoCharacter = '•'
	v.fields = []formField{reg, user, pass}
	v.focusField(1)
	return v
}

// CapturesInput reports that the form takes every typed key.
func (v *LoginFormView) CapturesInput() bool { return true }

// Title names the registry, for the border title.
func (v *LoginFormView) Title() string {
	return "login " + docker.NormalizeRegistry(v.field("registry").value())
}

// Init has nothing to start.
func (v *LoginFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *LoginFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *LoginFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *LoginFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *LoginFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *LoginFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *LoginFormView) Refresh() tea.Cmd { return nil }

// SetError shows a refused login in the form, to be fixed.
func (v *LoginFormView) SetError(msg string) { v.err = msg }

// View renders the form.
func (v *LoginFormView) View() string {
	return v.render("enter checks it with the registry, then stores it as docker login does · tab next field · esc cancel")
}

// Credentials reads the fields. The password is taken as typed — spaces
// included — since it is the registry's to judge.
func (v *LoginFormView) Credentials() (server, username, password string) {
	return v.field("registry").value(), v.field("username").value(), v.field("password").input.Value()
}

// HandleKey submits on enter, once every field is filled.
func (v *LoginFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	_, user, pass := v.Credentials()
	switch {
	case user == "":
		v.err = "username: required"
		v.focusField(v.indexOf("username"))
		return "", ""
	case pass == "":
		v.err = "password: required"
		v.focusField(v.indexOf("password"))
		return "", ""
	}
	return "registry_login", ""
}
