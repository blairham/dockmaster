package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"

	"github.com/blairham/dockmaster/internal/config"
)

// HotKey is a validated hotkey: the key as bubbletea names it, what help
// shows, and the `:` command it runs.
type HotKey struct {
	Key     string // bubbletea's name: ")", "ctrl+u", "f2"
	Label   string // as help shows it: "<shift-0>"
	Desc    string
	Command string
}

// shiftedDigits is what a terminal sends for Shift and a digit on a US
// layout — the key k9s's Shift-0 … Shift-9 mean.
const shiftedDigits = ")!@#$%^&*("

// reservedKeys can never reach a hotkey: the chrome, the palette, the
// filter, the view digits and the table keys are handled first, and a
// hotkey on a navigation key would take navigation away.
var reservedKeys = map[string]bool{
	"?": true, ":": true, "/": true, "esc": true, "enter": true, "tab": true, "shift+tab": true,
	"q": true, "r": true, "[": true, "]": true, "-": true,
	"ctrl+c": true, "ctrl+r": true, "ctrl+g": true, "ctrl+e": true, "ctrl+s": true,
	"space": true, "ctrl+space": true, "ctrl+\\": true,
	"shift+left": true, "shift+right": true, "shift+up": true, "shift+down": true,
	"j": true, "k": true, "h": true, "l": true, "g": true, "G": true, "ctrl+f": true, "ctrl+b": true,
	"up": true, "down": true, "left": true, "right": true, "pgup": true, "pgdown": true, "home": true, "end": true,
}

// ParseShortcut turns k9s's spelling of a key — Shift-0, Shift-A, Ctrl-U,
// Alt-X, F2, or a plain character — into what bubbletea reports when it is
// pressed.
func ParseShortcut(s string) (string, error) {
	mod, k, hasMod := strings.Cut(strings.TrimSpace(s), "-")
	if !hasMod || k == "" {
		k, mod = mod, ""
	}
	lower := strings.ToLower(k)
	switch strings.ToLower(mod) {
	case "":
		switch {
		case len(k) == 1:
			return k, nil
		case len(lower) >= 2 && lower[0] == 'f' && isDigits(lower[1:]):
			return lower, nil
		}
	case "shift":
		switch {
		case len(k) == 1 && k[0] >= '0' && k[0] <= '9':
			return string(shiftedDigits[k[0]-'0']), nil
		case len(k) == 1 && strings.ToUpper(k) != lower:
			return strings.ToUpper(k), nil
		}
	case "ctrl", "alt":
		if len(k) == 1 || (lower[0] == 'f' && isDigits(lower[1:])) {
			return strings.ToLower(mod) + "+" + lower, nil
		}
	}
	return "", fmt.Errorf("%q is not a key — use Shift-0, Shift-A, Ctrl-U, Alt-X, F2 or one character", s)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// HotKeys validates the hotkeys file before the UI starts, as the aliases
// file is: each shortcut parses, is not a key dockmaster keeps for itself
// and is not taken twice, and its command resolves (aliases included).
func HotKeys(entries map[string]config.HotKey, aliases map[string]string) ([]HotKey, error) {
	var (
		out  []HotKey
		errs []string
		seen = map[string]string{}
	)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		e := entries[name]
		key, err := ParseShortcut(e.ShortCut)
		switch {
		case err != nil:
			errs = append(errs, fmt.Sprintf("hotkey %q: %v", name, err))
			continue
		case reservedKeys[key] || (len(key) == 1 && key[0] >= '0' && key[0] <= '9'):
			errs = append(errs, fmt.Sprintf("hotkey %q: %s is a dockmaster key", name, e.ShortCut))
			continue
		case seen[key] != "":
			errs = append(errs, fmt.Sprintf("hotkey %q: %s is already hotkey %q", name, e.ShortCut, seen[key]))
			continue
		case strings.TrimSpace(e.Command) == "":
			errs = append(errs, fmt.Sprintf("hotkey %q: no command", name))
			continue
		}
		if _, err := expand(aliases, e.Command); err != nil {
			errs = append(errs, fmt.Sprintf("hotkey %q: %v", name, err))
			continue
		}
		seen[key] = name
		desc := e.Description
		if desc == "" {
			desc = ":" + e.Command
		}
		out = append(out, HotKey{Key: key, Label: "<" + strings.ToLower(e.ShortCut) + ">", Desc: desc, Command: e.Command})
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("hotkeys: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

// hotKey runs the hotkey bound to key, if there is one, as its command
// typed at the palette. It is asked after the view's own keys, so a view
// that binds the same key keeps it.
func (a *App) hotKey(key string) (tea.Cmd, bool) {
	for _, hk := range a.hotKeys {
		if hk.Key == key {
			msg, cmd := a.dispatchCommand(hk.Command)
			if msg != "" {
				a.errFlash = msg
			}
			return cmd, true
		}
	}
	return nil, false
}

// hotKeysHelp is the HOTKEYS column of help, when there are any.
func (a *App) hotKeysHelp() (chrome.HelpSection, bool) {
	if len(a.hotKeys) == 0 {
		return chrome.HelpSection{}, false
	}
	s := chrome.HelpSection{Title: "HOTKEYS"}
	for _, hk := range a.hotKeys {
		s.Entries = append(s.Entries, chrome.HelpEntry{Key: hk.Label, Desc: hk.Desc})
	}
	return s, true
}
