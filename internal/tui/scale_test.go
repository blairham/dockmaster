// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// scaleContainers is project shop: three web containers (one stopped —
// compose counts it) and one db, labeled with dir's compose file.
func scaleContainers(dir string) []docker.Container {
	file := filepath.Join(dir, "compose.yaml")
	labels := func(svc string) map[string]string {
		return map[string]string{
			docker.LabelProject: "shop", docker.LabelService: svc,
			docker.LabelConfigFiles: file, docker.LabelWorkingDir: dir,
		}
	}
	return []docker.Container{
		{ID: "w1", Name: "shop-web-1", State: "running", Project: "shop", Service: "web", Labels: labels("web")},
		{ID: "w2", Name: "shop-web-2", State: "running", Project: "shop", Service: "web", Labels: labels("web")},
		{ID: "w3", Name: "shop-web-3", State: "exited", Project: "shop", Service: "web", Labels: labels("web")},
		{ID: "d1", Name: "shop-db-1", State: "running", Project: "shop", Service: "db", Labels: labels("db")},
	}
}

// newScaleApp opens the projects view on scaleContainers, the compose file
// present or not.
func newScaleApp(t *testing.T, opts Options, present bool) (*App, *fakeCompose, string) {
	t.Helper()
	dir := t.TempDir()
	if present {
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := newSizedApp(t, opts, 200, 30)
	f := &fakeCompose{}
	a.composeRunner = f.run
	a.splashActive = false
	step(a, key("4"))
	step(a, views.ProjectsRefreshMsg{Containers: scaleContainers(dir)})
	return a, f, dir
}

// scaleArgv is the compose argv a scale of svc to n runs for the fixture.
func scaleArgv(dir, svc, n string) string {
	return "compose -p shop --project-directory " + dir + " -f " + filepath.Join(dir, "compose.yaml") +
		" up -d --scale " + svc + "=" + n + " --no-recreate " + svc
}

// setReplicas replaces the replicas field's text.
func setReplicas(a *App, s string) {
	for range 8 {
		step(a, keyBackspace)
	}
	typeText(a, s)
}

// TestScaleFormListsServices: s opens a form over the project's services,
// replicas prefilled with the chosen one's current count — every container,
// stopped too — and following the service as it changes.
func TestScaleFormListsServices(t *testing.T) {
	a, _, _ := newScaleApp(t, Options{}, true)
	step(a, key("s"))
	if a.view != style.ViewScaleForm {
		t.Fatalf("s opened %v", style.ViewName(a.view))
	}
	out := render(a)
	for _, want := range []string{"Service", "‹ db ›", "Replicas", "[1", "now 1", "enter scales it", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("form missing %q:\n%s", want, out)
		}
	}
	// Focus starts on replicas; up moves to the service to change it.
	step(a, key("up"))
	step(a, keyRight)
	out = render(a)
	if !strings.Contains(out, "‹ web ›") || !strings.Contains(out, "[3") || !strings.Contains(out, "now 3") {
		t.Errorf("web not chosen with its count 3:\n%s", out)
	}
	t.Logf("scale form:\n%s", out)
	step(a, keyRight)
	if out := render(a); !strings.Contains(out, "‹ db ›") || !strings.Contains(out, "[1") {
		t.Errorf("choice did not wrap back to db with 1:\n%s", out)
	}
}

// TestScaleUpRunsWithoutConfirm: a scale up removes nothing, so it runs
// straight away — the exact argv, the row busy meanwhile.
func TestScaleUpRunsWithoutConfirm(t *testing.T) {
	a, f, dir := newScaleApp(t, Options{}, true)
	step(a, key("s"))
	step(a, key("up"))
	step(a, keyRight) // web, 3
	step(a, key("down"))
	setReplicas(a, "5")
	cmd := step(a, key("enter"))
	if a.confirm.Active() {
		t.Fatalf("a scale up asked: %q", a.confirm.Prompt())
	}
	if a.view != style.ViewProjects {
		t.Errorf("form still up: %v", style.ViewName(a.view))
	}
	if pv := typedView[*views.ProjectsView](a, style.ViewProjects); pv.Busy("shop") != "scaling" {
		t.Errorf("busy = %q", pv.Busy("shop"))
	}
	runOnce(a, cmd)
	if got, want := f.last(), scaleArgv(dir, "web", "5"); got != want {
		t.Errorf("ran %q\nwant %q", got, want)
	}
	if a.flash != "scaled web to 5 in shop" {
		t.Errorf("flash = %q", a.flash)
	}
}

// TestScaleSameCountRunsWithoutConfirm: submitting the prefilled count
// removes nothing either.
func TestScaleSameCountRunsWithoutConfirm(t *testing.T) {
	a, f, dir := newScaleApp(t, Options{}, true)
	step(a, key("s"))
	runOnce(a, step(a, key("enter")))
	if a.confirm.Active() {
		t.Fatalf("an unchanged count asked: %q", a.confirm.Prompt())
	}
	if got, want := f.last(), scaleArgv(dir, "db", "1"); got != want {
		t.Errorf("ran %q\nwant %q", got, want)
	}
}

// TestScaleDownConfirms: a scale down removes containers, so it asks,
// naming the service and the counts, and only yes runs it.
func TestScaleDownConfirms(t *testing.T) {
	for _, tc := range []struct {
		n, prompt string
	}{
		{n: "1", prompt: "scale web in shop from 3 to 1? 2 containers are removed"},
		{n: "2", prompt: "scale web in shop from 3 to 2? 1 container is removed"},
		{n: "0", prompt: "scale web in shop from 3 to 0? every one of its containers is removed"},
	} {
		t.Run(tc.n, func(t *testing.T) {
			a, f, dir := newScaleApp(t, Options{}, true)
			open := func() {
				step(a, key("s"))
				step(a, key("up"))
				step(a, keyRight)
				step(a, key("down"))
				setReplicas(a, tc.n)
				if cmd := step(a, key("enter")); cmd != nil {
					runOnce(a, cmd)
				}
			}
			open()
			if !a.confirm.Active() || a.confirm.Prompt() != tc.prompt {
				t.Fatalf("confirm = %v %q\nwant %q", a.confirm.Active(), a.confirm.Prompt(), tc.prompt)
			}
			step(a, key("n"))
			if len(f.calls) != 0 {
				t.Fatalf("declined scale ran %q", f.calls)
			}
			open()
			runOnce(a, step(a, key("y")))
			if got, want := f.last(), scaleArgv(dir, "web", tc.n); got != want {
				t.Errorf("ran %q\nwant %q", got, want)
			}
		})
	}
}

// TestScaleRefusesBadReplicas: anything but a whole number, 0 or more,
// stays in the form with the reason, and runs nothing.
func TestScaleRefusesBadReplicas(t *testing.T) {
	for _, bad := range []string{"", "-1", "abc", "1.5", "+2", "2x", "1 0", " "} {
		a, f, _ := newScaleApp(t, Options{}, true)
		step(a, key("s"))
		setReplicas(a, bad)
		runOnce(a, step(a, key("enter")))
		if a.view != style.ViewScaleForm || !strings.Contains(render(a), "a whole number, 0 or more") {
			t.Errorf("%q: not refused in the form (view %v):\n%s", bad, style.ViewName(a.view), render(a))
		}
		if a.confirm.Active() || len(f.calls) != 0 {
			t.Errorf("%q: confirm %v, ran %q", bad, a.confirm.Active(), f.calls)
		}
	}
}

// TestScaleEscCancels: esc closes the form and runs nothing.
func TestScaleEscCancels(t *testing.T) {
	a, f, _ := newScaleApp(t, Options{}, true)
	step(a, key("s"))
	setReplicas(a, "0")
	step(a, key("esc"))
	if a.view != style.ViewProjects || a.confirm.Active() || len(f.calls) != 0 {
		t.Errorf("esc: view %v, confirm %v, ran %q", style.ViewName(a.view), a.confirm.Active(), f.calls)
	}
}

func TestScaleReadonly(t *testing.T) {
	a, f, _ := newScaleApp(t, Options{ReadOnly: true}, true)
	step(a, key("s"))
	if a.view != style.ViewProjects || !strings.Contains(a.errFlash, "readonly mode — scale form refused") {
		t.Errorf("readonly: view %v, flash %q", style.ViewName(a.view), a.errFlash)
	}
	// A submission reaching the app some other way is refused too.
	a.errFlash = ""
	_, cmd := a.handleAction("compose_scale", `{"project":"shop","service":"web","replicas":5,"current":3}`)
	runOnce(a, cmd)
	if !strings.Contains(a.errFlash, "readonly") || len(f.calls) != 0 {
		t.Errorf("readonly compose_scale: flash %q, ran %q", a.errFlash, f.calls)
	}
}

// TestScaleRefusesWithoutFiles: scale has no per-container fallback, so
// with the compose file gone it refuses, with the reason.
func TestScaleRefusesWithoutFiles(t *testing.T) {
	a, f, dir := newScaleApp(t, Options{}, false)
	step(a, key("s"))
	want := "cannot scale shop: " + filepath.Join(dir, "compose.yaml") + " is not on this machine"
	if a.view != style.ViewProjects || a.errFlash != want {
		t.Errorf("view %v, flash %q\nwant %q", style.ViewName(a.view), a.errFlash, want)
	}
	if len(f.calls) != 0 {
		t.Errorf("ran %q", f.calls)
	}
}

// TestScaleRefusesWhileBusy: s on a project mid-verb is refused, as the
// other verbs are; and a submission while one has started since the form
// opened is refused too.
func TestScaleRefusesWhileBusy(t *testing.T) {
	a, f, _ := newScaleApp(t, Options{}, true)
	pv := typedView[*views.ProjectsView](a, style.ViewProjects)
	pv.SetBusy("shop", "starting")
	step(a, key("s"))
	if a.view != style.ViewProjects || a.errFlash != "shop is already starting — wait for it to finish" {
		t.Errorf("busy s: view %v, flash %q", style.ViewName(a.view), a.errFlash)
	}

	pv.SetBusy("shop", "")
	a.errFlash = ""
	step(a, key("s"))
	pv.SetBusy("shop", "pulling")
	setReplicas(a, "7")
	runOnce(a, step(a, key("enter")))
	if a.errFlash != "shop is already pulling — wait for it to finish" {
		t.Errorf("busy submit: flash %q", a.errFlash)
	}
	if len(f.calls) != 0 {
		t.Errorf("ran %q", f.calls)
	}
}

// TestXrayScalesAService: s on xray's service node opens the form on that
// service; on its project node, on the first; on a container it is still
// the shell.
func TestXrayScalesAService(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t)
	f := &fakeCompose{}
	a.composeRunner = f.run
	a.dispatchCommand("xray")
	xv := typedView[*views.XrayView](a, style.ViewXray)
	step(a, views.XrayRefreshMsg{Containers: scaleContainers(dir)})

	gotoNode(t, a, xv, "p:shop/s:web")
	step(a, key("s"))
	if a.view != style.ViewScaleForm {
		t.Fatalf("s on a service node opened %v", style.ViewName(a.view))
	}
	if out := render(a); !strings.Contains(out, "‹ web ›") || !strings.Contains(out, "[3") {
		t.Errorf("form not on web with 3:\n%s", out)
	}
	setReplicas(a, "4")
	runOnce(a, step(a, key("enter")))
	if a.view != style.ViewXray {
		t.Errorf("form did not close back onto xray: %v", style.ViewName(a.view))
	}
	if got, want := f.last(), scaleArgv(dir, "web", "4"); got != want {
		t.Errorf("ran %q\nwant %q", got, want)
	}

	// Back to the top: the project node starts on the first service.
	for range 20 {
		step(a, key("k"))
	}
	gotoNode(t, a, xv, "p:shop")
	step(a, key("s"))
	if out := render(a); a.view != style.ViewScaleForm || !strings.Contains(out, "‹ db ›") {
		t.Errorf("project node: view %v\n%s", style.ViewName(a.view), out)
	}
	step(a, key("esc"))

	// Down to the db service: its own count, 1, scaled down confirms.
	gotoNode(t, a, xv, "p:shop/s:db")
	step(a, key("s"))
	setReplicas(a, "0")
	step(a, key("enter"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "scale db in shop from 1 to 0?") {
		t.Errorf("xray scale down confirm = %v %q", a.confirm.Active(), a.confirm.Prompt())
	}
	step(a, key("n"))
}

// TestXrayScaleReadonly: the xray route is gated as the projects one is.
func TestXrayScaleReadonly(t *testing.T) {
	dir := t.TempDir()
	a := newSizedApp(t, Options{ReadOnly: true}, 160, 44)
	a.splashActive = false
	a.dispatchCommand("xray")
	xv := typedView[*views.XrayView](a, style.ViewXray)
	step(a, views.XrayRefreshMsg{Containers: scaleContainers(dir)})
	gotoNode(t, a, xv, "p:shop/s:web")
	step(a, key("s"))
	if a.view != style.ViewXray || !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("view %v, flash %q", style.ViewName(a.view), a.errFlash)
	}
}

// TestScaleRechecksFilesOnSubmit: files that go away while the form is
// up refuse the submission, rather than running compose on nothing.
func TestScaleRechecksFilesOnSubmit(t *testing.T) {
	a, f, dir := newScaleApp(t, Options{}, true)
	step(a, key("s"))
	if err := os.Remove(filepath.Join(dir, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	setReplicas(a, "4")
	runOnce(a, step(a, key("enter")))
	if !strings.HasPrefix(a.errFlash, "cannot scale shop: ") || len(f.calls) != 0 {
		t.Errorf("flash %q, ran %q", a.errFlash, f.calls)
	}
}

// TestScaleRefusesWithoutServices: containers with a project label but no
// service label leave nothing to scale; s says so rather than opening an
// empty choice.
func TestScaleRefusesWithoutServices(t *testing.T) {
	a, f, dir := newScaleApp(t, Options{}, true)
	cs := scaleContainers(dir)[:1]
	cs[0].Service = ""
	step(a, views.ProjectsRefreshMsg{Containers: cs})
	step(a, key("s"))
	if a.view != style.ViewProjects || !strings.Contains(a.errFlash, "carries a compose service label") ||
		!strings.Contains(a.errFlash, "cannot scale shop") {
		t.Errorf("view %v, flash %q", style.ViewName(a.view), a.errFlash)
	}
	if len(f.calls) != 0 {
		t.Errorf("ran %q", f.calls)
	}
}

// TestProjectsViewGuardsScaleWhileBusy: s is one of the projects view's
// busy-guarded keys, as u, R and the rest are.
func TestProjectsViewGuardsScaleWhileBusy(t *testing.T) {
	a, _, _ := newScaleApp(t, Options{}, true)
	pv := typedView[*views.ProjectsView](a, style.ViewProjects)
	if act, param := pv.HandleKey("s"); act != "scale_form" || param != "shop" {
		t.Errorf("idle s = (%q, %q)", act, param)
	}
	pv.SetBusy("shop", "starting")
	if act, param := pv.HandleKey("s"); act != "project_busy" || param != "shop\x00starting" {
		t.Errorf("busy s = (%q, %q)", act, param)
	}
}
