// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blairham/tuikit/theme"

	"github.com/blairham/dockmaster/internal/tui/style"
)

// ContextSettings is one docker context's entry under config.yaml's
// `contexts:` (#55), resolved by main: Theme is the context's skin over the
// default theme (nil when it names none, or DOCKMASTER_SKIN overrides it),
// ReadOnly its readOnly (nil when unset), DefaultView the command a switch
// to it opens.
type ContextSettings struct {
	Theme       *theme.Theme
	ReadOnly    *bool
	DefaultView string
}

// ValidateContextCommand checks a context's defaultView: any command -c
// takes, but not one naming an @context of its own — it is already the
// landing of a context switch.
func ValidateContextCommand(input string, aliases map[string]string) error {
	if err := ValidateCommand(input, aliases); err != nil {
		return err
	}
	raw, err := expand(aliases, input)
	if err != nil {
		return err
	}
	if _, _, ok := cutContextArg(raw); ok {
		return fmt.Errorf("%q names a context; a context's defaultView opens on that context", input)
	}
	return nil
}

// readOnlyFor is whether the session is read-only on context name, before
// any :readonly toggle: --readonly forces it everywhere; else the
// context's readOnly when it sets one; else the top-level readOnly.
func (o Options) readOnlyFor(name string) bool {
	if o.ForceReadOnly {
		return true
	}
	if cs, ok := o.Contexts[name]; ok && cs.ReadOnly != nil {
		return *cs.ReadOnly
	}
	return o.ReadOnly
}

// readOnlyFromContext is whether name's own readOnly is what turns the
// session read-only — said when switching to it.
func (o Options) readOnlyFromContext(name string) bool {
	cs, ok := o.Contexts[name]
	return !o.ForceReadOnly && ok && cs.ReadOnly != nil && *cs.ReadOnly && !o.ReadOnly
}

// themeFor is the theme on context name: its skin's, else global — the
// top-level ui.skin's (or DOCKMASTER_SKIN's).
func (o Options) themeFor(global theme.Theme, name string) theme.Theme {
	if cs, ok := o.Contexts[name]; ok && cs.Theme != nil {
		return *cs.Theme
	}
	return global
}

// contextName is the docker context on screen, "" with no client.
func (a *App) contextName() string {
	if a.client == nil {
		return ""
	}
	return a.client.ContextName
}

// applyContextSettings puts context name's skin and readOnly into effect,
// or the top-level ones when it has no entry. It runs on every switch, so a
// :readonly toggle lasts until the next one.
func (a *App) applyContextSettings(name string) {
	a.readonly = a.applied.readOnlyFor(name)
	a.reskin(a.applied.themeFor(a.globalTheme, name))
}

// reskin puts theme t into effect as a reload does: every style re-derives
// from the new base (style.SetBase runs the OnBase hooks), and the frame
// and bars, built on the old one, are built again.
func (a *App) reskin(t theme.Theme) {
	style.SetBase(t)
	a.buildChrome(t, a.chrome.HeaderHidden, a.chrome.CrumbsHidden)
}

// contextLanding is where a switch to context name with no landing of its
// own opens: the context's defaultView, else the view last open on it, else
// nil (containers).
func (a *App) contextLanding(name string) *landing {
	cmd := a.applied.Contexts[name].DefaultView
	if cmd == "" {
		cmd = a.lastViews[name]
	}
	if cmd == "" {
		return nil
	}
	if vt, ok := ViewForCommand(cmd); ok {
		return &landing{view: vt}
	}
	return &landing{command: cmd}
}

// contextState is <state>/contexts.json: what dockmaster remembers about
// each docker context between runs. Never config.yaml, which dockmaster
// does not write.
type contextState struct {
	LastView map[string]string `json:"lastView"`
}

// readContextState reads path; a missing or unreadable file is an empty
// state, and a view name it does not know is dropped.
func readContextState(path string) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	b, err := os.ReadFile(path) //nolint:gosec // the app's own state file
	if err != nil {
		return out
	}
	var st contextState
	if json.Unmarshal(b, &st) != nil {
		return out
	}
	for ctx, v := range st.LastView {
		if _, ok := ViewForCommand(v); ok {
			out[ctx] = v
		}
	}
	return out
}

// viewCommandName is the palette name of a top-level view, as -c takes it.
func viewCommandName(v style.ViewType) (string, bool) {
	for _, n := range ViewCommandNames() {
		if vt, ok := ViewForCommand(n); ok && vt == v {
			return n, true
		}
	}
	return "", false
}

// rememberView records v as the view last open on the current context, and
// writes it to the state file when that changed. A write that fails is
// logged, not flashed: it is a convenience, and it would fail on every
// view change.
func (a *App) rememberView(v style.ViewType) {
	name := a.contextName()
	cmd, ok := viewCommandName(v)
	if name == "" || !ok || a.lastViews[name] == cmd {
		return
	}
	a.lastViews[name] = cmd
	if err := writeLastView(a.contextStateFile, name, cmd); err != nil {
		a.log.Warn("context state not saved", "file", a.contextStateFile, "error", err)
	}
}

// writeLastView sets one context's last view in the file at path, keeping
// the others — another dockmaster may have written them — through a
// temporary file, so a crash mid-write leaves the old file whole.
func writeLastView(path, ctx, view string) error {
	if path == "" {
		return nil
	}
	st := contextState{LastView: readContextState(path)}
	st.LastView[ctx] = view
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
