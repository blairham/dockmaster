package views

import (
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Copy directions, as the form's first field offers them.
const (
	CopyOut = "container → here"
	CopyIn  = "here → container"
)

// CopySpec is a submitted copy: which container, which way, which paths.
type CopySpec struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Container string `json:"container"`
	Local     string `json:"local"`
	Into      bool   `json:"into"`
}

// CopyFormView is `docker cp` for one container, either way. A drill-in
// from the containers view.
type CopyFormView struct {
	id, name string
	form
}

// NewCopyForm builds the copy form for container id named name.
func NewCopyForm(id, name string) *CopyFormView {
	v := &CopyFormView{id: id, name: name}
	v.valueWidth = runInputWidth + 4
	in := func(value string) formField {
		f := formField{input: newFormInput(value, 1024)}
		f.input.SetWidth(runInputWidth)
		return f
	}
	cpath, local := in(""), in(".")
	cpath.key, cpath.label, cpath.hint = "container", "Container", "path inside it, e.g. /etc/nginx"
	local.key, local.label, local.hint = "local", "Local", "here; ~ and relative paths work"
	v.fields = []formField{
		{key: "direction", label: "Direction", hint: "←/→ to change", choices: []string{CopyOut, CopyIn}},
		cpath,
		local,
	}
	v.focusField(1)
	return v
}

// CapturesInput reports that the form takes every typed key.
func (v *CopyFormView) CapturesInput() bool { return true }

// Title names the container, for the border title.
func (v *CopyFormView) Title() string { return "copy " + v.name }

// Init has nothing to start.
func (v *CopyFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *CopyFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *CopyFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *CopyFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *CopyFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *CopyFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *CopyFormView) Refresh() tea.Cmd { return nil }

// SetError shows a failed copy in the form, to be fixed.
func (v *CopyFormView) SetError(msg string) { v.err = msg }

// View renders the form.
func (v *CopyFormView) View() string {
	return v.render("enter copies · ←/→ flips the direction · tab next field · esc cancel")
}

// Spec reads the fields.
func (v *CopyFormView) Spec() CopySpec {
	return CopySpec{
		ID: v.id, Name: v.name,
		Container: strings.TrimSpace(v.field("container").value()),
		Local:     strings.TrimSpace(v.field("local").value()),
		Into:      v.field("direction").value() == CopyIn,
	}
}

// HandleKey submits on enter: copy_into changes the container, so it is
// a separate action --readonly can refuse; copy_from only reads it.
func (v *CopyFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	s := v.Spec()
	switch {
	case s.Container == "":
		v.err = "container: which path inside it?"
		return "", ""
	case !strings.HasPrefix(s.Container, "/"):
		v.err = "container: an absolute path, starting with /"
		return "", ""
	case s.Local == "":
		v.err = "local: which path here?"
		return "", ""
	}
	b, err := json.Marshal(s)
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	if s.Into {
		return "copy_into", string(b)
	}
	return "copy_from", string(b)
}

// DecodeCopySpec reads a submitted copy back.
func DecodeCopySpec(s string) (CopySpec, error) {
	var spec CopySpec
	err := json.Unmarshal([]byte(s), &spec)
	return spec, err
}
