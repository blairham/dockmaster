// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"encoding/json"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
)

// Create form field keys.
const (
	keyCreateName       = "name"
	keyCreateDriver     = "driver"
	keyCreateOpts       = "opts"
	keyCreateLabels     = "labels"
	keyCreateSubnet     = "subnet"
	keyCreateGateway    = "gateway"
	keyCreateInternal   = "internal"
	keyCreateAttachable = "attachable"
)

// The create form's kinds, which are also the words after `:create`.
const (
	CreateVolume  = "volume"
	CreateNetwork = "network"
)

// ActionCreateForm is the action that opens the create form; its param is
// CreateVolume or CreateNetwork.
const ActionCreateForm = "create_form"

const (
	// driverOverlay is the one network driver attachable means anything for.
	driverOverlay = "overlay"
	choiceYes     = "yes"
	labelName     = "Name"
	labelDriver   = "Driver"
)

// CreateFormView is `docker volume create` or `docker network create`: a
// drill-in from the volumes or networks view (ctrl-n, or :create).
type CreateFormView struct {
	kind string
	form
}

// NewVolumeForm builds the volume form: name, driver, driver opts, labels.
func NewVolumeForm() *CreateFormView {
	v := &CreateFormView{kind: CreateVolume}
	v.valueWidth = runInputWidth + 4
	v.fields = []formField{
		{key: keyCreateName, label: labelName, hint: "optional; the daemon picks one", input: createInput("", 64)},
		{key: keyCreateDriver, label: labelDriver, input: createInput("local", 64)},
		{key: keyCreateOpts, label: "Opts", hint: "type=nfs o=addr=10.0.0.1 …", input: createInput("", 1024)},
		{key: keyCreateLabels, label: "Labels", hint: "key=value …", input: createInput("", 1024)},
	}
	v.focusField(0)
	return v
}

// NewNetworkForm builds the network form. overlay is offered only when the
// daemon manages a swarm, since it is refused anywhere else; attachable
// means something only for overlay, so it is locked off otherwise.
func NewNetworkForm(swarm bool) *CreateFormView {
	v := &CreateFormView{kind: CreateNetwork}
	v.valueWidth = runInputWidth + 4
	v.labelWidth = len("Attachable") + 2
	drivers := []string{"bridge"}
	if swarm {
		drivers = append(drivers, driverOverlay)
	}
	v.fields = []formField{
		{key: keyCreateName, label: labelName, input: createInput("", 64)},
		{key: keyCreateDriver, label: labelDriver, choices: drivers, locked: len(drivers) == 1},
		{key: keyCreateSubnet, label: "Subnet", hint: "optional, 172.30.0.0/16", input: createInput("", 64)},
		{key: keyCreateGateway, label: "Gateway", hint: "optional, in the subnet", input: createInput("", 64)},
		{key: keyCreateInternal, label: "Internal", hint: "no route out", choices: yesNo},
		{key: keyCreateAttachable, label: "Attachable", hint: "overlay only", choices: yesNo, locked: true},
		{key: keyCreateLabels, label: "Labels", hint: "key=value …", input: createInput("", 1024)},
	}
	v.onChoice = v.driverChanged
	v.focusField(0)
	return v
}

func createInput(value string, limit int) textinput.Model {
	in := newFormInput(value, limit)
	in.SetWidth(runInputWidth)
	return in
}

// driverChanged locks attachable off unless the driver is overlay.
func (v *CreateFormView) driverChanged(key string) {
	if key != keyCreateDriver {
		return
	}
	a := v.field(keyCreateAttachable)
	a.locked = v.field(keyCreateDriver).value() != driverOverlay
	if a.locked {
		a.choice = 0
	}
}

// Kind is CreateVolume or CreateNetwork.
func (v *CreateFormView) Kind() string { return v.kind }

// SetError shows a failure from the daemon in the form, to be fixed.
func (v *CreateFormView) SetError(msg string) { v.err = msg }

// CapturesInput reports that the form takes every typed key.
func (v *CreateFormView) CapturesInput() bool { return true }

// Title names what is being created, for the border title.
func (v *CreateFormView) Title() string { return "new " + v.kind }

// Init has nothing to start.
func (v *CreateFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *CreateFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *CreateFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *CreateFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *CreateFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *CreateFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *CreateFormView) Refresh() tea.Cmd { return nil }

// View renders the form.
func (v *CreateFormView) View() string {
	return v.render("enter creates it · tab next field · esc cancel")
}

// HandleKey submits on enter. The spec is checked by the same code that
// builds the daemon request, so a bad label or subnet is reported here.
func (v *CreateFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	var (
		spec   any
		action string
		err    error
	)
	if v.kind == CreateVolume {
		s := v.VolumeSpec()
		_, err = docker.VolumeCreateConfig(s)
		spec, action = s, "volume_create"
	} else {
		s := v.NetworkSpec()
		_, err = docker.NetworkCreateConfig(s)
		spec, action = s, "network_create"
	}
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	b, err := json.Marshal(spec)
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	return action, string(b)
}

// VolumeSpec reads the volume form; list fields are space-separated.
func (v *CreateFormView) VolumeSpec() docker.VolumeSpec {
	f := func(key string) string { return v.field(key).value() }
	return docker.VolumeSpec{
		Name:   f(keyCreateName),
		Driver: f(keyCreateDriver),
		Opts:   strings.Fields(f(keyCreateOpts)),
		Labels: strings.Fields(f(keyCreateLabels)),
	}
}

// NetworkSpec reads the network form.
func (v *CreateFormView) NetworkSpec() docker.NetworkSpec {
	f := func(key string) string { return v.field(key).value() }
	return docker.NetworkSpec{
		Name:       f(keyCreateName),
		Driver:     f(keyCreateDriver),
		Subnet:     f(keyCreateSubnet),
		Gateway:    f(keyCreateGateway),
		Labels:     strings.Fields(f(keyCreateLabels)),
		Internal:   f(keyCreateInternal) == choiceYes,
		Attachable: f(keyCreateAttachable) == choiceYes,
	}
}

// DecodeVolumeSpec reads a submitted volume form back.
func DecodeVolumeSpec(s string) (docker.VolumeSpec, error) {
	var spec docker.VolumeSpec
	err := json.Unmarshal([]byte(s), &spec)
	return spec, err
}

// DecodeNetworkSpec reads a submitted network form back.
func DecodeNetworkSpec(s string) (docker.NetworkSpec, error) {
	var spec docker.NetworkSpec
	err := json.Unmarshal([]byte(s), &spec)
	return spec, err
}
