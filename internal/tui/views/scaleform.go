// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"encoding/json"
	"strconv"

	tea "charm.land/bubbletea/v2"
)

// ScaleSpec is a submitted scale form: a project's service, the count it
// is asked to have, and the count it had when the form opened — which is
// what decides whether the scale removes containers and has to confirm.
type ScaleSpec struct {
	Project  string `json:"project"`
	Service  string `json:"service"`
	Replicas int    `json:"replicas"`
	Current  int    `json:"current"`
}

// ScaleFormView scales one service of a compose project — k9s's scale
// dialog. It runs nothing itself: enter returns the spec, and the app
// confirms and runs it; esc abandons it.
type ScaleFormView struct {
	project  string
	replicas map[string]int
	form
}

// NewScaleForm builds the form for project, over its services and each
// one's current container count. The service field starts on service when
// it is one of them (xray's service node), else on the first; replicas
// starts at the chosen service's count and follows the choice.
func NewScaleForm(project string, services []string, replicas map[string]int, service string) *ScaleFormView {
	v := &ScaleFormView{project: project, replicas: replicas}
	svc := formField{key: "service", label: "Service", hint: "←/→ to change", choices: services}
	for i, s := range services {
		if s == service {
			svc.choice = i
		}
	}
	n := formField{key: "replicas", label: "Replicas", input: newFormInput("", 6)}
	v.fields = []formField{svc, n}
	v.onChoice = func(string) { v.prefill() }
	v.prefill()
	v.valueWidth = 34
	// The count is what is changed nearly every time; start there.
	v.focusField(1)
	return v
}

// prefill sets replicas to the chosen service's current count.
func (v *ScaleFormView) prefill() {
	v.field("replicas").input.SetValue(strconv.Itoa(v.replicas[v.field("service").value()]))
	v.field("replicas").hint = "now " + strconv.Itoa(v.replicas[v.field("service").value()])
}

// CapturesInput reports that the form takes every typed key.
func (v *ScaleFormView) CapturesInput() bool { return true }

// Title names the project, for the border title.
func (v *ScaleFormView) Title() string { return "scale " + v.project }

// Init has nothing to start.
func (v *ScaleFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *ScaleFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *ScaleFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *ScaleFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *ScaleFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *ScaleFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *ScaleFormView) Refresh() tea.Cmd { return nil }

// View renders the form.
func (v *ScaleFormView) View() string {
	return v.render("enter scales it · tab next field · esc cancel")
}

// HandleKey submits on enter. Replicas must be a whole number, 0 or more:
// anything else stays in the form, with the reason.
func (v *ScaleFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	n, ok := parseReplicas(v.field("replicas").value())
	if !ok {
		v.err = "replicas: a whole number, 0 or more"
		return "", ""
	}
	svc := v.field("service").value()
	b, err := json.Marshal(ScaleSpec{Project: v.project, Service: svc, Replicas: n, Current: v.replicas[svc]})
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	return "compose_scale", string(b)
}

// parseReplicas reads a count: digits only — no sign, no space inside —
// and small enough to be an int.
func parseReplicas(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// DecodeScaleSpec reads a submitted scale back.
func DecodeScaleSpec(s string) (ScaleSpec, error) {
	var sp ScaleSpec
	err := json.Unmarshal([]byte(s), &sp)
	return sp, err
}
