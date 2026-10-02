// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/tui/style"
)

func TestValidateAliases(t *testing.T) {
	if err := ValidateAliases(map[string]string{"pg": "containers /postgres", "p": "pg", "img": "images"}); err != nil {
		t.Errorf("good aliases: %v", err)
	}
	for _, tc := range []struct {
		aliases map[string]string
		want    string
	}{
		{aliases: map[string]string{"ps": "images"}, want: `"ps": already a dockmaster command`},
		{aliases: map[string]string{"x": "bogus"}, want: `"bogus" is not a command`},
		{aliases: map[string]string{"a": "b", "b": "a"}, want: "loops"},
		{aliases: map[string]string{"two words": "images"}, want: "one word"},
	} {
		if err := ValidateAliases(tc.aliases); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err %v, want %q", tc.aliases, err, tc.want)
		}
	}
}

// aliasApp is the app with aliases, on the containers view.
func aliasApp(t *testing.T, aliases map[string]string) *App {
	t.Helper()
	a := NewApp(nil, Options{Version: "test", Aliases: aliases})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 40})
	loadContainers(a)
	return a
}

// TestAliasOpensItsViewFiltered: an alias stands for a command line — a
// view and a filter, its case kept — and words after it go along.
func TestAliasOpensItsViewFiltered(t *testing.T) {
	a := aliasApp(t, map[string]string{"pg": "images /Postgres", "c": "containers", "w": "c /web"})
	if msg, _ := a.dispatchCommand("pg"); msg != "" || a.view != style.ViewImages || a.filter != "Postgres" {
		t.Errorf(":pg: msg %q view %v filter %q", msg, a.view, a.filter)
	}
	if _, _ = a.dispatchCommand("c /api"); a.view != style.ViewContainers || a.filter != "api" {
		t.Errorf(":c /api: view %v filter %q", a.view, a.filter)
	}
	if _, _ = a.dispatchCommand(
		"w",
	); a.view != style.ViewContainers || a.filter != "web" ||
		!strings.Contains(render(a), "web") {
		t.Errorf(":w (chained): view %v filter %q", a.view, a.filter)
	}
	if msg, _ := a.dispatchCommand("nope"); !strings.Contains(msg, "unknown command") {
		t.Errorf("an unknown word: %q", msg)
	}
}

func TestAliasesInThePalette(t *testing.T) {
	if got := fuzzyMatch("pg", aliasNames(map[string]string{"pg": "containers /postgres"})...); got != "pg" {
		t.Errorf("suggestion for pg = %q", got)
	}
	a := aliasApp(t, map[string]string{"pg": "containers /postgres"})
	_, cmd := a.dispatchCommand("aliases")
	runOnce(a, cmd)
	if out := render(a); !strings.Contains(out, "pg") || !strings.Contains(out, "containers /postgres") {
		t.Errorf(":aliases:\n%s", out)
	}
	empty := aliasApp(t, nil)
	_, cmd = empty.dispatchCommand("aliases")
	runOnce(empty, cmd)
	if out := render(empty); !strings.Contains(out, "no aliases") || !strings.Contains(out, "aliases.yaml") {
		t.Errorf(":aliases with none:\n%s", out)
	}
}
