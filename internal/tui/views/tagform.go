// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
)

// TagFormView is `docker tag` for one image: the new `repo[:tag]`. A
// drill-in from the images view.
type TagFormView struct {
	source string
	form
}

// NewTagForm builds the tag form for source (a reference, or an ID for an
// untagged image), prefilled with prefill.
func NewTagForm(source, prefill string) *TagFormView {
	v := &TagFormView{source: source}
	v.valueWidth = runInputWidth + 4
	ref := formField{input: newFormInput(prefill, 256)}
	ref.input.SetWidth(runInputWidth)
	ref.key, ref.label, ref.hint = "ref", "Tag as", "repo[:tag], e.g. ghcr.io/me/app:v1"
	v.fields = []formField{ref}
	v.focusField(0)
	v.fields[0].input.CursorEnd()
	return v
}

// CapturesInput reports that the form takes every typed key.
func (v *TagFormView) CapturesInput() bool { return true }

// Title names the image, for the border title.
func (v *TagFormView) Title() string { return "tag " + v.source }

// Init has nothing to start.
func (v *TagFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *TagFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *TagFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *TagFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *TagFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *TagFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *TagFormView) Refresh() tea.Cmd { return nil }

// SetError shows a refused tag in the form, to be fixed.
func (v *TagFormView) SetError(msg string) { v.err = msg }

// View renders the form.
func (v *TagFormView) View() string {
	return v.render("enter tags it · esc cancel")
}

// HandleKey submits on enter. The reference is checked the way the daemon
// client will check it, so a bad one is reported here.
func (v *TagFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	target := v.field("ref").value()
	if _, _, err := docker.ParseTag(target); err != nil {
		v.err = err.Error()
		return "", ""
	}
	return ActTagImage, v.source + "\x00" + target
}

// ActTagImage tags an image; its param is the source, NUL, the target.
const ActTagImage = "tag_image"

// ActPushImage asks to push an image reference (it confirms).
const ActPushImage = "confirm_push_image"
