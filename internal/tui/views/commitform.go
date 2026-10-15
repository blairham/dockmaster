// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The commit form's pause choices: docker commit pauses by default.
const (
	commitPause   = "pause it (as docker commit)"
	commitNoPause = "leave it running"

	commitChoiceHint = "←/→ to change"
)

// CommitSpec is a submitted commit: which container, the reference to tag
// the image with (empty leaves it untagged), a message, and whether to
// pause the container while it is read.
type CommitSpec struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Ref     string `json:"ref"`
	Message string `json:"message"`
	Pause   bool   `json:"pause"`
}

// CommitFormView is `docker commit` for one container: an image from its
// current filesystem. A drill-in from the containers view.
type CommitFormView struct {
	id, name string
	form
}

// NewCommitForm builds the commit form for container id named name.
func NewCommitForm(id, name string) *CommitFormView {
	v := &CommitFormView{id: id, name: name}
	v.valueWidth = runInputWidth + 4
	in := func(value string, limit int) formField {
		f := formField{input: newFormInput(value, limit)}
		f.input.SetWidth(runInputWidth)
		return f
	}
	ref, msg := in("", 256), in("", 1024)
	ref.key, ref.label, ref.hint = "ref", "Tag as", "repo[:tag]; empty leaves it untagged"
	msg.key, msg.label, msg.hint = "message", "Message", "optional, as docker commit -m"
	v.fields = []formField{
		ref,
		msg,
		{key: "pause", label: "Pause", hint: commitChoiceHint, choices: []string{commitPause, commitNoPause}},
	}
	v.focusField(0)
	return v
}

// CapturesInput reports that the form takes every typed key.
func (v *CommitFormView) CapturesInput() bool { return true }

// Title names the container, for the border title.
func (v *CommitFormView) Title() string { return "commit " + v.name }

// Init has nothing to start.
func (v *CommitFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *CommitFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *CommitFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *CommitFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *CommitFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *CommitFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *CommitFormView) Refresh() tea.Cmd { return nil }

// SetError shows a failed commit in the form, to be fixed.
func (v *CommitFormView) SetError(msg string) { v.err = msg }

// View renders the form.
func (v *CommitFormView) View() string {
	return v.render("enter commits · tab next field · esc cancel")
}

// Spec reads the fields.
func (v *CommitFormView) Spec() CommitSpec {
	return CommitSpec{
		ID:      v.id,
		Name:    v.name,
		Ref:     strings.TrimSpace(v.field("ref").value()),
		Message: strings.TrimSpace(v.field("message").value()),
		Pause:   v.field("pause").value() == commitPause,
	}
}

// HandleKey submits on enter. A bad reference is the daemon client's to
// refuse, and the refusal lands back in the form.
func (v *CommitFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	s := v.Spec()
	if strings.ContainsAny(s.Ref, " \t") {
		v.err = "tag as: a reference has no spaces"
		return "", ""
	}
	b, err := json.Marshal(s)
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	return "commit_container", string(b)
}

// DecodeCommitSpec reads a submitted commit back.
func DecodeCommitSpec(s string) (CommitSpec, error) {
	var spec CommitSpec
	err := json.Unmarshal([]byte(s), &spec)
	return spec, err
}
