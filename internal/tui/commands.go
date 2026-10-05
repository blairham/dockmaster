// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"maps"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// otherCommands are the `:` commands that are not views, each with the
// spellings dispatchCommand takes for it, in the order the aliases view
// lists them. TestAliasesViewCoversEveryCommand fails when a command is
// added to knownCommands or viewCommands without a row here or in the
// views.
var otherCommands = []views.Command{
	{Name: "ctx", Also: []string{"context", "contexts"}, Desc: "The docker contexts; :ctx <name> switches to one"},
	{Name: "xray", Also: []string{"x"}, Desc: "Projects, services, containers and what they use, as a tree"},
	{Name: "lint", Desc: "Each container's risky or fragile settings"},
	{Name: "dir", Desc: "Browse for compose files; :dir <path> starts there"},
	{Name: "sd", Also: []string{"screendump", "screendumps", "dumps"}, Desc: "What ctrl-s has saved"},
	{Name: "aliases", Also: []string{"alias"}, Desc: "This list (ctrl-a)"},
	{Name: "help", Also: []string{"h", "?"}, Desc: "The key reference, as ?"},
	{Name: "logs", Also: []string{"log"}, Desc: "The selected row's logs, as l"},
	{Name: "inspect", Also: []string{"describe"}, Desc: "Inspect the selected row, as o"},
	{Name: "top", Desc: "The selected container's processes, as T"},
	{Name: "diff", Desc: "The files the selected container changed, as D"},
	{Name: "health", Desc: "The selected container's healthcheck, as H"},
	{Name: "pull", Desc: "Pull an image: :pull <ref>", NeedsArg: true},
	{Name: "prune", Desc: "Remove stopped containers (confirms)"},
	{Name: "prune all", Desc: "As docker system prune; volumes are kept (confirms)"},
	{
		Name: "prune all volumes", Also: []string{"prune all -v", "prune all --volumes"},
		Desc: "prune all, and every unused volume (confirms)",
	},
	{Name: "prune cache", Also: []string{"prune build", "prune builder"}, Desc: "The build cache (confirms)"},
	{Name: "readonly", Desc: "Read-only mode on or off"},
	{Name: "all", Desc: "Stopped containers / all images, as a"},
	{Name: "stats", Desc: "The CPU/MEM poll on or off, as t"},
	{Name: "logo", Desc: "The header logo on or off"},
	{Name: "logoless", Desc: "The header logo off"},
	{Name: "q", Also: []string{"q!", "quit", "exit"}, Desc: "Quit"},
}

// isOtherSpelling reports a spelling dispatchCommand takes for a command
// that knownCommands lists under another name (:x, :log, :h), so -c, a
// hotkey and an alias accept every spelling the palette does.
func isOtherSpelling(line string) bool {
	for _, c := range otherCommands {
		if slices.Contains(c.Also, line) {
			return true
		}
	}
	return false
}

// viewDigits are the views a digit opens, for their description.
var viewDigits = map[style.ViewType]string{
	style.ViewContainers: "0", style.ViewImages: "1", style.ViewVolumes: "2", style.ViewNetworks: "3",
	style.ViewProjects: "4", style.ViewRuntimes: "5", style.ViewEvents: "6",
}

// commandRows is every `:` command for the aliases view: the user's
// aliases first, marked as such — the rows nobody else knows — then the
// views, each under its canonical name with its other spellings, then the
// other commands.
func commandRows(aliases map[string]string) []views.Command {
	out := make([]views.Command, 0, len(aliases)+len(viewCommands)+len(otherCommands))
	for _, name := range aliasNames(aliases) {
		out = append(out, views.Command{Name: name, Kind: views.CommandKindAlias, Desc: ":" + aliases[name]})
	}
	for _, name := range ViewCommandNames() {
		vt, _ := ViewForCommand(name)
		var also []string
		for _, spelling := range slices.Sorted(maps.Keys(viewCommands)) {
			if viewCommands[spelling] == vt && spelling != name {
				also = append(also, spelling)
			}
		}
		desc := style.ViewName(vt)
		if d, ok := viewDigits[vt]; ok {
			desc += " (" + d + ")"
		}
		out = append(out, views.Command{Name: name, Also: also, Kind: views.CommandKindView, Desc: desc})
	}
	for _, c := range otherCommands {
		c.Kind = views.CommandKindCommand
		out = append(out, c)
	}
	return out
}

// showAliases opens the aliases view (ctrl-a, :aliases): every command and
// alias, enter running the selected one. Asked again while it is showing,
// it stays as it is rather than stacking a second copy.
func (a *App) showAliases() {
	if a.view == style.ViewAliases {
		return
	}
	a.setView(style.ViewAliases, views.NewAliasesView(commandRows(a.aliases)))
	a.pushView(style.ViewAliases)
	if len(a.aliases) == 0 {
		path, _ := config.AliasesPath() //nolint:errcheck // only a hint
		a.flash = "no aliases of your own — add them to " + path + ", as k9s's aliases.yaml"
	}
}

// runCommand runs a command the aliases view picked, as if typed at the
// palette.
func (a *App) runCommand(line string) tea.Cmd {
	msg, cmd := a.dispatchCommand(line)
	if msg != "" {
		a.errFlash = msg
	}
	return cmd
}
