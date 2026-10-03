// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/colima"
	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// cliFake answers any runtime CLI from a table and records every call.
type cliFake struct {
	out   map[string]string
	fail  map[string]bool
	calls []string
	mu    sync.Mutex
}

func (f *cliFake) run(_ context.Context, bin string, args ...string) ([]byte, error) {
	key := strings.Join(append([]string{bin}, args...), " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, key)
	if f.fail[key] {
		return nil, errors.New(bin + ": refused")
	}
	return []byte(f.out[key]), nil
}

func (f *cliFake) called(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == key {
			return true
		}
	}
	return false
}

// newEnginesApp opens the runtimes view over podman and Docker Desktop,
// both on fake CLIs; Docker Desktop answers its ping.
func newEnginesApp(t *testing.T) (*App, *cliFake) {
	t.Helper()
	var opts Options
	f := &cliFake{out: map[string]string{
		"podman machine list --format json": `[{"Name":"pm","Running":false,"VMType":"applehv","CPUs":2,"Memory":"2147483648","DiskSize":"107374182400"}]`,
	}, fail: map[string]bool{}}
	dd := engines.Single{
		Run: f.run, Engine: engines.DockerDesktopName, Context: "desktop-linux", Host: "unix:///h/.docker/run/docker.sock",
		StartC: []string{"docker", "desktop", "start"}, StopC: []string{"docker", "desktop", "stop"},
		Ping: func(context.Context, string) bool { return true },
	}
	opts.Engines = []engines.Provider{engines.Podman{Run: f.run}, dd}
	if opts.Version == "" {
		opts.Version = "test"
	}
	a := NewApp(nil, opts)
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 200, Height: 30})
	runCmd(a, a.switchView(style.ViewRuntimes))
	return a, f
}

func TestRuntimesListsEveryProvider(t *testing.T) {
	a, _ := newEnginesApp(t)
	out := render(a)
	for _, want := range []string{"PROVIDER", "podman", "pm", "Stopped", "docker-desktop", "Running", "desktop-linux", "runtimes(all)[2]"} {
		if !strings.Contains(out, want) {
			t.Errorf("runtimes view is missing %q\n%s", want, out)
		}
	}
}

// TestOneRuntimeFailingDoesNotHideTheOthers: a runtime whose CLI errors
// is reported, and the rest are still listed.
func TestOneRuntimeFailingDoesNotHideTheOthers(t *testing.T) {
	f := &cliFake{fail: map[string]bool{"podman machine list --format json": true}}
	dd := engines.Single{
		Run: f.run, Engine: engines.DockerDesktopName, Host: "unix:///d.sock",
		Ping: func(context.Context, string) bool { return true },
	}
	a := NewApp(nil, Options{Version: "test", Engines: []engines.Provider{engines.Podman{Run: f.run}, dd}})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 200, Height: 30})
	runCmd(a, a.switchView(style.ViewRuntimes))
	out := render(a)
	if !strings.Contains(out, "podman: podman: refused") || !strings.Contains(out, "docker-desktop") {
		t.Errorf("want the podman error and docker-desktop still listed:\n%s", out)
	}
}

// TestEachRuntimeGetsItsOwnVerbs: start, stop and restart reach the right
// CLI for the row they were pressed on.
func TestEachRuntimeGetsItsOwnVerbs(t *testing.T) {
	a, f := newEnginesApp(t)
	runCmd(a, step(a, key("u"))) // pm
	if !f.called("podman machine start pm") {
		t.Errorf("u on podman: calls %q", f.calls)
	}
	step(a, tea.KeyPressMsg{Code: tea.KeyDown}) // docker-desktop
	step(a, key("x"))
	if !strings.Contains(a.confirm.Prompt(), "stop docker-desktop?") {
		t.Errorf("confirm = %q", a.confirm.Prompt())
	}
	runCmd(a, step(a, key("y")))
	if !f.called("docker desktop stop") {
		t.Errorf("x on docker desktop: calls %q", f.calls)
	}
}

// TestSingleEngineRefusesWhatItCannotDo: an app-managed engine has no
// machines to delete, resize, shell into or create, and says so by name
// instead of running anything.
func TestSingleEngineRefusesWhatItCannotDo(t *testing.T) {
	a, f := newEnginesApp(t)
	step(a, tea.KeyPressMsg{Code: tea.KeyDown}) // docker-desktop
	before := len(f.calls)
	for k, want := range map[string]string{
		"ctrl+d": "docker-desktop cannot delete machines",
		"e":      "docker-desktop cannot change resources",
		"s":      "docker-desktop cannot open a shell",
	} {
		a.errFlash = ""
		runCmd(a, step(a, key(k)))
		if !strings.Contains(a.errFlash, want) || a.confirm.Active() || a.view != style.ViewRuntimes {
			t.Errorf("%s: flash %q confirm %v view %v", k, a.errFlash, a.confirm.Active(), a.view)
		}
	}
	if len(f.calls) != before {
		t.Errorf("refused verbs ran commands: %q", f.calls[before:])
	}

	only := NewApp(
		nil,
		Options{Version: "test", Engines: []engines.Provider{engines.Single{Engine: engines.OrbStackName}}},
	)
	only.splashActive = false
	step(only, tea.WindowSizeMsg{Width: 160, Height: 30})
	runCmd(only, only.switchView(style.ViewRuntimes))
	step(only, key("n"))
	if only.view != style.ViewRuntimes || !strings.Contains(only.errFlash, "no runtime here creates machines") {
		t.Errorf("n with only single engines: view %v flash %q", only.view, only.errFlash)
	}
}

// TestPodmanCreateForm: n on a podman row creates a podman machine, with no
// Runtime field — podman has no choice of container runtime.
func TestPodmanCreateForm(t *testing.T) {
	a, f := newEnginesApp(t)
	step(a, key("n"))
	out := render(a)
	// Provider, Name, CPUs, Memory, Disk and podman's two toggles — no
	// Runtime choice. ("Runtime" alone would also match the breadcrumb.)
	if !strings.Contains(out, "new machine(all)[7]") || !strings.Contains(out, "podman") ||
		!strings.Contains(out, "Rootful") {
		t.Errorf("podman form:\n%s", out)
	}
	typeText(a, "dev")
	runCmd(a, step(a, key("enter")))
	if !f.called(
		"podman machine init --cpus 2 --memory 2048 --disk-size 100 --rootful=false --user-mode-networking=false --now dev",
	) {
		t.Errorf("calls %q", f.calls)
	}
}

func TestOwnerFindsTheMachineBehindAnEndpoint(t *testing.T) {
	dd := engines.Single{
		Engine: engines.DockerDesktopName,
		Host:   "unix:///d.sock",
		Ping:   func(context.Context, string) bool { return false },
	}
	m, ok := engines.Owner(context.Background(), []engines.Provider{dd}, "unix:///d.sock")
	if !ok || m.Provider != engines.DockerDesktopName || m.Running {
		t.Errorf("Owner = %+v, %v", m, ok)
	}
	if _, ok := engines.Owner(context.Background(), []engines.Provider{dd}, "unix:///other.sock"); ok {
		t.Error("Owner matched a foreign endpoint")
	}
	_ = views.MachineKey
}

// newColimaAndPodmanApp: colima with one profile, podman installed with no
// machines yet — the case where n used to offer no way to create one.
func newColimaAndPodmanApp(t *testing.T, podmanList string) (*App, *fakeColima, *cliFake) {
	t.Helper()
	fc := &fakeColima{fail: map[string]error{}}
	pf := &cliFake{out: map[string]string{
		"podman machine list --format json": podmanList,
		"podman machine inspect pm":         `[{"Rootful":false,"UserModeNetworking":false}]`,
	}, fail: map[string]bool{}}
	a := NewApp(nil, Options{Version: "test", Engines: []engines.Provider{
		engines.Colima{C: colima.NewWithRunner(fc.run, "/h/.colima")},
		engines.Podman{Run: pf.run},
	}})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 200, Height: 30})
	runCmd(a, a.switchView(style.ViewRuntimes))
	return a, fc, pf
}

// TestCreateTheFirstPodmanMachine: with no podman row to stand on, n on a
// colima row and ←/→ on Provider makes a podman machine; the fields follow
// the runtime and what was typed survives the switch.
func TestCreateTheFirstPodmanMachine(t *testing.T) {
	a, fc, pf := newColimaAndPodmanApp(t, `[]`)
	step(a, key("n"))
	if out := render(a); !strings.Contains(out, "colima") || !strings.Contains(out, "Runtime") {
		t.Fatalf("form should start on colima, the row's runtime:\n%s", out)
	}
	typeText(a, "dev")
	step(a, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}) // back to Provider
	step(a, tea.KeyPressMsg{Code: tea.KeyRight})
	out := render(a)
	if !strings.Contains(out, "‹ podman ›") || !strings.Contains(out, "Rootful") || strings.Contains(out, "‹ docker ›") {
		t.Errorf("after switching to podman:\n%s", out)
	}
	if !strings.Contains(out, "dev") {
		t.Error("the typed name was lost in the switch")
	}
	runCmd(a, step(a, key("enter")))
	if !pf.called(
		"podman machine init --cpus 2 --memory 2048 --disk-size 100 --rootful=false --user-mode-networking=false --now dev",
	) {
		t.Errorf("podman calls %q", pf.calls)
	}
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "start") {
			t.Errorf("colima was asked to %q", c)
		}
	}
}

// TestPodmanEditTogglesOnlyWhatChanged: turning rootful on sends exactly
// that; untouched resources and toggles are not re-applied.
func TestPodmanEditTogglesOnlyWhatChanged(t *testing.T) {
	a, _, pf := newColimaAndPodmanApp(t,
		`[{"Name":"pm","Running":false,"CPUs":2,"Memory":"2147483648","DiskSize":"107374182400"}]`)
	toPodmanRow(t, a)
	step(a, key("e"))
	for range 3 { // CPUs → Memory → Disk → Rootful
		step(a, tea.KeyPressMsg{Code: tea.KeyTab})
	}
	step(a, tea.KeyPressMsg{Code: tea.KeyRight}) // rootful: no → yes
	runCmd(a, step(a, key("enter")))
	if !pf.called("podman machine set --cpus 2 --memory 2048 --rootful=true pm") {
		t.Errorf("podman calls %q", pf.calls)
	}
}

func TestRuntimeInspect(t *testing.T) {
	a, _, pf := newColimaAndPodmanApp(t,
		`[{"Name":"pm","Running":false,"CPUs":2,"Memory":"2147483648","DiskSize":"107374182400"}]`)
	pf.out["podman machine inspect pm"] = `[{"Name":"pm","Rootful":false,"Resources":{"CPUs":2}}]`
	toPodmanRow(t, a)
	runCmd(a, step(a, key("o")))
	if a.view != style.ViewInspect {
		t.Fatalf("o opened %v", a.view)
	}
	out := render(a)
	if !strings.Contains(out, `"Resources": {`) || !strings.Contains(out, "podman pm") {
		t.Errorf("inspect view:\n%s", out)
	}
}

// toPodmanRow moves the cursor to the first podman machine.
func toPodmanRow(t *testing.T, a *App) {
	t.Helper()
	for range 10 {
		if m, ok := a.runtimesView().Selected(); ok && m.Provider == engines.PodmanName {
			return
		}
		step(a, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	t.Fatal("no podman row")
}

// TestRuntimesListInParallel: two runtimes slow to answer are asked at the
// same time, not one after the other, and the rows keep provider order.
//
// Each fake waits until both are in flight before answering — a rendezvous
// only a parallel refresh can reach. A serial one leaves the first waiting
// alone until its timeout, which the test sees as a miss. Unlike timing the
// refresh, this cannot fail because the machine is busy (#23).
func TestRuntimesListInParallel(t *testing.T) {
	var (
		mu      sync.Mutex
		waiting int
		met     = make(chan struct{})
		alone   []string
	)
	slow := func(name string) engines.Single {
		return engines.Single{
			Engine: name, Host: "tcp://" + name,
			Ping: func(context.Context, string) bool {
				mu.Lock()
				waiting++
				if waiting == 2 {
					close(met)
				}
				mu.Unlock()
				select {
				case <-met:
				case <-time.After(2 * time.Second):
					mu.Lock()
					alone = append(alone, name)
					mu.Unlock()
				}
				return true
			},
		}
	}
	v := views.NewRuntimesView([]engines.Provider{slow(engines.OrbStackName), slow(engines.RancherDesktopName)}, "")
	msg, ok := v.Refresh()().(views.RuntimesRefreshMsg)
	if len(alone) > 0 {
		t.Errorf("%v was asked with no other runtime in flight; the runtimes were listed one after another", alone)
	}
	if !ok || len(msg.Machines) != 2 || msg.Machines[0].Name != engines.OrbStackName ||
		msg.Machines[1].Name != engines.RancherDesktopName {
		t.Errorf("rows lost provider order: %+v", msg.Machines)
	}
}
