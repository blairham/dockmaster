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

// Build form field keys.
const (
	keyBuildContext    = "context"
	keyBuildDockerfile = "dockerfile"
	keyBuildTag        = "tag"
	keyBuildTarget     = "target"
	keyBuildArgs       = "args"
	keyBuildPush       = "push"
)

// buildInputWidth is the build form's input width: a context directory is
// a path, and build args a list.
const buildInputWidth = 48

// BuildFormView is `docker build` for one directory (#104): its tag,
// Dockerfile, target and build args. It runs nothing itself — enter
// returns the spec and the app runs the build into an output view.
type BuildFormView struct {
	form
}

// NewBuildForm builds the form for the context directory dir, its
// Dockerfile prefilled with dockerfile ("" for the context's own).
func NewBuildForm(dir, dockerfile string) *BuildFormView {
	v := &BuildFormView{}
	v.valueWidth = buildInputWidth + 4
	input := func(value string, limit int) textinput.Model {
		in := newFormInput(value, limit)
		in.SetWidth(buildInputWidth)
		return in
	}
	v.fields = []formField{
		{key: keyBuildContext, label: "Context", hint: "a directory", input: input(dir, 1024)},
		{key: keyBuildDockerfile, label: "Dockerfile", hint: "relative to the context", input: input(dockerfile, 1024)},
		{key: keyBuildTag, label: "Tag", hint: "optional; app:dev", input: input("", 256)},
		{key: keyBuildTarget, label: "Target", hint: "optional; a stage name", input: input("", 128)},
		{key: keyBuildArgs, label: "Build args", hint: "KEY=value …", input: input("", 2048)},
		{
			key: keyBuildPush, label: "Push local", hint: "then push the tag to the local registry",
			choices: []string{"no", choiceYes},
		},
	}
	v.labelWidth = 12
	// The tag is what is typed nearly every time; start there.
	v.focusField(2)
	return v
}

// CapturesInput reports that the form takes every typed key.
func (v *BuildFormView) CapturesInput() bool { return true }

// Title is the form's subject, for the border title.
func (v *BuildFormView) Title() string { return "build " + v.field(keyBuildContext).value() }

// Init has nothing to start.
func (v *BuildFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *BuildFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *BuildFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *BuildFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *BuildFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *BuildFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *BuildFormView) Refresh() tea.Cmd { return nil }

// View renders the form.
func (v *BuildFormView) View() string {
	return v.render("enter builds it · tab next field · esc cancel")
}

// Spec reads the fields: build args are space-separated, as typed after
// --build-arg.
func (v *BuildFormView) Spec() docker.BuildSpec {
	f := func(key string) string { return strings.TrimSpace(v.field(key).value()) }
	return docker.BuildSpec{
		Context:    f(keyBuildContext),
		Dockerfile: f(keyBuildDockerfile),
		Tag:        f(keyBuildTag),
		Target:     f(keyBuildTarget),
		BuildArgs:  strings.Fields(f(keyBuildArgs)),
	}
}

// PushLocal reports the form's "push to the local registry" choice (#120).
func (v *BuildFormView) PushLocal() bool { return v.field(keyBuildPush).value() == choiceYes }

// buildRequest is what the form submits: the spec, and whether to push the
// tag to the local registry after it builds.
type buildRequest struct {
	docker.BuildSpec
	PushLocal bool `json:"pushLocal,omitempty"`
}

// HandleKey submits on enter. The spec is checked first, so a context
// that is not a directory, a missing Dockerfile or a malformed build arg
// is reported here, in the form, rather than as a failed build; so is a
// push to local with no tag to push.
func (v *BuildFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	spec := v.Spec()
	spec.Context = ResolvePath(spec.Context)
	if err := spec.Check(); err != nil {
		v.err = err.Error()
		return "", ""
	}
	if v.PushLocal() && spec.Tag == "" {
		v.err = "push local: a tag is required — it is what is pushed"
		return "", ""
	}
	b, err := json.Marshal(buildRequest{BuildSpec: spec, PushLocal: v.PushLocal()})
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	return "build_run", string(b)
}

// SetError shows a failure in the form, to be fixed.
func (v *BuildFormView) SetError(msg string) { v.err = msg }

// DecodeBuildSpec reads a submitted build form back: the spec, and whether
// to push its tag to the local registry once it has built.
func DecodeBuildSpec(s string) (docker.BuildSpec, bool, error) {
	var req buildRequest
	err := json.Unmarshal([]byte(s), &req)
	return req.BuildSpec, req.PushLocal, err
}
