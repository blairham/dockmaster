// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/tui/style"
)

// fakeBuildx stands in for the docker CLI's buildx: `ls` answers with real
// `buildx ls --format json` output (docker/testdata), `inspect` with a
// line naming the builder, and every other verb succeeds unless fail names
// it. Nothing creates, starts or removes a real builder. It returns the
// argv of every call after `buildx`.
func fakeBuildx(t *testing.T, a *App, fail map[string]error) *[]string {
	t.Helper()
	ls, err := os.ReadFile("../docker/testdata/buildx-ls.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	a.buildxRun = func(_ context.Context, args ...string) ([]byte, error) {
		i := slices.Index(args, "buildx")
		if i < 0 {
			t.Fatalf("not a buildx call: %q", args)
		}
		call := strings.Join(args[i+1:], " ")
		calls = append(calls, call)
		verb := args[i+1]
		if err := fail[verb]; err != nil {
			return nil, err
		}
		switch verb {
		case "ls":
			return ls, nil
		case "inspect":
			return []byte("Name: " + args[len(args)-1] + "\nDriver: docker\n"), nil
		case "prune":
			return []byte("ID\tRECLAIMABLE\tSIZE\nTotal:\t1.2GB\n"), nil
		case "create":
			return []byte("brave_hopper\n"), nil
		}
		return nil, nil
	}
	return &calls
}

// openBuildersView opens :builders over the fake.
func openBuildersView(t *testing.T, fail map[string]error) (*App, *[]string) {
	t.Helper()
	a := newTestApp(t)
	calls := fakeBuildx(t, a, fail)
	_, cmd := a.dispatchCommand("builders")
	runCmd(a, cmd)
	if a.view != style.ViewBuilders {
		t.Fatalf(":builders opened %s (err %q)", style.ViewName(a.view), a.errFlash)
	}
	return a, calls
}

// lastCall is the most recent buildx argv, "" if none.
func lastCall(calls *[]string) string {
	if len(*calls) == 0 {
		return ""
	}
	return (*calls)[len(*calls)-1]
}

// TestBuildersView: :builders lists every builder buildx knows, from
// `buildx ls --format json`, with its driver, endpoint, status, BuildKit
// version and platforms, and why a broken one is broken.
func TestBuildersView(t *testing.T) {
	a, calls := openBuildersView(t, nil)
	if lastCall(calls) != "ls --format=json" {
		t.Errorf("listed with %q", *calls)
	}
	out := render(a)
	for _, want := range []string{
		"ci-builder", "docker-container", "k8s-remote", "kubernetes", "stopped",
		"colima", "running", "v0.30.0", "linux/arm64", "desktop-linux", "failed to connect",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("builders view is missing %q:\n%s", want, out)
		}
	}
	marked := 0
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "▸") {
			marked++
			if !strings.Contains(line, "colima") {
				t.Errorf("marked a builder other than the current one: %q", line)
			}
		}
	}
	if marked != 1 {
		t.Errorf("%d builders marked, want the current one:\n%s", marked, out)
	}
	for _, alias := range []string{"builder", "buildx"} {
		a := newTestApp(t)
		fakeBuildx(t, a, nil)
		_, cmd := a.dispatchCommand(alias)
		runCmd(a, cmd)
		if a.view != style.ViewBuilders {
			t.Errorf(":%s opened %s", alias, style.ViewName(a.view))
		}
	}
}

// TestBuildersListFailure: a listing buildx refuses is said in the view.
func TestBuildersListFailure(t *testing.T) {
	a, _ := openBuildersView(t, map[string]error{"ls": errors.New("unknown command: docker buildx")})
	if out := render(a); !strings.Contains(out, "buildx ls: unknown command") {
		t.Errorf("the failure is not shown:\n%s", out)
	}
}

// TestBuilderVerbs: u, x and U on the selected builder run inspect
// --bootstrap, stop and use on it, for the daemon on screen, and the list
// is read again after each.
func TestBuilderVerbs(t *testing.T) {
	a, calls := openBuildersView(t, nil)
	for _, c := range []struct {
		key, call, flash string
	}{
		{key: "u", call: "inspect --bootstrap -- ci-builder", flash: "started builder ci-builder"},
		{key: "x", call: "stop -- ci-builder", flash: "stopped builder ci-builder"},
		{key: "U", call: "use -- ci-builder", flash: "now building with ci-builder"},
	} {
		*calls = nil
		runCmd(a, step(a, key(c.key)))
		if len(*calls) != 2 || (*calls)[0] != c.call || (*calls)[1] != "ls --format=json" {
			t.Errorf("%s ran %q, want %q then a listing", c.key, *calls, c.call)
		}
		if a.flash != c.flash || a.errFlash != "" {
			t.Errorf("%s: flash %q, err %q", c.key, a.flash, a.errFlash)
		}
	}
}

// TestBuilderVerbFailure: buildx's reason reaches the flash.
func TestBuilderVerbFailure(t *testing.T) {
	a, _ := openBuildersView(t, map[string]error{"stop": errors.New("no builder \"ci-builder\" found")})
	runCmd(a, step(a, key("x")))
	if !strings.Contains(a.errFlash, `buildx stop: no builder "ci-builder" found`) {
		t.Errorf("err %q", a.errFlash)
	}
}

// TestBuilderInspect: enter and o show `buildx inspect` in the inspect
// view; enter does until :builds (#118) gives it the build history.
func TestBuilderInspect(t *testing.T) {
	for _, k := range []string{"o", "enter"} {
		a, calls := openBuildersView(t, nil)
		runCmd(a, step(a, key(k)))
		if a.view != style.ViewInspect || lastCall(calls) != "inspect -- ci-builder" {
			t.Fatalf("%s: view %s, calls %q", k, style.ViewName(a.view), *calls)
		}
		if out := render(a); !strings.Contains(out, "Name: ci-builder") {
			t.Errorf("%s: inspect output missing:\n%s", k, out)
		}
	}
}

// TestBuilderRemove: ctrl-d confirms naming the builder and runs buildx rm
// only on yes; a docker context's own builder is refused in place, with no
// confirm, since buildx will not remove it.
func TestBuilderRemove(t *testing.T) {
	a, calls := openBuildersView(t, nil)
	*calls = nil
	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "remove builder ci-builder?") {
		t.Fatalf("ctrl-d asked %q (active %v, err %q)", a.confirm.Prompt(), a.confirm.Active(), a.errFlash)
	}
	if len(*calls) != 0 {
		t.Fatalf("ran before the confirm: %q", *calls)
	}
	runCmd(a, step(a, key("y")))
	if len(*calls) == 0 || (*calls)[0] != "rm -- ci-builder" {
		t.Errorf("calls %q", *calls)
	}
	if a.flash != "removed builder ci-builder" {
		t.Errorf("flash %q, err %q", a.flash, a.errFlash)
	}

	// colima is the third row: the current context's docker-driver builder.
	*calls = nil
	step(a, key("down"))
	step(a, key("down"))
	step(a, key("ctrl+d"))
	if a.confirm.Active() || !strings.Contains(a.errFlash, "colima is docker context colima's own builder") {
		t.Errorf("a context builder: confirm %v, err %q", a.confirm.Active(), a.errFlash)
	}
	if len(*calls) != 0 {
		t.Errorf("ran anyway: %q", *calls)
	}
}

// TestBuilderPrune: P confirms, then prunes that builder's cache and puts
// the total freed in the flash.
func TestBuilderPrune(t *testing.T) {
	a, calls := openBuildersView(t, nil)
	*calls = nil
	step(a, key("P"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "prune builder ci-builder's build cache?") {
		t.Fatalf("P asked %q (active %v)", a.confirm.Prompt(), a.confirm.Active())
	}
	runCmd(a, step(a, key("y")))
	if len(*calls) == 0 || (*calls)[0] != "prune --builder=ci-builder --force" {
		t.Errorf("calls %q", *calls)
	}
	if a.flash != "pruned the build cache of ci-builder — Total: 1.2GB" {
		t.Errorf("flash %q, err %q", a.flash, a.errFlash)
	}
}

// TestBuilderCreate: ctrl-n opens the form; a name buildx would refuse is
// refused in the form; a good one runs buildx create with the driver
// chosen, returns to the list and reads it again.
func TestBuilderCreate(t *testing.T) {
	a, calls := openBuildersView(t, nil)
	step(a, key("ctrl+n"))
	if a.view != style.ViewBuilderForm {
		t.Fatalf("ctrl-n opened %s", style.ViewName(a.view))
	}
	typeText(a, "-x")
	step(a, key("enter"))
	if a.view != style.ViewBuilderForm || !strings.Contains(render(a), "name:") {
		t.Fatalf("a flag-shaped name was not refused in the form:\n%s", render(a))
	}
	clearField(a, 2)
	typeText(a, "ci2")
	step(a, tab)
	step(a, keyRight) // docker-container → kubernetes
	step(a, tab)
	typeText(a, "linux/amd64")
	*calls = nil
	runCmd(a, step(a, key("enter")))
	want := []string{"create --driver=kubernetes --name=ci2 --platform=linux/amd64", "ls --format=json"}
	if !slices.Equal(*calls, want) {
		t.Errorf("calls %q, want %q", *calls, want)
	}
	if a.view != style.ViewBuilders || a.flash != "created builder brave_hopper" {
		t.Errorf("after create: view %s, flash %q, err %q", style.ViewName(a.view), a.flash, a.errFlash)
	}
}

// TestBuilderCreateRefused: buildx's refusal lands in the form, to fix.
func TestBuilderCreateRefused(t *testing.T) {
	a, _ := openBuildersView(t, map[string]error{"create": errors.New(`existing instance for "ci2"`)})
	step(a, key("ctrl+n"))
	typeText(a, "ci2")
	runCmd(a, step(a, key("enter")))
	if a.view != style.ViewBuilderForm || !strings.Contains(render(a), `existing instance for "ci2"`) {
		t.Errorf("the refusal is not in the form: view %s\n%s", style.ViewName(a.view), render(a))
	}
}

// TestBuildersReadonly: --readonly refuses every builder change and runs
// none of them; listing and inspecting still work.
func TestBuildersReadonly(t *testing.T) {
	a, calls := openBuildersView(t, nil)
	a.readonly = true
	*calls = nil
	for _, k := range []string{"u", "x", "U", "P", "ctrl+d", "ctrl+n"} {
		a.errFlash = ""
		runCmd(a, step(a, key(k)))
		if a.confirm.Active() || a.view != style.ViewBuilders || !strings.Contains(a.errFlash, "readonly mode") {
			t.Errorf("%s: confirm %v, view %s, err %q", k, a.confirm.Active(), style.ViewName(a.view), a.errFlash)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("ran anyway: %q", *calls)
	}
	runCmd(a, step(a, key("o")))
	if a.view != style.ViewInspect {
		t.Errorf("o under --readonly opened %s", style.ViewName(a.view))
	}
}
