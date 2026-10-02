package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

func loadImages(a *App) {
	a.dispatchCommand("images")
	step(a, views.ImagesRefreshMsg{Images: []docker.Image{
		{ID: "sha256:aaaa", Repo: "nginx", Tag: "1.27"},
		{ID: "sha256:bbbb", Repo: "<none>", Tag: "<none>", Dangling: true},
	}})
}

var tab = tea.KeyPressMsg{Code: tea.KeyTab}

// TestRunFormFromAnImage: u on an image opens the run form on it; a bad
// port is refused in the form, and a good spec is submitted as typed.
func TestRunFormFromAnImage(t *testing.T) {
	a := newTestApp(t)
	loadImages(a)
	step(a, key("u"))
	if a.view != style.ViewRunForm {
		t.Fatalf("u on an image opened %v, want the run form", a.view)
	}
	f := typedView[*views.RunFormView](a, style.ViewRunForm)
	if f.Title() != "run nginx:1.27" || !strings.Contains(render(a), "nginx:1.27") {
		t.Errorf("run form for %q", f.Title())
	}

	typeText(a, "web") // focus starts on Name
	step(a, tab)
	typeText(a, "8080:http") // Ports
	step(a, key("enter"))
	if a.view != style.ViewRunForm || !strings.Contains(render(a), "ports") {
		t.Fatalf("a bad port was not refused in the form:\n%s", render(a))
	}

	for range len("http") {
		step(a, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(a, "80")
	step(a, tab)
	typeText(a, "A=1 B=two")
	spec := f.Spec()
	if spec.Name != "web" || strings.Join(spec.Ports, ",") != "8080:80" || strings.Join(spec.Env, ",") != "A=1,B=two" ||
		spec.Remove {
		t.Fatalf("spec = %+v", spec)
	}
	if act, param := f.HandleKey("enter"); act != "run_create" || !strings.Contains(param, `"8080:80"`) {
		t.Errorf("enter = %q %q", act, param)
	}
}

// TestRunResult: a failure lands in the form to be fixed; success goes to
// the new container's logs over the containers list.
func TestRunResult(t *testing.T) {
	a := newTestApp(t)
	loadImages(a)
	step(a, key("u"))
	spec := docker.RunSpec{Image: "nginx:1.27", Name: "web"}

	step(a, runDoneMsg{spec: spec, err: errors.New("port is already allocated")})
	if a.view != style.ViewRunForm || !strings.Contains(render(a), "port is already allocated") {
		t.Fatalf("a run failure did not land in the form:\n%s", render(a))
	}

	step(a, runDoneMsg{spec: spec, id: "cafef00d1234"})
	lv := typedView[*views.LogsView](a, style.ViewLogs)
	if a.view != style.ViewLogs || lv.ContainerID() != "cafef00d1234" || lv.Title() != "web" {
		t.Fatalf("after a run: view %v, logs of %q titled %q", a.view, lv.ContainerID(), lv.Title())
	}
	step(a, key("esc"))
	if a.view != style.ViewContainers {
		t.Errorf("esc from the new container's logs went to %v, want containers", a.view)
	}
}

func TestRunGuards(t *testing.T) {
	ro := newSizedApp(t, Options{ReadOnly: true, Splashless: true}, 160, 44)
	ro.loading = false
	loadImages(ro)
	step(ro, key("u"))
	if ro.view == style.ViewRunForm || !strings.Contains(ro.errFlash, "readonly") {
		t.Errorf("--readonly opened the run form (view %v flash %q)", ro.view, ro.errFlash)
	}

	a := newTestApp(t)
	loadImages(a)
	step(a, key("j")) // the dangling image
	step(a, key("u"))
	if f := typedView[*views.RunFormView](a, style.ViewRunForm); f == nil || f.Spec().Image != "sha256:bbbb" {
		t.Errorf("a dangling image is run by %v, want its ID", f.Spec().Image)
	}
	step(a, key("esc"))
	if a.view != style.ViewImages {
		t.Errorf("esc from the run form went to %v", a.view)
	}
}
