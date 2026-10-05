// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/theme"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// Reloaded is a fresh read of the config directory for ui.reactive (#38):
// the options it sets, the skin's theme, and the settings it changed that
// only a restart applies.
type Reloaded struct {
	Theme        theme.Theme
	NeedsRestart []string
	Options      Options
}

// reloadedMsg carries a reload's result back to the UI goroutine.
type reloadedMsg struct {
	err error
	r   Reloaded
}

// fingerprint is the config directory's files by name, size and
// modification time — cheap enough to take on every tick, and changed by
// any save. Only two levels deep: the directory and skins/.
func fingerprint(dir string) string {
	if dir == "" {
		return ""
	}
	var rows []string
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A file that vanished mid-walk just changes the fingerprint;
			// the next tick sees the directory as it settled.
			return nil //nolint:nilerr // see above
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return nil //nolint:nilerr // p is always under dir
		}
		if d.IsDir() {
			if strings.Count(rel, string(filepath.Separator)) >= 1 {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			rows = append(rows, fmt.Sprintf("%s|%d|%d", rel, info.Size(), info.ModTime().UnixNano()))
		}
		return nil
	})
	if walkErr != nil {
		return ""
	}
	slices.Sort(rows)
	return strings.Join(rows, "\n")
}

// checkReload is the tick's look at the config directory. A change is read
// only once it has held still for a tick: an editor saving in several
// writes, or a half-written file, is never what gets applied.
func (a *App) checkReload() tea.Cmd {
	if a.reload == nil {
		return nil
	}
	fp := fingerprint(a.watchDir)
	switch {
	case fp == a.watchFP:
		a.pendingFP = ""
		return nil
	case fp != a.pendingFP:
		a.pendingFP = fp
		return nil
	}
	a.watchFP, a.pendingFP = fp, ""
	reload := a.reload
	return func() tea.Msg {
		r, err := reload()
		return reloadedMsg{r: r, err: err}
	}
}

// applyReload puts a fresh read of the config directory into effect. A file
// that does not load leaves everything as it was: the running config is
// never half-replaced. A toggle the user flips at runtime — readonly, the
// header, the crumbs, the logo — is changed only when its value in the file
// changed, so a reload does not undo it.
func (a *App) applyReload(m reloadedMsg) {
	if m.err != nil {
		a.errFlash = "config not reloaded: " + m.err.Error()
		return
	}
	n, prev := m.r.Options, a.applied

	a.aliases, a.hotKeys, a.plugins, a.jumps = n.Aliases, n.HotKeys, n.Plugins, n.Jumps
	a.thresholds = n.Thresholds
	if cv := typedView[*views.ContainersView](a, style.ViewContainers); cv != nil {
		cv.SetThresholds(n.Thresholds)
	}
	a.logTail, a.logShowTime, a.logBuffer, a.logSince = n.LogTail, n.LogShowTime, n.LogBuffer, n.LogSince
	a.logWrap, a.logPaused, a.logFullscreen = n.LogWrap, n.LogPaused, n.LogFullscreen
	a.liveRefresh, a.noExitOnCtrlC, a.dumpDir, a.shell, a.noMouse = n.LiveRefresh, n.NoExitOnCtrlC, n.DumpDir, n.Shell, n.NoMouse
	if n.RefreshRate > 0 {
		a.refresh = n.RefreshRate
		if pv := typedView[*views.PulsesView](a, style.ViewPulses); pv != nil {
			pv.SetInterval(a.refresh)
		}
	}
	// readOnly — top-level, the context's, --readonly — counts as changed
	// only when what it comes to on this context did.
	ctxName := a.contextName()
	if ro := n.readOnlyFor(ctxName); ro != prev.readOnlyFor(ctxName) {
		a.readonly = ro
	}
	if n.Logoless != prev.Logoless {
		a.logoless = n.Logoless
	}
	headless, crumbsless := a.chrome.HeaderHidden, a.chrome.CrumbsHidden
	if n.Headless != prev.Headless {
		headless = n.Headless
	}
	if n.Crumbsless != prev.Crumbsless {
		crumbsless = n.Crumbsless
	}

	// The skin: every style re-derives from the new base, and the frame
	// and bars, built on the old one, are built again. On a context with a
	// skin of its own it is that one; the top-level one is kept for the
	// next switch.
	a.globalTheme = m.r.Theme
	th := n.themeFor(a.globalTheme, ctxName)
	style.SetBase(th)
	a.buildChrome(th, headless, crumbsless)
	a.applyColumnLayouts(n.ColumnLayouts)
	a.resizeActiveView()
	a.applied = n

	a.flash = "config reloaded"
	if len(m.r.NeedsRestart) > 0 {
		a.flash += " — " + strings.Join(m.r.NeedsRestart, ", ") + " take effect on restart"
	}
}

// applyColumnLayouts puts views.yaml's columns into effect on every view
// already built — the active one and those waiting under it on the stack —
// so none shows its rows under columns they were not built for.
func (a *App) applyColumnLayouts(l map[string]views.ColumnLayout) {
	views.SetColumnLayouts(l)
	for _, v := range a.viewMap {
		if r, ok := v.(views.Relayouter); ok {
			r.Relayout()
		}
	}
}
