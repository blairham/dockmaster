// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// logAction carries out the log view's own keys: time range, wrap, clear,
// copy, save and mark. None of them touches the daemon.
func (a *App) logAction(action, param string) tea.Cmd {
	lv := typedView[*views.LogsView](a, style.ViewLogs)
	if lv == nil {
		return nil
	}
	switch action {
	case "log_range":
		for _, r := range views.LogRanges {
			if r.Label == param {
				if r.Since == 0 {
					a.flash = "logs: the usual backlog"
				} else {
					a.flash = "logs: everything from the last " + r.Label
				}
				return lv.SetRange(r.Since)
			}
		}
	case "log_wrap":
		lv.ToggleWrap()
		a.flash = "wrap " + onOff(lv.Wrap())
	case "log_mark":
		lv.Mark(time.Now())
	case "log_clear":
		lv.Clear()
		a.flash = "cleared — new lines keep arriving"
	case "log_copy":
		text, n := lv.PlainText()
		if n == 0 {
			a.errFlash = "nothing to copy"
			return nil
		}
		a.flash = fmt.Sprintf("copied %d lines", n)
		return tea.SetClipboard(text)
	case "log_save":
		path, n, err := a.saveLog(lv)
		if err != nil {
			a.errFlash = "saving logs: " + err.Error()
			return nil
		}
		a.flash = fmt.Sprintf("saved %d lines to %s", n, path)
	}
	return nil
}

// saveLog writes what the log view shows, styling stripped, to
// <state>/logs/<container>-<time>.txt.
func (a *App) saveLog(lv *views.LogsView) (string, int, error) {
	text, n := lv.PlainText()
	if n == 0 {
		return "", 0, errors.New("nothing to save")
	}
	path, err := a.saveDump("logs", lv.Title(), text)
	return path, n, err
}

// setFullscreen hides the header and breadcrumbs so a log gets the whole
// terminal, and puts back exactly what was showing before — a header the
// user had already hidden with ctrl-e stays hidden.
func (a *App) setFullscreen(on bool) {
	if on == a.fullscreen {
		return
	}
	if on {
		a.preFullscreen = [2]bool{a.chrome.HeaderHidden, a.chrome.CrumbsHidden}
		a.chrome.HeaderHidden, a.chrome.CrumbsHidden = true, true
	} else {
		a.chrome.HeaderHidden, a.chrome.CrumbsHidden = a.preFullscreen[0], a.preFullscreen[1]
	}
	a.fullscreen = on
	if lv := typedView[*views.LogsView](a, style.ViewLogs); lv != nil {
		lv.SetFullscreen(on)
	}
	a.resizeActiveView()
}

// inspectAction carries out the inspect view's own keys: copy and save
// what it shows, and toggle refreshing on the poll (#6).
func (a *App) inspectAction(action string) tea.Cmd {
	iv := typedView[*views.InspectView](a, style.ViewInspect)
	if iv == nil {
		return nil
	}
	switch action {
	case "inspect_copy":
		text, n := iv.PlainText()
		if n == 0 {
			a.errFlash = "nothing to copy"
			return nil
		}
		a.flash = fmt.Sprintf("copied %d lines", n)
		return tea.SetClipboard(text)
	case "inspect_save":
		text, n := iv.PlainText()
		if n == 0 {
			a.errFlash = "nothing to save"
			return nil
		}
		path, err := a.saveDump("dumps", iv.Title(), text)
		if err != nil {
			a.errFlash = "saving " + iv.Title() + ": " + err.Error()
			return nil
		}
		a.flash = fmt.Sprintf("saved %d lines to %s", n, path)
	case "search_next", "search_prev":
		moved := iv.NextMatch
		if action == "search_prev" {
			moved = iv.PrevMatch
		}
		if !moved() {
			a.errFlash = "no matches — / to search"
		}
	case "inspect_auto":
		on := !a.inspectAutoRefresh()
		iv.SetAutoRefresh(on)
		a.flash = "auto-refresh " + onOff(on)
	}
	return nil
}

// inspectAutoRefresh reports whether the open inspect view refreshes on the
// poll: its own a toggle when pressed, liveViewAutoRefresh otherwise.
func (a *App) inspectAutoRefresh() bool {
	if a.view != style.ViewInspect {
		return false
	}
	iv := typedView[*views.InspectView](a, style.ViewInspect)
	if iv == nil {
		return a.liveRefresh
	}
	if on, set := iv.AutoRefresh(); set {
		return on
	}
	return a.liveRefresh
}

// openLogs opens lv with the log settings: backlog, timestamps, buffer and
// range, and k9s's textWrap, disableAutoscroll and defaultsToFullScreen.
// Every log view opens here, so a new one cannot miss a setting.
func (a *App) openLogs(lv *views.LogsView) tea.Cmd {
	lv.Configure(a.logTail, a.logShowTime).Limits(a.logBuffer, a.logSince)
	if a.logWrap {
		lv.ToggleWrap()
	}
	if a.logPaused {
		lv.ToggleFollow()
	}
	a.setView(style.ViewLogs, lv)
	a.pushView(style.ViewLogs)
	if a.logFullscreen {
		a.setFullscreen(true)
	}
	return lv.Init()
}
