package views

import (
	"encoding/json"
	"runtime"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
)

// EditApply is a submitted edit form: which container, what to set.
type EditApply struct {
	ID   string          `json:"id"`
	Spec docker.EditSpec `json:"spec"`
}

// EditFormView changes a running container's name, CPU and memory limits
// and restart policy — docker update and docker rename — without
// recreating it. A drill-in from the containers view.
type EditFormView struct {
	id string
	form
}

// NewEditForm builds the edit form for container id, prefilled with its
// current settings.
func NewEditForm(id string, cur docker.EditState) *EditFormView {
	v := &EditFormView{id: id}
	in := func(value string, limit int) formField { return formField{input: newFormInput(value, limit)} }
	name := in(cur.Name, 128)
	name.key, name.label = "name", "Name"
	name.input.SetWidth(28)
	cpus := in(docker.CPUsText(cur.NanoCPUs), 6)
	cpus.key, cpus.label, cpus.hint = "cpus", "CPUs", "e.g. 1.5 — this machine has "+strconv.Itoa(runtime.NumCPU())
	mem := in(docker.MemoryText(cur.Memory), 10)
	mem.key, mem.label, mem.hint = "memory", "Memory", "e.g. 512m, 2g"
	if cur.NanoCPUs == 0 {
		cpus.hint = "unlimited now; " + cpus.hint
	}
	if cur.Memory == 0 {
		mem.hint = "unlimited now; " + mem.hint
	}
	restart := formField{key: "restart", label: "Restart", hint: "←/→ to change", choices: docker.RestartPolicies}
	for i, p := range docker.RestartPolicies {
		if p == cur.Restart {
			restart.choice = i
		}
	}
	v.fields = []formField{name, cpus, mem, restart}
	v.valueWidth = 34
	v.focusField(0)
	return v
}

// CapturesInput reports that the form takes every typed key.
func (v *EditFormView) CapturesInput() bool { return true }

// Title names the container, for the border title.
func (v *EditFormView) Title() string { return "edit " + v.field("name").input.Value() }

// Init has nothing to start.
func (v *EditFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *EditFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *EditFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *EditFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *EditFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *EditFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *EditFormView) Refresh() tea.Cmd { return nil }

// SetError shows a failed edit in the form, to be fixed.
func (v *EditFormView) SetError(msg string) { v.err = msg }

// View renders the form.
func (v *EditFormView) View() string {
	return v.render("enter applies it — no restart · tab next field · esc cancel")
}

// Spec reads the fields.
func (v *EditFormView) Spec() docker.EditSpec {
	f := func(key string) string { return v.field(key).value() }
	return docker.EditSpec{Name: f("name"), CPUs: f("cpus"), Memory: f("memory"), Restart: f("restart")}
}

// HandleKey submits on enter. The spec is read the way the update is
// built, so a bad size or CPU count is reported in the form.
func (v *EditFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	s := v.Spec()
	if s.Name != "" && !validContainerName.MatchString(s.Name) {
		v.err = "name: letters, digits, _ . - only, starting with a letter or digit"
		return "", ""
	}
	if _, _, _, err := docker.EditUpdate(docker.EditState{}, docker.EditSpec{CPUs: s.CPUs, Memory: s.Memory}); err != nil {
		v.err = err.Error()
		return "", ""
	}
	b, err := json.Marshal(EditApply{ID: v.id, Spec: s})
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	return "edit_apply", string(b)
}

// DecodeEditApply reads a submitted edit back.
func DecodeEditApply(s string) (EditApply, error) {
	var e EditApply
	err := json.Unmarshal([]byte(s), &e)
	return e, err
}
