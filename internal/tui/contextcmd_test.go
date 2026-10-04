// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

func TestCutContextArg(t *testing.T) {
	for _, tc := range []struct {
		in, name, rest string
		ok             bool
	}{
		{in: "containers @prod", name: "prod", rest: "containers", ok: true},
		{in: "@prod images /nginx", name: "prod", rest: "images /nginx", ok: true},
		{in: "volumes @prod /a @b", name: "prod", rest: "volumes /a @b", ok: true},
		{in: "containers /user@host", rest: "containers /user@host"},
		{in: "containers @", rest: "containers @"},
		{in: "containers", rest: "containers"},
	} {
		name, rest, ok := cutContextArg(tc.in)
		if name != tc.name || rest != tc.rest || ok != tc.ok {
			t.Errorf("cutContextArg(%q) = %q, %q, %v; want %q, %q, %v", tc.in, name, rest, ok, tc.name, tc.rest, tc.ok)
		}
	}
}

// contextStore points DOCKER_CONFIG at a temp store holding one context,
// prod, on a socket nothing listens on.
func contextStore(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "")
	sum := sha256.Sum256([]byte("prod"))
	meta := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(meta, 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{
		"Name":      "prod",
		"Endpoints": map[string]any{"docker": map[string]any{"Host": "unix:///nonexistent/prod.sock"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta, "meta.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestViewAtContextSwitchesThenLands: `:images @prod /nginx` dials prod
// and asks the switch to land on images filtered to nginx, as k9s's
// `:pods @ctx` switches context and opens the view.
func TestViewAtContextSwitchesThenLands(t *testing.T) {
	contextStore(t)
	a := newTestApp(t)
	a.client = &docker.Client{ContextName: "here", Host: "unix:///nonexistent/here.sock"}
	errMsg, cmd := a.dispatchCommand("images @PROD /nginx")
	if errMsg != "" || cmd == nil {
		t.Fatalf("dispatch: err %q, cmd %v", errMsg, cmd)
	}
	msg, ok := cmd().(switchContextMsg)
	if !ok {
		t.Fatalf("the command did not switch context: %T", cmd())
	}
	if msg.name != "prod" || msg.land == nil || msg.land.view != style.ViewImages || msg.land.filter != "nginx" {
		t.Fatalf("switch %q landing %+v", msg.name, msg.land)
	}
	if msg.err == nil {
		t.Error("dialed a socket nothing listens on without an error")
	}
}

// TestSwitchLandsWhereAsked: the switch's result opens the asked-for view
// and filter; without a landing it opens containers, unfiltered, as before.
func TestSwitchLandsWhereAsked(t *testing.T) {
	for _, tc := range []struct {
		land       *landing
		name       string
		wantFilter string
		wantView   style.ViewType
	}{
		{name: "images", land: &landing{view: style.ViewImages, filter: "nginx"}, wantView: style.ViewImages, wantFilter: "nginx"},
		{name: "containers", land: &landing{view: style.ViewContainers, filter: "web"}, wantView: style.ViewContainers, wantFilter: "web"},
		{name: "no landing", wantView: style.ViewContainers},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			a.switchView(style.ViewVolumes)
			a.filter = "stale"
			c, err := docker.New("unix:///nonexistent/prod.sock")
			if err != nil {
				t.Fatal(err)
			}
			a.applySwitchContext(switchContextMsg{name: "prod", client: c, land: tc.land})
			if a.view != tc.wantView || a.filter != tc.wantFilter {
				t.Errorf("landed on %s filtered %q; want %s filtered %q",
					style.ViewName(a.view), a.filter, style.ViewName(tc.wantView), tc.wantFilter)
			}
		})
	}
}

// TestViewAtContextRefusals: an unknown context and a command that is not
// a view are refused with the reason, and nothing is dialed.
func TestViewAtContextRefusals(t *testing.T) {
	contextStore(t)
	a := newTestApp(t)
	for in, want := range map[string]string{
		"images @nope":  "no such docker context: nope",
		"aliases @prod": "goes with a view",
	} {
		errMsg, cmd := a.dispatchCommand(in)
		if !strings.Contains(errMsg, want) || cmd != nil {
			t.Errorf("%q: err %q, cmd %v; want %q and no command", in, errMsg, cmd, want)
		}
	}
}

// TestViewAtCurrentContextDoesNotRedial: naming the context on screen
// opens the view in place, with the same client.
func TestViewAtCurrentContextDoesNotRedial(t *testing.T) {
	contextStore(t)
	a := newTestApp(t)
	c := &docker.Client{ContextName: "prod", Host: "unix:///nonexistent/prod.sock"}
	a.client = c
	if errMsg, _ := a.dispatchCommand("volumes @prod /data"); errMsg != "" {
		t.Fatal(errMsg)
	}
	if a.view != style.ViewVolumes || a.filter != "data" || a.client != c {
		t.Errorf("view %s filter %q, client replaced %v", style.ViewName(a.view), a.filter, a.client != c)
	}
}
