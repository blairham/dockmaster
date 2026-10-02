package views

import (
	"encoding/json"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/engines"
)

// InputCapturer is implemented by views that take typed input. While one
// is active the app hands it every key but esc and ctrl-c, so digits, `r`,
// `/` and `:` type into a field instead of switching views or opening bars.
type InputCapturer interface {
	CapturesInput() bool
}

// Field keys. Fields are found by key, not position: which fields a form
// has depends on the runtime it is for.
const (
	keyProvider = "provider"
	keyName     = "name"
	keyCPUs     = "cpus"
	keyMemory   = "memory"
	keyDisk     = "disk"
	keyRuntime  = "runtime"
	keyRootful  = "rootful"
	keyUserNet  = "usernet"
)

// New-machine defaults: colima's own, a reasonable small VM for any runtime.
const (
	defaultCPUs      = 2
	defaultMemoryGiB = 2
	defaultDiskGiB   = 100
)

var yesNo = []string{"no", "yes"}

type formField struct {
	key     string
	label   string
	unit    string
	hint    string
	choices []string
	input   textinput.Model
	choice  int
	locked  bool
}

func (f *formField) value() string {
	if f.choices != nil {
		return f.choices[f.choice]
	}
	return strings.TrimSpace(f.input.Value())
}

// RuntimeFormView creates a machine for a runtime, or changes an existing
// machine's resources. It is a drill-in from the runtimes view; esc
// abandons it.
type RuntimeFormView struct {
	form
	caps      map[string]engines.Caps
	creatable []string
	existing  []engines.Machine
	machine   engines.Machine
	editing   bool
}

// NewRuntimeCreateForm is the form for a new machine. creatable are the
// installed runtimes that can create machines, provider the one to start
// on (the row the user was on); existing is checked for name collisions
// within the chosen runtime.
func NewRuntimeCreateForm(provider string, creatable []engines.Provider, existing []engines.Machine) *RuntimeFormView {
	v := &RuntimeFormView{existing: existing, caps: map[string]engines.Caps{}}
	v.onChoice = func(key string) {
		if key == keyProvider {
			v.applyProviderFields()
		}
	}
	start := 0
	for i, p := range creatable {
		v.creatable = append(v.creatable, p.Name())
		v.caps[p.Name()] = p.Caps()
		if p.Name() == provider {
			start = i
		}
	}
	v.fields = []formField{
		{key: keyProvider, label: "Provider", hint: "←/→ to change", choices: v.creatable, choice: start},
		{key: keyName, label: "Name", hint: "letters, digits, - and _", input: newFormInput("", 32)},
		{key: keyCPUs, label: "CPUs", hint: cpuHint(), input: newFormInput(strconv.Itoa(defaultCPUs), 3)},
		{
			key:   keyMemory,
			label: "Memory",
			unit:  "GiB",
			hint:  memoryHint(),
			input: newFormInput(strconv.Itoa(defaultMemoryGiB), 6),
		},
		{
			key:   keyDisk,
			label: "Disk",
			unit:  "GiB",
			hint:  "can grow later, never shrink",
			input: newFormInput(strconv.Itoa(defaultDiskGiB), 5),
		},
	}
	if len(v.creatable) == 1 {
		v.fields[0].locked = true
		v.fields[0].hint = "the only runtime here that creates machines"
	}
	v.applyProviderFields()
	v.focusField(v.indexOf(keyName))
	return v
}

// NewRuntimeEditForm is the form for changing a machine's resources. Name
// and runtime are fixed: a machine cannot be renamed, and a different
// runtime is a different machine.
func NewRuntimeEditForm(m engines.Machine, caps engines.Caps) *RuntimeFormView {
	v := &RuntimeFormView{
		machine:   m,
		editing:   true,
		creatable: []string{m.Provider},
		caps:      map[string]engines.Caps{m.Provider: caps},
	}
	v.fields = []formField{
		{key: keyName, label: "Name", input: newFormInput(m.Name, 32), locked: true},
		{key: keyCPUs, label: "CPUs", hint: cpuHint(), input: newFormInput(strconv.Itoa(m.CPUs), 3)},
		{
			key:   keyMemory,
			label: "Memory",
			unit:  "GiB",
			hint:  memoryHint(),
			input: newFormInput(formatGiB(bytesGiB(m.Memory)), 6),
		},
		{
			key: keyDisk, label: "Disk", unit: "GiB", input: newFormInput(strconv.Itoa(currentDiskGiB(m)), 5),
			hint: fmt.Sprintf("at least %d — a disk cannot shrink", currentDiskGiB(m)),
		},
	}
	if m.Runtime != "" && len(caps.Runtimes) > 0 {
		v.fields = append(v.fields, formField{
			key: keyRuntime, label: "Runtime", hint: "fixed — changing it means a new machine",
			choices: []string{m.Runtime}, locked: true,
		})
	}
	if caps.Toggles {
		v.fields = append(v.fields, toggleFields(m.Rootful, m.UserNet)...)
	}
	v.focusField(v.indexOf(keyCPUs))
	return v
}

func toggleFields(rootful, usernet bool) []formField {
	return []formField{
		{
			key:     keyRootful,
			label:   "Rootful",
			hint:    "containers run as root in the VM",
			choices: yesNo,
			choice:  boolIndex(rootful),
		},
		{
			key:     keyUserNet,
			label:   "User net",
			hint:    "user-mode networking — for VPNs that break the default",
			choices: yesNo,
			choice:  boolIndex(usernet),
		},
	}
}

func boolIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}

// applyProviderFields fits the runtime-specific fields to the chosen
// runtime: a Runtime choice for colima, the mode toggles for podman. The
// common fields keep whatever was typed.
func (v *RuntimeFormView) applyProviderFields() {
	kept := v.fields[:0:0]
	for _, f := range v.fields {
		if f.key != keyRuntime && f.key != keyRootful && f.key != keyUserNet {
			kept = append(kept, f)
		}
	}
	caps := v.caps[v.provider()]
	if len(caps.Runtimes) > 0 {
		kept = append(kept, formField{key: keyRuntime, label: "Runtime", hint: "←/→ to change", choices: caps.Runtimes})
	}
	if caps.Toggles {
		kept = append(kept, toggleFields(false, false)...)
	}
	v.fields = kept
	if v.focus >= len(v.fields) {
		v.focus = 0
	}
}

// provider is the runtime the form is for.
func (v *RuntimeFormView) provider() string {
	if v.editing {
		return v.machine.Provider
	}
	if i := v.indexOf(keyProvider); i >= 0 {
		return v.fields[i].value()
	}
	return ""
}

// formInputWidth is every input box's width, so the brackets line up down
// the form whatever each field's own limit is; a longer name scrolls.
const formInputWidth = 12

func newFormInput(value string, limit int) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = limit
	in.SetValue(value)
	in.SetWidth(formInputWidth)
	return in
}

// hostMemoryGiB is this machine's memory in whole GiB, 0 when unknown.
// A var so tests can pin it.
var hostMemoryGiB = func() int { return int(engines.HostMemory() >> 30) }

func cpuHint() string { return fmt.Sprintf("this machine has %d", runtime.NumCPU()) }

// memoryHint tells the user how much there is to give, as the CPU hint does.
func memoryHint() string {
	if g := hostMemoryGiB(); g > 0 {
		return fmt.Sprintf("this machine has %d GiB", g)
	}
	return ""
}

func currentDiskGiB(m engines.Machine) int { return int(bytesGiB(m.Disk)) }

func bytesGiB(n int64) float64 { return float64(n) / (1 << 30) }

func formatGiB(g float64) string { return strconv.FormatFloat(g, 'f', -1, 64) }

// CapturesInput — the form takes every typed key.
func (v *RuntimeFormView) CapturesInput() bool { return true }

// Editing reports whether the form changes an existing machine.
func (v *RuntimeFormView) Editing() bool { return v.editing }

// Title names the subject in the border.
func (v *RuntimeFormView) Title() string {
	if v.editing {
		return "edit " + v.machine.Name
	}
	return "new machine"
}

// Init has nothing to load.
func (v *RuntimeFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *RuntimeFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *RuntimeFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *RuntimeFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *RuntimeFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *RuntimeFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *RuntimeFormView) Refresh() tea.Cmd { return nil }

// HandleKey submits on enter. Everything else is for UpdateTable.
func (v *RuntimeFormView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	spec, msg := v.validate()
	if msg != "" {
		v.err = msg
		return "", ""
	}
	if v.editing {
		return "runtime_apply", EncodeRuntimeSpec(spec)
	}
	return "runtime_create", EncodeRuntimeSpec(spec)
}

// machineName is what the runtimes accept as a machine name: it becomes a
// directory and, for colima, a docker context name.
var machineName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// validate reads the fields, or explains the first one that is wrong.
func (v *RuntimeFormView) validate() (RuntimeSpec, string) {
	spec := RuntimeSpec{Provider: v.provider(), Name: v.field(keyName).value()}
	if !v.editing {
		switch {
		case spec.Name == "":
			return spec, "Name: a machine needs a name"
		case !machineName.MatchString(spec.Name):
			return spec, "Name: letters, digits, - and _ only, starting with a letter or digit"
		}
		for _, m := range v.existing {
			if m.Provider == spec.Provider && strings.EqualFold(m.Name, spec.Name) {
				return spec, "Name: a " + m.Provider + " machine named " + m.Name + " already exists"
			}
		}
	}

	cpus, err := strconv.Atoi(v.field(keyCPUs).value())
	if err != nil || cpus < 1 {
		return spec, "CPUs: a whole number, 1 or more"
	}
	if n := runtime.NumCPU(); cpus > n {
		return spec, fmt.Sprintf("CPUs: this machine has %d", n)
	}

	mem, err := strconv.ParseFloat(v.field(keyMemory).value(), 64)
	if err != nil || mem < 0.5 {
		return spec, "Memory: GiB, at least 0.5"
	}
	if g := hostMemoryGiB(); g > 0 && mem > float64(g) {
		return spec, fmt.Sprintf("Memory: this machine has %d GiB", g)
	}

	disk, err := strconv.Atoi(v.field(keyDisk).value())
	if err != nil || disk < 1 {
		return spec, "Disk: whole GiB, 1 or more"
	}
	if v.editing && disk < currentDiskGiB(v.machine) {
		return spec, fmt.Sprintf("Disk: a disk cannot shrink — at least %d", currentDiskGiB(v.machine))
	}

	spec.Config = engines.Config{CPUs: cpus, MemoryGiB: mem, DiskGiB: disk}
	if f := v.field(keyRuntime); f != nil && !v.editing {
		spec.Config.Runtime = f.value()
	}
	var rootful, usernet *bool
	if f := v.field(keyRootful); f != nil {
		b := f.value() == "yes"
		rootful = &b
	}
	if f := v.field(keyUserNet); f != nil {
		b := f.value() == "yes"
		usernet = &b
	}
	if v.editing {
		// Only what changed, so an untouched toggle is not re-applied.
		if rootful != nil && *rootful == v.machine.Rootful {
			rootful = nil
		}
		if usernet != nil && *usernet == v.machine.UserNet {
			usernet = nil
		}
		if cpus == v.machine.CPUs && mem == bytesGiB(v.machine.Memory) && disk == currentDiskGiB(v.machine) &&
			rootful == nil && usernet == nil {
			return spec, "nothing changed"
		}
	}
	spec.Config.Rootful, spec.Config.UserNet = rootful, usernet
	return spec, ""
}

// View renders the form.
func (v *RuntimeFormView) View() string { return v.render(v.footer()) }

func (v *RuntimeFormView) footer() string {
	switch {
	case !v.editing:
		return "enter creates and starts it · tab next field · esc cancel"
	case v.machine.Running:
		return "enter saves and restarts it — every container in it stops · esc cancel"
	default:
		return "enter saves and starts it · tab next field · esc cancel"
	}
}

// RuntimeSpec is a submitted form: which runtime, which machine, what
// configuration.
type RuntimeSpec struct {
	Provider string
	Name     string
	Config   engines.Config
}

// EncodeRuntimeSpec packs a spec into an action param.
func EncodeRuntimeSpec(s RuntimeSpec) string {
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(b)
}

// DecodeRuntimeSpec is the inverse of EncodeRuntimeSpec.
func DecodeRuntimeSpec(s string) (RuntimeSpec, error) {
	var spec RuntimeSpec
	if err := json.Unmarshal([]byte(s), &spec); err != nil || spec.Provider == "" || spec.Name == "" {
		return RuntimeSpec{}, fmt.Errorf("malformed machine spec %q", s)
	}
	return spec, nil
}
