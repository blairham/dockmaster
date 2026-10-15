// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// What a tar form writes.
const (
	TarExport = "export" // a container's filesystem, as docker export
	TarSave   = "save"   // images with their layers and tags, as docker save
)

// TarSpec is a submitted export or save: what to write (one container,
// or one or more images) and the local path to write it to.
type TarSpec struct {
	Kind string `json:"kind"`
	// Name is what the flash and the confirm call it.
	Name string `json:"name"`
	Path string `json:"path"`
	// IDs are what the daemon is asked for: a container ID, or image
	// references (an ID for an untagged one).
	IDs []string `json:"ids"`
}

// TarFormView asks where to write a tar: a container's export or an
// image save. A drill-in from the containers or images view.
type TarFormView struct {
	spec TarSpec
	form
}

// NewTarForm builds the form for spec, its path prefilled.
func NewTarForm(spec TarSpec) *TarFormView {
	v := &TarFormView{spec: spec}
	v.valueWidth = runInputWidth + 4
	path := formField{input: newFormInput(spec.Path, 1024)}
	path.input.SetWidth(runInputWidth)
	path.key, path.label, path.hint = "path", "File", "a .tar here; ~ and relative paths work"
	v.fields = []formField{path}
	v.focusField(0)
	return v
}

// CapturesInput reports that the form takes every typed key.
func (v *TarFormView) CapturesInput() bool { return true }

// Title says what is written, for the border title.
func (v *TarFormView) Title() string { return v.spec.Kind + " " + v.spec.Name }

// Init has nothing to start.
func (v *TarFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *TarFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *TarFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *TarFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *TarFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *TarFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *TarFormView) Refresh() tea.Cmd { return nil }

// SetError shows a failed write in the form, to be fixed.
func (v *TarFormView) SetError(msg string) { v.err = msg }

// View renders the form.
func (v *TarFormView) View() string {
	verb := "exports the filesystem"
	if v.spec.Kind == TarSave {
		verb = "saves"
	}
	return v.render("enter " + verb + " · an existing file is replaced only after a yes · esc cancel")
}

// Spec reads the path into the spec.
func (v *TarFormView) Spec() TarSpec {
	s := v.spec
	s.Path = strings.TrimSpace(v.field("path").value())
	return s
}

// HandleKey submits on enter. Neither export nor save changes the daemon,
// so neither is in mutating; overwriting a file is confirmed by the app.
func (v *TarFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	s := v.Spec()
	if s.Path == "" {
		v.err = "file: where should it go?"
		return "", ""
	}
	b, err := json.Marshal(s)
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	return "write_tar", string(b)
}

// DecodeTarSpec reads a submitted export or save back.
func DecodeTarSpec(s string) (TarSpec, error) {
	var spec TarSpec
	err := json.Unmarshal([]byte(s), &spec)
	return spec, err
}
