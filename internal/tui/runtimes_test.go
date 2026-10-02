package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/colima"
	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

const colimaListJSON = `{"name":"default","status":"Running","arch":"aarch64","cpus":8,"memory":17179869184,"disk":107374182400,"runtime":"docker"}
{"name":"work","status":"Stopped","arch":"x86_64","cpus":2,"memory":2147483648,"disk":64424509440,"runtime":"containerd"}
`

// fakeColima answers `colima list` from a fixture and records every other
// verb, so no test here starts or stops a real VM.
type fakeColima struct {
	fail map[string]error
	// k8s, keyed by profile, is what `colima status --json` reports for
	// kubernetes; absent profiles answer nothing.
	k8s   map[string]bool
	calls []string
	mu    sync.Mutex
}

func (f *fakeColima) run(_ context.Context, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, key)
	if err := f.fail[key]; err != nil {
		return nil, err
	}
	if key == "list --json" {
		return []byte(colimaListJSON), nil
	}
	if profile, ok := strings.CutPrefix(key, "status --json --profile "); ok {
		if on, known := f.k8s[profile]; known {
			return []byte(fmt.Sprintf(`{"kubernetes":%t}`, on)), nil
		}
	}
	return nil, nil
}

func (f *fakeColima) called(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == key {
			return true
		}
	}
	return false
}

// newColimaApp builds an App with a fake colima and the colima view loaded,
// cursor on the first profile ("default").
func newColimaApp(t *testing.T, opts Options) (*App, *fakeColima) {
	t.Helper()
	f := &fakeColima{fail: map[string]error{}}
	opts.Engines = []engines.Provider{engines.Colima{C: colima.NewWithRunner(f.run, "/h/.colima")}}
	if opts.Version == "" {
		opts.Version = "test"
	}
	a := NewApp(nil, opts)
	a.splashActive = false
	a.loading = false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 40})
	runCmd(a, a.switchView(style.ViewRuntimes))
	return a, f
}

// runCmd executes a command synchronously and feeds its message back,
// flattening batches. Exec-process and tick commands are skipped: the
// first would take over the terminal, the second would sleep.
func runCmd(a *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	switch m := msg.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			runCmd(a, c)
		}
	case nil:
	default:
		runCmd(a, step(a, m))
	}
}

func TestColimaViewListsProfiles(t *testing.T) {
	a, _ := newColimaApp(t, Options{})
	out := render(a)
	for _, want := range []string{
		"runtimes(all)[2]", "default", "Running", "16GiB", "100GiB", "colima",
		"work", "Stopped", "2GiB", "colima-work", "containerd",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("colima view is missing %q\n%s", want, out)
		}
	}
	for _, want := range []string{"<5>", "Runtimes", "Connect", "Delete"} {
		if !strings.Contains(out, want) {
			t.Errorf("shortcuts are missing %q", want)
		}
	}
}

func TestColimaViewMarksTheConnectedProfile(t *testing.T) {
	c := colima.NewWithRunner(nil, "/h/.colima")
	v := views.NewRuntimesView([]engines.Provider{engines.Colima{C: c}}, c.DockerHost("work"))
	v.Resize(140, 10)
	v.Update(views.RuntimesRefreshMsg{Machines: []engines.Machine{
		{Provider: "colima", Name: "default", Host: c.DockerHost("default")},
		{Provider: "colima", Name: "work", Host: c.DockerHost("work")},
	}})
	out := ansi.ReplaceAllString(v.View(), "")
	marked := 0
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "▸") {
			continue
		}
		marked++
		if !strings.Contains(line, "work") {
			t.Errorf("marker is on the wrong row: %q", line)
		}
	}
	if marked != 1 {
		t.Errorf("%d rows marked connected, want 1\n%s", marked, out)
	}
}

func TestColimaKeys(t *testing.T) {
	a, _ := newColimaApp(t, Options{})
	cv := a.runtimesView()
	for _, tc := range []struct{ key, action, param string }{
		{key: "enter", action: "runtime_connect", param: views.MachineKey("colima", "default")},
		{key: "s", action: "runtime_shell", param: views.MachineKey("colima", "default")},
		{key: "u", action: "runtime_start", param: views.MachineKey("colima", "default")},
		{key: "x", action: "confirm_runtime_stop", param: views.MachineKey("colima", "default")},
		{key: "R", action: "confirm_runtime_restart", param: views.MachineKey("colima", "default")},
		{key: "ctrl+d", action: "confirm_runtime_delete", param: views.MachineKey("colima", "default")},
		{key: "e", action: "runtime_edit", param: views.MachineKey("colima", "default")},
		{key: "n", action: "runtime_new", param: "colima"},
		{key: "i", action: ""},
	} {
		action, param := cv.HandleKey(tc.key)
		if action != tc.action || param != tc.param {
			t.Errorf("%s: (%q, %q), want (%q, %q)", tc.key, action, param, tc.action, tc.param)
		}
	}

	// A profile mid-operation refuses every lifecycle key: a second start
	// on top of a running one is how colima ends up with two hostagents.
	cv.SetBusy("colima", "default", "starting")
	if action, _ := cv.HandleKey("x"); action != "runtime_busy" {
		t.Errorf("x on a busy machine = %q, want runtime_busy", action)
	}
}

// TestColimaStartRunsAndClearsBusy drives a start end to end: the row says
// "starting…" while colima runs, the verb reaches colima with the profile,
// and the marker clears when it is done.
func TestColimaStartRunsAndClearsBusy(t *testing.T) {
	a, f := newColimaApp(t, Options{})
	step(a, tea.KeyPressMsg{Code: tea.KeyDown}) // onto "work", which is stopped
	cmd := step(a, key("u"))
	if cmd == nil {
		t.Fatal("u produced no command")
	}
	if out := render(a); !strings.Contains(out, "starting…") {
		t.Errorf("row does not show the start in progress:\n%s", out)
	}
	runCmd(a, cmd)
	if !f.called("start work") {
		t.Errorf("colima was not asked to start work; calls %q", f.calls)
	}
	if out := render(a); strings.Contains(out, "starting…") {
		t.Error("busy marker survived the start finishing")
	}
	if !strings.Contains(a.flash, "started colima work") {
		t.Errorf("flash = %q", a.flash)
	}
}

func TestColimaFailureSurfacesColimasMessage(t *testing.T) {
	a, f := newColimaApp(t, Options{})
	f.fail["start default"] = &colima.Error{
		Args:   []string{"start", "default"},
		Stderr: `time="t" level=fatal msg="error starting vm: disk is locked"`,
		Err:    errors.New("exit status 1"),
	}
	runCmd(a, step(a, key("u")))
	if a.errFlash != "colima: error starting vm: disk is locked" {
		t.Errorf("errFlash = %q", a.errFlash)
	}
	if cv := a.runtimesView(); cv.Busy("colima", "default") != "" {
		t.Error("a failed start left the profile marked busy")
	}
}

// TestColimaDestructiveVerbsConfirm pins that stop, restart and delete all
// ask first and that declining reaches colima with nothing.
func TestColimaDestructiveVerbsConfirm(t *testing.T) {
	for _, tc := range []struct{ key, words, verb string }{
		{key: "x", words: "stop colima default", verb: "stop default"},
		{key: "R", words: "restart colima default", verb: "restart default"},
		{key: "ctrl+d", words: "delete colima default", verb: "delete --force default"},
	} {
		a, f := newColimaApp(t, Options{})
		step(a, key(tc.key))
		if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), tc.words) {
			t.Errorf("%s: confirm %v %q", tc.key, a.confirm.Active(), a.confirm.Prompt())
			continue
		}
		step(a, key("n"))
		if f.called(tc.verb) {
			t.Errorf("%s: declining still ran colima %s", tc.key, tc.verb)
		}

		step(a, key(tc.key))
		runCmd(a, step(a, key("y")))
		if !f.called(tc.verb) {
			t.Errorf("%s: confirming did not run colima %s; calls %q", tc.key, tc.verb, f.calls)
		}
	}
}

func TestColimaReadonlyRefusesLifecycle(t *testing.T) {
	a, f := newColimaApp(t, Options{ReadOnly: true})
	for _, k := range []string{"u", "x", "R", "ctrl+d", "s", "K"} {
		a.errFlash = ""
		runCmd(a, step(a, key(k)))
		if !strings.Contains(a.errFlash, "readonly") {
			t.Errorf("%s: not refused in readonly (errFlash %q)", k, a.errFlash)
		}
		if a.confirm.Active() {
			t.Errorf("%s: readonly opened a confirm", k)
			step(a, key("n"))
		}
	}
	for _, c := range f.calls {
		if !colimaReadOnly(c) {
			t.Errorf("readonly reached colima %q", c)
		}
	}
}

func TestColimaConnectRefusesAStoppedProfile(t *testing.T) {
	a, _ := newColimaApp(t, Options{})
	step(a, tea.KeyPressMsg{Code: tea.KeyDown})
	if cmd := step(a, key("enter")); cmd != nil {
		t.Error("enter on a stopped profile tried to connect")
	}
	if !strings.Contains(a.errFlash, "work is stopped") {
		t.Errorf("errFlash = %q", a.errFlash)
	}
}

// TestColimaListingLandsWhileAway pins the routing in Update: a listing
// that arrives after the user has left the view must still reach it, or
// its single-flight guard stays set and it never refreshes again.
func TestColimaListingLandsWhileAway(t *testing.T) {
	a, _ := newColimaApp(t, Options{})
	cv := a.runtimesView()
	pending := cv.Refresh() // in flight
	if pending == nil {
		t.Fatal("refresh was refused before anything was in flight")
	}
	step(a, key("0")) // leave
	step(a, pending())
	if cv.Refresh() == nil {
		t.Error("colima view is wedged: the listing that landed while away never cleared its in-flight guard")
	}
}

func TestColimaNotInstalled(t *testing.T) {
	a := newTestApp(t)
	step(a, key("5"))
	if out := render(a); !strings.Contains(out, "no container runtime found") {
		t.Errorf("no install hint:\n%s", out)
	}
}

func TestStartOnColimaOpensTheColimaView(t *testing.T) {
	a, _ := newColimaApp(t, Options{StartOnRuntimes: true, Notice: "no docker daemon at x"})
	if a.view != style.ViewRuntimes {
		t.Errorf("view = %v, want colima", a.view)
	}
	if a.errFlash != "no docker daemon at x" {
		t.Errorf("errFlash = %q", a.errFlash)
	}
	b := NewApp(nil, Options{Version: "test", StartOnRuntimes: true})
	if b.view != style.ViewRuntimes {
		t.Error("StartOnRuntimes did not set the opening view")
	}
}

func TestColimaPaletteCommand(t *testing.T) {
	a := newTestApp(t)
	a.dispatchCommand("colima")
	if a.view != style.ViewRuntimes {
		t.Errorf(":colima landed on %v", a.view)
	}
}

func typeText(a *App, s string) {
	for _, r := range s {
		step(a, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

var (
	keyTab       = tea.KeyPressMsg{Code: tea.KeyTab}
	keyBackspace = tea.KeyPressMsg{Code: tea.KeyBackspace}
	keyRight     = tea.KeyPressMsg{Code: tea.KeyRight}
)

func clearField(a *App, n int) {
	for range n {
		step(a, keyBackspace)
	}
}

// TestColimaCreateProfile drives the whole create path through the form,
// including digits and `r` — keys the app would otherwise take for view
// switching and refresh — and lands back on the profiles.
func TestColimaCreateProfile(t *testing.T) {
	a, f := newColimaApp(t, Options{})
	step(a, key("n"))
	if a.view != style.ViewRuntimeForm {
		t.Fatalf("n opened %v, want the form", a.view)
	}
	typeText(a, "dev-r1")
	if a.view != style.ViewRuntimeForm {
		t.Fatalf("typing a name left the form for %v", a.view)
	}
	step(a, keyTab) // CPUs
	clearField(a, 3)
	typeText(a, "4")
	step(a, keyTab) // Memory
	clearField(a, 6)
	typeText(a, "6.5")
	step(a, keyTab) // Disk
	step(a, keyTab) // Runtime
	step(a, keyRight)

	out := render(a)
	for _, want := range []string{"new machine", "dev-r1", "6.5", "containerd", "enter creates and starts it"} {
		if !strings.Contains(out, want) {
			t.Errorf("form is missing %q\n%s", want, out)
		}
	}

	runCmd(a, step(a, key("enter")))
	if a.view != style.ViewRuntimes {
		t.Errorf("after create the view is %v, want the profiles", a.view)
	}
	if want := "start dev-r1 --cpus 4 --memory 6.5 --disk 100 --runtime containerd"; !f.called(want) {
		t.Errorf("colima was not asked to %q; calls %q", want, f.calls)
	}
	if !strings.Contains(a.flash, "created colima dev-r1") {
		t.Errorf("flash = %q", a.flash)
	}
}

func TestColimaCreateRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(a *App)
		want  string
	}{
		{name: "duplicate", setup: func(a *App) { typeText(a, "default") }, want: "already exists"},
		{name: "empty", setup: func(*App) {}, want: "needs a name"},
		{name: "bad chars", setup: func(a *App) { typeText(a, "my box") }, want: "letters, digits"},
		{name: "cpus", setup: func(a *App) {
			typeText(a, "dev")
			step(a, keyTab)
			clearField(a, 3)
			typeText(a, "9999")
		}, want: "CPUs"},
		{name: "memory", setup: func(a *App) {
			typeText(a, "dev")
			step(a, keyTab)
			step(a, keyTab)
			clearField(a, 6)
			typeText(a, "lots")
		}, want: "Memory"},
	} {
		a, f := newColimaApp(t, Options{})
		step(a, key("n"))
		tc.setup(a)
		runCmd(a, step(a, key("enter")))
		if a.view != style.ViewRuntimeForm {
			t.Errorf("%s: invalid input left the form", tc.name)
		}
		if out := render(a); !strings.Contains(out, tc.want) {
			t.Errorf("%s: no %q error shown\n%s", tc.name, tc.want, out)
		}
		for _, c := range f.calls {
			if strings.HasPrefix(c, "start") {
				t.Errorf("%s: invalid input reached colima: %q", tc.name, c)
			}
		}
	}
}

func TestColimaFormEscCancels(t *testing.T) {
	a, f := newColimaApp(t, Options{})
	step(a, key("n"))
	typeText(a, "dev")
	step(a, key("esc"))
	if a.view != style.ViewRuntimes {
		t.Errorf("esc left the view at %v", a.view)
	}
	for _, c := range f.calls {
		if !colimaReadOnly(c) {
			t.Errorf("canceled form reached colima: %q", c)
		}
	}
}

// TestColimaEditStoppedProfile: a stopped profile is started with the new
// resources, without a confirm — nothing is running to interrupt. Runtime
// is not passed: it cannot change on an existing profile.
func TestColimaEditStoppedProfile(t *testing.T) {
	a, f := newColimaApp(t, Options{})
	step(a, tea.KeyPressMsg{Code: tea.KeyDown}) // work: stopped, 2 cpus, 2GiB, 60GiB
	step(a, key("e"))
	if a.view != style.ViewRuntimeForm {
		t.Fatalf("e opened %v", a.view)
	}
	out := render(a)
	for _, want := range []string{"edit work", "60", "a disk cannot shrink", "enter saves and starts it"} {
		if !strings.Contains(out, want) {
			t.Errorf("edit form is missing %q\n%s", want, out)
		}
	}
	clearField(a, 3) // focus starts on CPUs
	typeText(a, "4")
	runCmd(a, step(a, key("enter")))
	if a.confirm.Active() {
		t.Error("editing a stopped profile asked to confirm")
	}
	if want := "start work --cpus 4 --memory 2 --disk 60"; !f.called(want) {
		t.Errorf("colima was not asked to %q; calls %q", want, f.calls)
	}
}

// TestColimaEditRunningProfileConfirms: applying to a running profile stops
// it, so it asks first, and then stops before starting with the new flags.
func TestColimaEditRunningProfileConfirms(t *testing.T) {
	a, f := newColimaApp(t, Options{})
	step(a, key("e")) // default: running
	step(a, keyTab)   // Memory
	clearField(a, 6)
	typeText(a, "12")
	step(a, key("enter"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "it restarts") {
		t.Fatalf("no restart confirm: %v %q", a.confirm.Active(), a.confirm.Prompt())
	}
	if f.called("stop default") {
		t.Fatal("stopped before the confirm was answered")
	}
	runCmd(a, step(a, key("y")))
	stop, start := -1, -1
	for i, c := range f.calls {
		switch c {
		case "stop default":
			stop = i
		case "start default --cpus 8 --memory 12 --disk 100":
			start = i
		}
	}
	if stop < 0 || start < 0 || stop > start {
		t.Errorf("want stop then start with new memory; calls %q", f.calls)
	}
}

func TestColimaEditRefusesShrinkAndNoop(t *testing.T) {
	a, f := newColimaApp(t, Options{})
	step(a, key("e"))
	runCmd(a, step(a, key("enter")))
	if out := render(a); !strings.Contains(out, "nothing changed") {
		t.Errorf("unchanged form submitted without complaint\n%s", out)
	}
	step(a, keyTab)
	step(a, keyTab) // Disk
	clearField(a, 5)
	typeText(a, "50")
	runCmd(a, step(a, key("enter")))
	if out := render(a); !strings.Contains(out, "cannot shrink") {
		t.Errorf("disk shrink not refused\n%s", out)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "start") || strings.HasPrefix(c, "stop") {
			t.Errorf("refused edit reached colima: %q", c)
		}
	}
}

func TestColimaReadonlyRefusesCreateAndEdit(t *testing.T) {
	a, _ := newColimaApp(t, Options{ReadOnly: true})
	for _, k := range []string{"n", "e"} {
		step(a, key(k))
		if a.view != style.ViewRuntimes || !strings.Contains(a.errFlash, "readonly") {
			t.Errorf("%s: readonly let the form open (view %v, flash %q)", k, a.view, a.errFlash)
		}
	}
}

func TestColimaSpecRoundTrip(t *testing.T) {
	yes := true
	in := views.RuntimeSpec{
		Provider: "podman",
		Name:     "x",
		Config:   engines.Config{CPUs: 3, MemoryGiB: 2.5, DiskGiB: 40, Rootful: &yes},
	}
	got, err := views.DecodeRuntimeSpec(views.EncodeRuntimeSpec(in))
	if err != nil || got.Provider != "podman" || got.Name != "x" || got.Config.CPUs != 3 || got.Config.Rootful == nil ||
		!*got.Config.Rootful {
		t.Errorf("round trip: %+v %v", got, err)
	}
	if _, err := views.DecodeRuntimeSpec("colima\x00x"); err == nil {
		t.Error("malformed spec decoded")
	}
}

// colimaReadOnly reports whether a colima call only reads: listing the
// profiles, or a running one's status (where the K8S column comes from).
func colimaReadOnly(call string) bool {
	return call == "list --json" || strings.HasPrefix(call, "status --json ")
}
