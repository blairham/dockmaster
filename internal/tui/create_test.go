// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// TestCreateVolumeForm: ctrl-n in volumes opens the volume form — on an
// empty list too — a bad label is refused in the form, and a good spec is
// submitted as typed.
func TestCreateVolumeForm(t *testing.T) {
	a := newTestApp(t)
	step(a, key("2"))
	step(a, views.VolumesRefreshMsg{Volumes: nil})
	step(a, key("ctrl+n"))
	if a.view != style.ViewCreateForm {
		t.Fatalf("ctrl-n in volumes opened %v, want the create form", a.view)
	}
	f := typedView[*views.CreateFormView](a, style.ViewCreateForm)
	if f.Kind() != views.CreateVolume || !strings.Contains(render(a), "local") {
		t.Fatalf("form %q:\n%s", f.Kind(), render(a))
	}

	typeText(a, "pgdata")
	step(a, keyTab)
	step(a, keyTab)
	typeText(a, "type=tmpfs")
	step(a, keyTab)
	typeText(a, "nokey")
	step(a, key("enter"))
	if a.view != style.ViewCreateForm || !strings.Contains(render(a), "label") {
		t.Fatalf("a bad label was not refused in the form:\n%s", render(a))
	}
	for range len("nokey") {
		step(a, keyBackspace)
	}
	typeText(a, "team=db")
	if act, param := f.HandleKey("enter"); act != "volume_create" ||
		!strings.Contains(
			param,
			`"pgdata"`,
		) || !strings.Contains(param, `"type=tmpfs"`) || !strings.Contains(param, `"team=db"`) {
		t.Errorf("enter = %q %q", act, param)
	}
}

// TestCreateNetworkForm: ctrl-n on a network row opens the network form;
// without a swarm, bridge is the only driver and attachable stays locked.
func TestCreateNetworkForm(t *testing.T) {
	a := newTestApp(t)
	step(a, key("3"))
	step(a, views.NetworksRefreshMsg{Networks: []docker.Network{{ID: "n1", Name: "bridge", Driver: "bridge"}}})
	step(a, key("ctrl+n"))
	f := typedView[*views.CreateFormView](a, style.ViewCreateForm)
	if a.view != style.ViewCreateForm || f.Kind() != views.CreateNetwork {
		t.Fatalf("ctrl-n in networks opened %v", a.view)
	}
	if strings.Contains(render(a), "‹ bridge") {
		t.Errorf("overlay offered without a swarm:\n%s", render(a))
	}
	typeText(a, "backend")
	step(a, keyTab) // the driver is locked, so this is Subnet
	typeText(a, "172.30.0.0/16")
	step(a, keyTab)
	typeText(a, "10.0.0.1")
	step(a, key("enter"))
	if a.view != style.ViewCreateForm || !strings.Contains(render(a), "not in 172.30.0.0/16") {
		t.Fatalf("a gateway outside the subnet was not refused:\n%s", render(a))
	}
	for range len("10.0.0.1") {
		step(a, keyBackspace)
	}
	step(a, keyTab)
	step(a, key("space")) // Internal: yes
	step(a, keyTab)       // Attachable is locked: Labels
	typeText(a, "x=1")
	spec := f.NetworkSpec()
	if spec.Name != "backend" || spec.Driver != "bridge" || spec.Subnet != "172.30.0.0/16" || spec.Gateway != "" ||
		!spec.Internal || spec.Attachable || strings.Join(spec.Labels, ",") != "x=1" {
		t.Errorf("spec = %+v", spec)
	}
	if act, _ := f.HandleKey("enter"); act != "network_create" {
		t.Errorf("enter = %q", act)
	}

	// On a swarm manager overlay is a choice, and choosing it unlocks
	// attachable.
	sw := views.NewNetworkForm(true)
	sw.UpdateTable(keyTab)
	sw.UpdateTable(key("space"))
	if sw.NetworkSpec().Driver != "overlay" {
		t.Fatalf("driver = %q, want overlay", sw.NetworkSpec().Driver)
	}
	sw.UpdateTable(keyTab)
	sw.UpdateTable(keyTab)
	sw.UpdateTable(keyTab)
	sw.UpdateTable(keyTab) // Attachable, now unlocked
	sw.UpdateTable(key("space"))
	if !sw.NetworkSpec().Attachable {
		t.Errorf("attachable did not unlock for overlay: %+v", sw.NetworkSpec())
	}
}

// TestCreateCommands: :create volume / :create network open the forms from
// any view over their list; :create alone follows the view on screen.
func TestCreateCommands(t *testing.T) {
	a := newTestApp(t)
	if msg, _ := a.dispatchCommand("create volume"); msg != "" || a.view != style.ViewCreateForm {
		t.Fatalf(":create volume = %q, view %v", msg, a.view)
	}
	if len(a.viewStack) != 1 || a.viewStack[0] != style.ViewVolumes {
		t.Errorf("the volume form is not over the volumes list: %v", a.viewStack)
	}
	step(a, key("esc"))
	if a.view != style.ViewVolumes {
		t.Errorf("esc from the form landed on %v", a.view)
	}

	a.dispatchCommand("create net")
	if f := typedView[*views.CreateFormView](a, style.ViewCreateForm); a.view != style.ViewCreateForm ||
		f.Kind() != views.CreateNetwork || a.viewStack[0] != style.ViewNetworks {
		t.Fatalf(":create net: view %v stack %v", a.view, a.viewStack)
	}

	a.dispatchCommand("containers")
	if msg, _ := a.dispatchCommand("create"); !strings.Contains(msg, "volumes or networks") {
		t.Errorf(":create in containers = %q", msg)
	}
	a.dispatchCommand("volumes")
	a.dispatchCommand("create")
	if f := typedView[*views.CreateFormView](a, style.ViewCreateForm); a.view != style.ViewCreateForm ||
		f.Kind() != views.CreateVolume {
		t.Errorf(":create in volumes: view %v", a.view)
	}

	for _, c := range []string{"create", "create volume", "create vol", "create network", "create net"} {
		if err := ValidateCommand(c, nil); err != nil {
			t.Errorf("ValidateCommand(%q) = %v", c, err)
		}
	}
}

// TestCreateResult: a refusal lands in the form; success returns to the
// list with a flash.
func TestCreateResult(t *testing.T) {
	a := newTestApp(t)
	a.dispatchCommand("create network")
	step(
		a,
		createDoneMsg{
			kind: views.CreateNetwork,
			name: "backend",
			err:  errors.New("network with name backend already exists"),
		},
	)
	if a.view != style.ViewCreateForm || !strings.Contains(render(a), "already exists") {
		t.Fatalf("a refusal did not land in the form:\n%s", render(a))
	}
	step(a, createDoneMsg{kind: views.CreateNetwork, name: "backend"})
	if a.view != style.ViewNetworks || a.flash != "created network backend" {
		t.Errorf("after create: view %v flash %q", a.view, a.flash)
	}
}

// TestCreateIsReadonly: every way in is refused under --readonly.
func TestCreateIsReadonly(t *testing.T) {
	a := NewApp(nil, Options{ReadOnly: true})
	a.splashActive, a.loading = false, false
	step(a, key("2"))
	step(a, key("ctrl+n"))
	if a.view == style.ViewCreateForm || !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("--readonly opened the form: view %v flash %q", a.view, a.errFlash)
	}
	for _, act := range []string{"create_form", "volume_create", "network_create"} {
		a.errFlash = ""
		a.handleAction(act, "{}")
		if !strings.Contains(a.errFlash, "readonly") {
			t.Errorf("--readonly let %s through: %q", act, a.errFlash)
		}
	}
}
