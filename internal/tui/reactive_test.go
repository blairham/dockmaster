// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/theme"

	"github.com/blairham/dockmaster/internal/tui/style"
)

// reactiveApp watches a temporary config directory and reloads with next.
func reactiveApp(t *testing.T, next func() (Reloaded, error)) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "config.yaml"), "dockmaster: {}\n")
	base := style.Base()
	t.Cleanup(func() { style.SetBase(base) })
	a := optsApp(t, Options{WatchDir: dir, Reload: next})
	return a, dir
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// A distinct mtime even on a coarse filesystem clock.
	later := time.Now().Add(time.Duration(len(body)) * time.Second)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
}

// reloadNow is two ticks with the directory unchanged in between: the
// first sees the change, the second — the change having held still —
// reloads, and the result is applied.
func reloadNow(t *testing.T, a *App) {
	t.Helper()
	if cmd := a.checkReload(); cmd != nil {
		t.Fatal("reloaded on the tick that first saw the change; it must hold still first")
	}
	cmd := a.checkReload()
	if cmd == nil {
		t.Fatal("no reload after the change held still")
	}
	step(a, cmd())
}

// TestReactiveReload: a saved change applies without a restart (#38) —
// aliases, settings, the skin — but only once it has held still for a
// tick, and the bars keep their history through the reskin.
func TestReactiveReload(t *testing.T) {
	skin := theme.Default()
	skin.Logo = theme.Default().Status.Error
	a, dir := reactiveApp(t, func() (Reloaded, error) {
		return Reloaded{Theme: skin, Options: Options{
			Aliases: map[string]string{"zzpg": "containers /postgres"}, LogWrap: true, Shell: "zsh",
			HostShellImage: "registry.local/tools:1",
		}}, nil
	})
	a.commandBar.SetHistory([]string{"images"})

	if cmd := a.checkReload(); cmd != nil {
		t.Fatal("reloaded with nothing changed")
	}
	write(t, filepath.Join(dir, "config.yaml"), "dockmaster: {shell: zsh}\n")
	reloadNow(t, a)

	if a.aliases["zzpg"] == "" || !a.logWrap || a.shell != "zsh" || a.hostShellImage != "registry.local/tools:1" ||
		a.flash != "config reloaded" {
		t.Errorf("not applied: aliases %v wrap %v shell %q flash %q", a.aliases, a.logWrap, a.shell, a.flash)
	}
	if style.Base().Logo != skin.Logo {
		t.Error("the skin was not applied")
	}
	if got := strings.Join(a.commandBar.History(), ","); got != "images" {
		t.Errorf("the reskin lost the command history: %q", got)
	}
	step(a, key(":"))
	typeText(a, "zz") // no built-in command starts so, so only the alias can match
	if !strings.Contains(render(a), "zzpg") {
		t.Errorf("the command bar does not suggest the reloaded alias:\n%s", render(a))
	}
}

// TestReactiveBadFile: a file that does not load leaves the running config
// exactly as it was, and says why.
func TestReactiveBadFile(t *testing.T) {
	a, dir := reactiveApp(
		t,
		func() (Reloaded, error) { return Reloaded{}, errors.New("config.yaml: line 2: unknown key bogus") },
	)
	a.shell = "bash"
	write(t, filepath.Join(dir, "config.yaml"), "dockmaster: {bogus: 1}\n")
	reloadNow(t, a)
	if a.shell != "bash" || !strings.Contains(a.errFlash, "config not reloaded") ||
		!strings.Contains(a.errFlash, "bogus") {
		t.Errorf("shell %q err %q", a.shell, a.errFlash)
	}
}

// TestReactiveKeepsRuntimeToggles: a toggle flipped at runtime survives a
// reload that did not change it in the file, and follows one that did; a
// setting only a restart applies is named.
func TestReactiveKeepsRuntimeToggles(t *testing.T) {
	ro := false
	a, dir := reactiveApp(t, func() (Reloaded, error) {
		return Reloaded{Theme: style.Base(), Options: Options{ReadOnly: ro}, NeedsRestart: []string{"context"}}, nil
	})
	a.dispatchCommand("readonly") // on at runtime; the file still says off
	write(t, filepath.Join(dir, "aliases.yaml"), "x: images\n")
	reloadNow(t, a)
	if !a.readonly {
		t.Error("a reload that left readOnly alone turned the runtime toggle off")
	}
	if !strings.Contains(a.flash, "context take effect on restart") {
		t.Errorf("restart-only change not named: %q", a.flash)
	}

	a.readonly, ro = false, true // the file now turns it on
	write(t, filepath.Join(dir, "config.yaml"), "dockmaster: {readOnly: true}\n")
	reloadNow(t, a)
	if !a.readonly {
		t.Error("a reload that changed readOnly in the file did not apply it")
	}
}

// TestReactiveOff: without a reload function nothing is watched.
func TestReactiveOff(t *testing.T) {
	a := optsApp(t, Options{})
	if a.checkReload() != nil {
		t.Error("watched a directory with ui.reactive off")
	}
	_ = tea.Quit
}
