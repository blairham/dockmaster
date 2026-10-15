// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"
)

// allServices is the service choice that builds every one.
const allServices = "(all)"

// labelService labels the field that picks a project's service.
const labelService = "Service"

// ComposeBuildSpec is a submitted compose build: a project, the service to
// build ("" for all of them), and whether to bring the project up after.
type ComposeBuildSpec struct {
	Project string `json:"project"`
	Service string `json:"service,omitempty"`
	Up      bool   `json:"up,omitempty"`
}

// ComposeBuildFormView is `compose build` for one project (#105): every
// service or one, picked as the scale form picks one, and whether to run
// `up -d` once it has built. It runs nothing itself.
type ComposeBuildFormView struct {
	project string
	form
}

// NewComposeBuildForm builds the form for project over its services.
func NewComposeBuildForm(project string, services []string) *ComposeBuildFormView {
	v := &ComposeBuildFormView{project: project}
	v.fields = []formField{
		{
			key: "service", label: labelService, hint: commitChoiceHint,
			choices: append([]string{allServices}, services...),
		},
		{key: "up", label: "Then up", hint: "compose up -d once it has built", choices: []string{"no", choiceYes}},
	}
	v.valueWidth = 34
	v.focusField(0)
	return v
}

// CapturesInput reports that the form takes every typed key.
func (v *ComposeBuildFormView) CapturesInput() bool { return true }

// Title names the project, for the border title.
func (v *ComposeBuildFormView) Title() string { return "build " + v.project }

// Init has nothing to start.
func (v *ComposeBuildFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *ComposeBuildFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *ComposeBuildFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *ComposeBuildFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *ComposeBuildFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *ComposeBuildFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *ComposeBuildFormView) Refresh() tea.Cmd { return nil }

// View renders the form.
func (v *ComposeBuildFormView) View() string {
	return v.render("enter builds · tab next field · esc cancel")
}

// HandleKey submits on enter.
func (v *ComposeBuildFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	sp := ComposeBuildSpec{Project: v.project, Up: v.field("up").value() == choiceYes}
	if svc := v.field("service").value(); svc != allServices {
		sp.Service = svc
	}
	b, err := json.Marshal(sp)
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	return "compose_build", string(b)
}

// DecodeComposeBuildSpec reads a submitted compose build back.
func DecodeComposeBuildSpec(s string) (ComposeBuildSpec, error) {
	var sp ComposeBuildSpec
	err := json.Unmarshal([]byte(s), &sp)
	return sp, err
}
