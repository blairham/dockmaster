// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeDockerContextRm puts a docker on PATH that writes each argument to a
// file, one per line, and removes the prod context's metadata as the real
// `docker context rm prod` would — so the refresh after has something to
// show. It returns the file.
func fakeDockerContextRm(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	sum := sha256.Sum256([]byte("prod"))
	meta := filepath.Join(os.Getenv("DOCKER_CONFIG"), "contexts", "meta", hex.EncodeToString(sum[:]))
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > '" + argv + "'\nrm -rf '" + meta + "'\n"
	if err := os.WriteFile(
		filepath.Join(dir, "docker"),
		[]byte(script),
		0o700,
	); err != nil { //nolint:gosec // an executable test fake
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argv
}

// openContexts opens :ctx on a store holding default and prod, with
// dockmaster connected through current, and puts the cursor on name.
func openContexts(t *testing.T, a *App, current, name string) *views.ContextsView {
	t.Helper()
	a.client = &docker.Client{ContextName: current, Host: "unix:///nonexistent/" + current + ".sock"}
	_, cmd := a.dispatchCommand("ctx")
	runCmd(a, cmd)
	cv := typedView[*views.ContextsView](a, style.ViewContexts)
	if cv == nil || a.view != style.ViewContexts || cv.Count() != 2 {
		t.Fatalf(":ctx: view %s, %d contexts", style.ViewName(a.view), cv.Count())
	}
	for range 2 {
		if c, _ := cv.Selected(); c.Name == name {
			return cv
		}
		step(a, key("j"))
	}
	t.Fatalf("no context %q in the list", name)
	return nil
}

// TestContextRemove: ctrl-d on a context confirms, naming it, then runs
// `docker context rm <name>` — the name one argument — and refreshes the
// list, which no longer has it.
func TestContextRemove(t *testing.T) {
	contextStore(t)
	argv := fakeDockerContextRm(t)
	a := newTestApp(t)
	cv := openContexts(t, a, "here", "prod")
	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "remove docker context prod?") {
		t.Fatalf("ctrl-d asked %q (active %v, err %q)", a.confirm.Prompt(), a.confirm.Active(), a.errFlash)
	}
	if _, err := os.Stat(argv); err == nil {
		t.Fatal("docker ran before the confirm was answered")
	}
	runCmd(a, step(a, key("y")))
	if got := read(t, argv); got != "context\nrm\nprod\n" {
		t.Errorf("docker was given %q, want context rm prod", got)
	}
	if a.flash != "removed context prod" || a.errFlash != "" {
		t.Errorf("flash %q, err %q", a.flash, a.errFlash)
	}
	if cv.Count() != 1 || strings.Contains(render(a), "prod.sock") {
		t.Errorf("the list was not refreshed: %d contexts\n%s", cv.Count(), render(a))
	}
}

// TestContextRemoveRefused: default, the context dockmaster is connected
// through and the CLI's current one are refused with the reason, and
// --readonly refuses any — none of them asks, and docker never runs.
func TestContextRemoveRefused(t *testing.T) {
	for _, tc := range []struct {
		name, current, cliContext, pick, readonly, want string
	}{
		{name: "default", current: "here", pick: "default", want: "built-in"},
		{name: "connected", current: "prod", pick: "prod", want: "dockmaster is connected through prod"},
		{name: "cli current", current: "here", cliContext: "prod", pick: "prod", want: "docker CLI's current context"},
		{name: "readonly", current: "here", pick: "prod", readonly: "yes", want: "readonly mode — remove context refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contextStore(t)
			t.Setenv("DOCKER_CONTEXT", tc.cliContext)
			argv := fakeDockerContextRm(t)
			a := newTestApp(t)
			a.readonly = tc.readonly != ""
			openContexts(t, a, tc.current, tc.pick)
			runCmd(a, step(a, key("ctrl+d")))
			if a.confirm.Active() || !strings.Contains(a.errFlash, tc.want) {
				t.Errorf("confirm %v, err %q, want %q", a.confirm.Active(), a.errFlash, tc.want)
			}
			if _, err := os.Stat(argv); err == nil {
				t.Errorf("docker ran: %q", read(t, argv))
			}
		})
	}
}
