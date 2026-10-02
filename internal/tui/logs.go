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
		path, n, err := saveLog(lv)
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
func saveLog(lv *views.LogsView) (string, int, error) {
	text, n := lv.PlainText()
	if n == 0 {
		return "", 0, errors.New("nothing to save")
	}
	path, err := saveDump("logs", lv.Title(), text)
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
