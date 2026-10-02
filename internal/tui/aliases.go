package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// maxAliasDepth bounds how far an alias may chain through others.
const maxAliasDepth = 5

// isBuiltinCommand reports whether word is the first word of a command
// dockmaster already has, which an alias may not take over.
func isBuiltinCommand(word string) bool {
	if _, ok := viewCommands[word]; ok {
		return true
	}
	for _, c := range append(knownCommands, "aliases", "alias") {
		if first, _, _ := strings.Cut(c, " "); first == word {
			return true
		}
	}
	return false
}

// ValidateAliases checks the aliases file before the UI starts, as
// config.yaml is checked: a name is one word that is not already a
// command, and what it stands for resolves to a command — directly or
// through other aliases, without a loop.
func ValidateAliases(aliases map[string]string) error {
	var errs []string
	for name, target := range aliases {
		switch {
		case name == "" || strings.ContainsAny(name, " \t:/"):
			errs = append(errs, fmt.Sprintf("alias %q: a name is one word, with no : or /", name))
			continue
		case isBuiltinCommand(strings.ToLower(name)):
			errs = append(errs, fmt.Sprintf("alias %q: already a dockmaster command", name))
			continue
		}
		if _, err := expand(aliases, target); err != nil {
			errs = append(errs, fmt.Sprintf("alias %q: %v", name, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return fmt.Errorf("aliases: %s", strings.Join(errs, "; "))
}

// expand resolves a command line's first word through the aliases, the
// rest of the line kept after what it expands to. A command that is not
// an alias comes back as it was; one that never reaches a command, or
// loops, is an error.
func expand(aliases map[string]string, line string) (string, error) {
	for range maxAliasDepth {
		first, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
		target, ok := aliases[strings.ToLower(first)]
		if !ok {
			if first != "" && !isBuiltinCommand(strings.ToLower(first)) {
				return "", fmt.Errorf("%q is not a command", first)
			}
			return line, nil
		}
		line = strings.TrimSpace(target + " " + rest)
	}
	return "", fmt.Errorf("loops through aliases, or chains more than %d deep", maxAliasDepth)
}

// aliasNames is the aliases' names, for the palette's suggestions.
func aliasNames(aliases map[string]string) []string {
	out := make([]string, 0, len(aliases))
	for name := range aliases {
		out = append(out, strings.ToLower(name))
	}
	sort.Strings(out)
	return out
}

// showAliases opens :aliases — each alias and what it stands for.
func (a *App) showAliases() tea.Cmd {
	lines := make([]string, 0, len(a.aliases)+2)
	for _, name := range aliasNames(a.aliases) {
		lines = append(lines, fmt.Sprintf("  %-16s %s", name, a.aliases[name]))
	}
	if len(lines) == 0 {
		path, _ := config.AliasesPath() //nolint:errcheck // only a hint
		lines = append(lines, "", "  no aliases — add them to "+path+", as k9s's aliases.yaml:",
			"", "    aliases:", "      pg: containers /postgres")
	}
	body := []byte(strings.Join(lines, "\n"))
	v := views.NewInspectFetchView("aliases", func(context.Context) ([]byte, error) { return body, nil })
	v.SetPlain()
	a.setView(style.ViewInspect, v)
	a.pushView(style.ViewInspect)
	return v.Init()
}
