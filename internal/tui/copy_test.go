package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// TestCopyForm: C opens docker cp for the selected container; the paths
// are checked in the form, and the direction picks the action.
func TestCopyForm(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	step(a, key("C"))
	if a.view != style.ViewCopyForm {
		t.Fatalf("C opened %v, want the copy form", a.view)
	}
	f := typedView[*views.CopyFormView](a, style.ViewCopyForm)
	if f.Title() != "copy web" {
		t.Errorf("title %q", f.Title())
	}

	typeText(a, "etc/nginx") // not absolute
	step(a, key("enter"))
	if a.view != style.ViewCopyForm || !strings.Contains(render(a), "absolute path") {
		t.Fatalf("a relative container path was not refused:\n%s", render(a))
	}
	step(a, tea.KeyPressMsg{Code: tea.KeyHome})
	typeText(a, "/")
	if act, param := f.HandleKey("enter"); act != "copy_from" || !strings.Contains(param, `"/etc/nginx"`) {
		t.Errorf("container → here: %q %q", act, param)
	}

	step(a, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}) // back to Direction
	step(a, tea.KeyPressMsg{Code: tea.KeyRight})
	if s := f.Spec(); !s.Into || s.Container != "/etc/nginx" || s.Local != "." {
		t.Fatalf("after flipping: %+v", s)
	}
	if act, _ := f.HandleKey("enter"); act != "copy_into" {
		t.Errorf("here → container submitted %q", act)
	}
}

// TestCopyReadonly: copying out only reads the container, so --readonly
// allows it; copying in changes it, so --readonly refuses.
func TestCopyReadonly(t *testing.T) {
	a := NewApp(nil, Options{ReadOnly: true})
	spec := `{"id":"x","name":"web","container":"/etc","local":".","into":true}`
	a.handleAction("copy_into", spec)
	if !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("copy_into under --readonly: flash %q", a.errFlash)
	}
	a.errFlash = ""
	if _, cmd := a.handleAction(
		"copy_from",
		strings.Replace(spec, `"into":true`, `"into":false`, 1),
	); cmd == nil ||
		a.errFlash != "" {
		t.Errorf("copy_from under --readonly was refused: flash %q", a.errFlash)
	}
}

func TestCopyResult(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	step(a, key("C"))
	spec := views.CopySpec{ID: "x", Name: "web", Container: "/nope", Local: "./out"}
	step(a, copyDoneMsg{spec: spec, err: errors.New("Could not find the file /nope in container web")})
	if a.view != style.ViewCopyForm || !strings.Contains(render(a), "Could not find the file /nope") {
		t.Fatalf("a failed copy did not land in the form:\n%s", render(a))
	}
	spec.Container = "/etc"
	step(a, copyDoneMsg{spec: spec})
	if a.view != style.ViewContainers || !strings.Contains(a.flash, "copied web:/etc → ") ||
		!strings.HasSuffix(a.flash, "/out") {
		t.Errorf("after a copy: view %v flash %q", a.view, a.flash)
	}
}
