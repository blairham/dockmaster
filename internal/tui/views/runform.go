package views

import (
	"encoding/json"
	"regexp"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
)

// Run form field keys.
const (
	keyRunName    = "name"
	keyRunPorts   = "ports"
	keyRunEnv     = "env"
	keyRunVolumes = "volumes"
	keyRunCommand = "command"
	keyRunRemove  = "remove"
)

// runInputWidth is the run form's input width: ports, env and volumes are
// lists, and twelve cells was too few to see one.
const runInputWidth = 40

// RunFormView is `docker run -d` for one image: a name, ports, env,
// volumes, a command, and --rm. It is a drill-in from the images view.
type RunFormView struct {
	image string
	form
}

// NewRunForm builds the form for image (its reference, e.g. nginx:1.27).
func NewRunForm(image string) *RunFormView {
	v := &RunFormView{image: image}
	v.valueWidth = runInputWidth + 4
	input := func(limit int) textinput.Model {
		in := newFormInput("", limit)
		in.SetWidth(runInputWidth)
		return in
	}
	v.fields = []formField{
		{key: "image", label: "Image", input: newFormInput(image, 256), locked: true},
		{key: keyRunName, label: "Name", hint: "optional", input: input(64)},
		{key: keyRunPorts, label: "Ports", hint: "8080:80 127.0.0.1:9443:443", input: input(256)},
		{key: keyRunEnv, label: "Env", hint: "KEY=value …", input: input(1024)},
		{key: keyRunVolumes, label: "Volumes", hint: "/host:/path data:/data:ro", input: input(1024)},
		{key: keyRunCommand, label: "Command", hint: "optional; replaces the image's", input: input(512)},
		{key: keyRunRemove, label: "--rm", hint: "remove it when it exits", choices: []string{"no", "yes"}},
	}
	v.focusField(1)
	return v
}

// SetError shows a failure from the daemon in the form, to be fixed.
func (v *RunFormView) SetError(msg string) { v.err = msg }

// CapturesInput reports that the form takes every typed key.
func (v *RunFormView) CapturesInput() bool { return true }

// Title names the image, for the border title.
func (v *RunFormView) Title() string { return "run " + v.image }

// Init has nothing to start.
func (v *RunFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *RunFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *RunFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *RunFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *RunFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *RunFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *RunFormView) Refresh() tea.Cmd { return nil }

// View renders the form.
func (v *RunFormView) View() string {
	return v.render("enter runs it, detached · tab next field · esc cancel")
}

// validContainerName is what docker accepts as a container name.
var validContainerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// HandleKey submits on enter: the spec is checked the same way the daemon
// request is built, so a bad port or env entry is reported here, in the
// form, rather than as a create error.
func (v *RunFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	spec := v.Spec()
	if spec.Name != "" && !validContainerName.MatchString(spec.Name) {
		v.err = "name: letters, digits, _ . - only, starting with a letter or digit"
		return "", ""
	}
	if _, _, err := docker.RunConfig(spec); err != nil {
		v.err = err.Error()
		return "", ""
	}
	b, err := json.Marshal(spec)
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	return "run_create", string(b)
}

// Spec reads the fields: list fields are space-separated, as they would be
// typed after -p, -e and -v.
func (v *RunFormView) Spec() docker.RunSpec {
	f := func(key string) string { return v.field(key).value() }
	return docker.RunSpec{
		Image:   v.image,
		Name:    f(keyRunName),
		Ports:   strings.Fields(f(keyRunPorts)),
		Env:     strings.Fields(f(keyRunEnv)),
		Volumes: strings.Fields(f(keyRunVolumes)),
		Command: strings.Fields(f(keyRunCommand)),
		Remove:  f(keyRunRemove) == "yes",
	}
}

// DecodeRunSpec reads a submitted form back.
func DecodeRunSpec(s string) (docker.RunSpec, error) {
	var spec docker.RunSpec
	err := json.Unmarshal([]byte(s), &spec)
	return spec, err
}
