// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"encoding/json"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
)

// Builder form field keys.
const (
	keyBuilderName     = "name"
	keyBuilderDriver   = "driver"
	keyBuilderPlatform = "platform"
	keyBuilderEndpoint = "endpoint"
)

// BuilderFormView is `docker buildx create`: a name, a driver
// (docker-container by default), the platforms, and BuildKit's address for
// the remote driver. A drill-in from the builders view (ctrl-n).
type BuilderFormView struct {
	form
}

// NewBuilderForm builds the create-builder form.
func NewBuilderForm() *BuilderFormView {
	v := &BuilderFormView{}
	v.valueWidth = runInputWidth + 4
	v.fields = []formField{
		{key: keyBuilderName, label: labelName, hint: "optional; buildx picks one", input: createInput("", 64)},
		{key: keyBuilderDriver, label: labelDriver, choices: docker.BuilderDrivers},
		{
			key: keyBuilderPlatform, label: "Platform", hint: "optional, linux/amd64,linux/arm64",
			input: createInput("", 256),
		},
		{
			key: keyBuilderEndpoint, label: "Endpoint", hint: "remote only, tcp://buildkitd:1234",
			input: createInput("", 256), locked: true,
		},
	}
	v.onChoice = v.driverChanged
	v.focusField(0)
	return v
}

// driverChanged unlocks the endpoint for the remote driver only; the
// others build on the daemon on screen.
func (v *BuilderFormView) driverChanged(key string) {
	if key != keyBuilderDriver {
		return
	}
	ep := v.field(keyBuilderEndpoint)
	ep.locked = v.field(keyBuilderDriver).value() != docker.DriverRemote
	if ep.locked {
		ep.input.SetValue("")
	}
}

// CapturesInput reports that the form takes every typed key.
func (v *BuilderFormView) CapturesInput() bool { return true }

// Title is the border title.
func (v *BuilderFormView) Title() string { return "new builder" }

// Init has nothing to start.
func (v *BuilderFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *BuilderFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *BuilderFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *BuilderFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *BuilderFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *BuilderFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *BuilderFormView) Refresh() tea.Cmd { return nil }

// SetError shows a refused create in the form, to be fixed.
func (v *BuilderFormView) SetError(msg string) { v.err = msg }

// View renders the form.
func (v *BuilderFormView) View() string {
	return v.render("enter creates it (buildx create) · ←/→ driver · tab next field · esc cancel")
}

// Spec reads the form.
func (v *BuilderFormView) Spec() docker.BuilderSpec {
	f := func(key string) string { return v.field(key).value() }
	return docker.BuilderSpec{
		Name:     f(keyBuilderName),
		Driver:   f(keyBuilderDriver),
		Platform: f(keyBuilderPlatform),
		Endpoint: f(keyBuilderEndpoint),
	}
}

// HandleKey submits on enter, once buildx would accept the spec.
func (v *BuilderFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	s := v.Spec()
	if err := s.Check(); err != nil {
		v.err = err.Error()
		return "", ""
	}
	b, err := json.Marshal(s)
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	return ActBuilderCreate, string(b)
}

// DecodeBuilderSpec reads ActBuilderCreate's param back, checked again.
func DecodeBuilderSpec(param string) (docker.BuilderSpec, error) {
	var s docker.BuilderSpec
	if err := json.Unmarshal([]byte(param), &s); err != nil {
		return s, fmt.Errorf("builder spec: %w", err)
	}
	if err := s.Check(); err != nil {
		return s, fmt.Errorf("builder spec: %w", err)
	}
	return s, nil
}
