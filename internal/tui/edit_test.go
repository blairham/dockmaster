// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

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

// TestEditForm: e reads the container's settings and opens the form on
// them; a bad size is refused in the form; enter submits what was typed.
func TestEditForm(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	if act, _ := a.activeView().HandleKey("e"); act != "edit_form" {
		t.Fatal("e on a container has no action")
	}
	step(a, editStateMsg{id: "aaaaaaaaaaaa1111", state: docker.EditState{
		Name: "web", Restart: "no", Memory: 256 << 20, MemorySwap: 512 << 20,
	}})
	if a.view != style.ViewEditForm {
		t.Fatalf("the settings did not open the form: %v", a.view)
	}
	out := render(a)
	for _, want := range []string{"edit web", "[web", "256m", "‹ no ›", "unlimited now"} {
		if !strings.Contains(out, want) {
			t.Errorf("edit form is missing %q:\n%s", want, out)
		}
	}

	f := typedView[*views.EditFormView](a, style.ViewEditForm)
	step(a, tab) // CPUs
	typeText(a, "1.5")
	step(a, tab) // Memory
	for range 4 {
		step(a, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(a, "lots")
	step(a, key("enter"))
	if a.view != style.ViewEditForm || !strings.Contains(render(a), "is not a size") {
		t.Fatalf("a bad size was not refused in the form:\n%s", render(a))
	}
	for range 4 {
		step(a, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(a, "1g")
	step(a, tab) // Restart
	step(a, tea.KeyPressMsg{Code: tea.KeyRight})
	step(a, tea.KeyPressMsg{Code: tea.KeyRight})
	if s := f.Spec(); s.CPUs != "1.5" || s.Memory != "1g" || s.Restart != "unless-stopped" || s.Name != "web" {
		t.Fatalf("spec %+v", s)
	}
	if act, param := f.HandleKey("enter"); act != "edit_apply" || !strings.Contains(param, `"memory":"1g"`) {
		t.Errorf("enter = %q %q", act, param)
	}
}

func TestEditResult(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	step(a, editStateMsg{id: "x", state: docker.EditState{Name: "web", Restart: "no"}})
	step(a, editDoneMsg{name: "web", err: errors.New("Conflicting options: Nano CPUs and CPU Period cannot both be set")})
	if a.view != style.ViewEditForm || !strings.Contains(render(a), "Nano CPUs and CPU Period") {
		t.Fatalf("a refused edit did not land in the form:\n%s", render(a))
	}
	step(a, editDoneMsg{name: "web"})
	if a.view != style.ViewContainers || !strings.Contains(a.flash, "updated web") {
		t.Errorf("after an edit: view %v flash %q", a.view, a.flash)
	}

	// The restart choice opens on the container's own policy — on-failure
	// here, so it cannot pass by being the first choice.
	step(a, editStateMsg{id: "x", state: docker.EditState{Name: "web", Restart: "on-failure"}})
	if !strings.Contains(render(a), "‹ on-failure ›") {
		t.Errorf("restart policy not prefilled:\n%s", render(a))
	}

	ro := NewApp(nil, Options{ReadOnly: true})
	ro.handleAction("edit_form", "x")
	if !strings.Contains(ro.errFlash, "readonly") {
		t.Errorf("--readonly opened the edit form: %q", ro.errFlash)
	}
}
