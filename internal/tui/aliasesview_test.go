// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// openAliases presses ctrl-a and returns the view it opened.
func openAliases(t *testing.T, a *App) *views.AliasesView {
	t.Helper()
	step(a, key("ctrl+a"))
	if a.view != style.ViewAliases {
		t.Fatalf("ctrl-a opened %s", style.ViewName(a.view))
	}
	return typedView[*views.AliasesView](a, style.ViewAliases)
}

// TestAliasesViewListsCommandsAndAliases: ctrl-a opens every command and
// the user's aliases as a table, k9s's aliases view; an alias is marked as
// one and shows what it stands for, a built-in shows its other spellings.
func TestAliasesViewListsCommandsAndAliases(t *testing.T) {
	a := aliasApp(t, map[string]string{"pg": "containers /postgres"})
	openAliases(t, a)
	out := render(a)
	for _, want := range []string{
		"COMMAND", "ALSO", "KIND", "DESCRIPTION",
		"pg", "alias", ":containers /postgres",
		"containers", "container, ps", "view",
		"xray", "command",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the aliases view lacks %q:\n%s", want, out)
		}
	}
	// Asked again while it is showing, it does not stack a second copy.
	step(a, key("ctrl+a"))
	if len(a.viewStack) != 1 {
		t.Errorf("ctrl-a twice: stack %v", a.viewStack)
	}
	step(a, key("esc"))
	if a.view != style.ViewContainers {
		t.Errorf("esc from the aliases view went to %s", style.ViewName(a.view))
	}
}

// TestAliasesViewEnterRunsTheCommand: enter runs the selected row as if
// typed at the palette — an alias expanded, a view opened, a command that
// needs an argument left in the palette to finish.
func TestAliasesViewEnterRunsTheCommand(t *testing.T) {
	a := aliasApp(t, map[string]string{"pg": "containers /postgres"})
	openAliases(t, a) // the user's aliases come first
	step(a, key("enter"))
	if a.view != style.ViewContainers || a.filter != "postgres" {
		t.Errorf("enter on pg: view %s filter %q", style.ViewName(a.view), a.filter)
	}

	openAliases(t, a)
	step(a, key("/"))
	typeText(a, "^xray$")
	step(a, key("enter"))
	if n := typedView[*views.AliasesView](a, style.ViewAliases).Count(); n != 1 {
		t.Fatalf("filter ^xray$ left %d rows:\n%s", n, render(a))
	}
	step(a, key("enter"))
	if a.view != style.ViewXray {
		t.Errorf("enter on xray opened %s", style.ViewName(a.view))
	}

	openAliases(t, a)
	step(a, key("/"))
	typeText(a, "^pull$")
	step(a, key("enter"))
	step(a, key("enter"))
	if !a.commandBar.Active() || a.commandBar.Value() != "pull " {
		t.Errorf("enter on pull: palette active %v value %q", a.commandBar.Active(), a.commandBar.Value())
	}
}

// TestAliasesViewSorts: the aliases view sorts on shift-arrows like any
// table, and Selected follows the sort — enter runs the row under the
// cursor, not the one that was there before.
func TestAliasesViewSorts(t *testing.T) {
	a := aliasApp(t, nil)
	av := openAliases(t, a)
	first, _ := av.Selected()
	step(a, key("shift+right")) // sort by COMMAND, ascending
	sorted, _ := av.Selected()
	if sorted.Name == first.Name || sorted.Name != "aliases" {
		t.Errorf("sorted by command: first row %q (was %q), want aliases", sorted.Name, first.Name)
	}
	step(a, key("enter"))
	if a.view != style.ViewAliases || len(a.viewStack) != 1 {
		t.Errorf("enter on :aliases from its own view: view %s stack %v", style.ViewName(a.view), a.viewStack)
	}
}

// TestCtrlAInTheBarsIsLineStart: the bars get ctrl-a first, so in : and /
// it still moves to the start of the line, as in any readline.
func TestCtrlAInTheBarsIsLineStart(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	step(a, key(":"))
	typeText(a, "mages")
	step(a, key("ctrl+a"))
	typeText(a, "i")
	if got := a.commandBar.Value(); got != "images" || a.view != style.ViewContainers {
		t.Errorf("ctrl-a in the palette: value %q, view %s", got, style.ViewName(a.view))
	}
	step(a, key("esc"))

	step(a, key("/"))
	typeText(a, "eb")
	step(a, key("ctrl+a"))
	typeText(a, "w")
	if a.filter != "web" || a.view != style.ViewContainers {
		t.Errorf("ctrl-a in the filter: filter %q, view %s", a.filter, style.ViewName(a.view))
	}
}

// TestCtrlAIsNotAHotKey: a hotkey or plugin cannot take ctrl-a, which the
// app answers first.
func TestCtrlAIsNotAHotKey(t *testing.T) {
	if _, err := HotKeys(map[string]config.HotKey{"x": {ShortCut: "Ctrl-A", Command: "images"}}, nil); err == nil ||
		!strings.Contains(err.Error(), "dockmaster key") {
		t.Errorf("a ctrl-a hotkey: %v", err)
	}
	_, err := Plugins(map[string]config.Plugin{"p": {ShortCut: "Ctrl-A", Command: "true", Scopes: []string{"all"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "dockmaster key") {
		t.Errorf("a ctrl-a plugin: %v", err)
	}
}

// TestAliasesViewCoversEveryCommand: every spelling the palette knows is
// a row or an ALSO of one, and every row runs — a command added to
// knownCommands or viewCommands without a row here fails.
func TestAliasesViewCoversEveryCommand(t *testing.T) {
	rows := commandRows(nil)
	listed := map[string]bool{}
	for _, r := range rows {
		listed[r.Name] = true
		for _, s := range r.Also {
			listed[s] = true
		}
		line := r.Name
		if r.NeedsArg {
			line += " x"
		}
		if err := validateCommand(line, nil); err != nil {
			t.Errorf("row %q does not run: %v", r.Name, err)
		}
		for _, s := range r.Also {
			if err := validateCommand(s, nil); err != nil {
				t.Errorf("row %q: spelling %q does not run: %v", r.Name, s, err)
			}
		}
	}
	names := append([]string{}, knownCommands...)
	for name := range viewCommands {
		names = append(names, name)
	}
	for _, n := range names {
		if !listed[n] {
			t.Errorf(":%s is not in the aliases view", n)
		}
	}
	if n := len(commandRows(map[string]string{"pg": "ps"})) - len(rows); n != 1 {
		t.Errorf("one alias added %d rows", n)
	}
}
