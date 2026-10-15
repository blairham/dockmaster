// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/alerts"
	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// cmdAlerts is the :alerts command.
const cmdAlerts = "alerts"

// maxFiredAlerts caps what :alerts remembers; the oldest go first.
const maxFiredAlerts = 500

// alertBurstWindow is how long a drain keeps collecting after the first
// event, so a burst — a crash and its restart — is one message.
const alertBurstWindow = 100 * time.Millisecond

// alertWatch is the alert watcher (#109): one StreamEvents subscription,
// from now on, held for as long as alerts are enabled on the context on
// screen — whichever view is showing, unlike the events view's own. The
// Detector decides what is worth a flash; it is kept across a dropped and
// re-made subscription and started over on a context switch, since its
// container IDs are the old daemon's.
type alertWatch struct {
	detector *alerts.Detector
	cancel   context.CancelFunc
	stream   *docker.EventStream
	fired    []views.FiredAlert
	gen      int
}

// alertEventsMsg carries a drained burst of events to the watcher, or
// that its stream ended.
type alertEventsMsg struct {
	err    error
	events []docker.Event
	gen    int
	closed bool
}

// alertSettings is config.yaml's alerts block with the context on
// screen's overrides laid over it.
func (a *App) alertSettings() config.Alerts {
	return a.applied.Alerts.With(a.applied.Contexts[a.contextName()].Alerts)
}

func rulesOf(s config.Alerts) alerts.Rules {
	return alerts.Rules{Count: s.RestartLoop.Count, Window: s.RestartLoop.Window}
}

// ensureAlerts brings the watcher in line with the settings: it
// subscribes when alerts are on and nothing is subscribed — at startup, on
// the tick after a stream ended, after a reload turned them on — and
// unsubscribes when they are off. It is cheap to call on every tick.
func (a *App) ensureAlerts() tea.Cmd {
	s := a.alertSettings()
	w := &a.alerts
	if w.detector != nil {
		w.detector.SetRules(rulesOf(s))
	}
	if !s.Enabled || a.client == nil {
		a.stopAlerts()
		return nil
	}
	if w.stream != nil {
		return nil
	}
	if w.detector == nil {
		w.detector = alerts.NewDetector(rulesOf(s))
	}
	w.gen++
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.stream = a.client.StreamEvents(ctx, 0)
	return drainAlerts(w.stream, w.gen)
}

// stopAlerts cancels the subscription, if there is one. A drain still
// blocked on it returns, and its message is dropped by generation.
func (a *App) stopAlerts() {
	w := &a.alerts
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	if w.stream != nil {
		w.gen++
	}
	w.stream = nil
}

// restartAlerts starts the watcher over on a new daemon: the old one's
// containers mean nothing there.
func (a *App) restartAlerts() tea.Cmd {
	a.stopAlerts()
	a.alerts.detector = nil
	return a.ensureAlerts()
}

// drainAlerts blocks until an event arrives or the stream ends — a quiet
// daemon costs no wakeups — then collects for alertBurstWindow.
func drainAlerts(st *docker.EventStream, gen int) tea.Cmd {
	if st == nil {
		return nil
	}
	return func() tea.Msg {
		events, closed, err := collectBurst(st.Events, st.Err, alertBurstWindow)
		return alertEventsMsg{gen: gen, events: events, closed: closed, err: err}
	}
}

// collectBurst returns the events from the first to window after it, or
// what arrived before the stream closed or failed.
func collectBurst[T any](items <-chan T, errs <-chan error, window time.Duration) ([]T, bool, error) {
	var out []T
	var timer *time.Timer
	var deadline <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case it, ok := <-items:
			if !ok {
				return out, true, nil
			}
			out = append(out, it)
			if timer == nil {
				timer = time.NewTimer(window)
				deadline = timer.C
			}
		case e, ok := <-errs:
			switch {
			case !ok:
				errs = nil // closed: only the events channel is left to end it
			case e != nil:
				return out, true, e
			}
		case <-deadline:
			return out, false, nil
		}
	}
}

// handleAlertEvents runs a burst through the detector, fires what it
// raises and re-arms the drain; a stream that ended is made again on the
// next tick.
func (a *App) handleAlertEvents(m alertEventsMsg) tea.Cmd {
	w := &a.alerts
	if m.gen != w.gen || w.detector == nil {
		return nil
	}
	var raised []alerts.Alert
	for _, e := range m.events {
		raised = append(raised, w.detector.Observe(e)...)
	}
	for _, al := range raised {
		a.fireAlert(al)
	}
	var cmds []tea.Cmd
	if len(raised) > 0 && a.alertSettings().Bell {
		cmds = append(cmds, tea.Raw("\a"))
	}
	if m.closed {
		if m.err != nil && !strings.Contains(m.err.Error(), "context canceled") {
			a.log.Debug("alert stream ended", "error", m.err)
		}
		a.stopAlerts()
		return tea.Batch(cmds...)
	}
	return tea.Batch(append(cmds, drainAlerts(w.stream, w.gen))...)
}

// fireAlert records an alert, writes it to the log and flashes it over
// whatever is on screen. It is the log's line, so the flash is marked
// logged rather than written a second time as an error.
func (a *App) fireAlert(al alerts.Alert) {
	ctx := a.contextName()
	w := &a.alerts
	w.fired = append(w.fired, views.FiredAlert{Context: ctx, Alert: al})
	if over := len(w.fired) - maxFiredAlerts; over > 0 {
		w.fired = w.fired[over:]
	}
	a.log.Warn("alert", "kind", al.Kind.String(), "container", al.Name, "id", al.ID,
		"context", ctx, "message", al.Message())
	a.errFlash = al.String()
	a.loggedFlash = a.errFlash
	if av := typedView[*views.AlertsView](a, style.ViewAlerts); av != nil {
		av.SetAlerts(w.fired)
	}
}

// showAlerts opens :alerts over the current view.
func (a *App) showAlerts() {
	if a.view == style.ViewAlerts {
		return
	}
	a.setView(style.ViewAlerts, views.NewAlertsView(a.alerts.fired))
	a.pushView(style.ViewAlerts)
	if !a.alertSettings().Enabled {
		a.flash = "alerts are off on this context — alerts.enabled in config.yaml"
	}
}

// jumpToAlert opens the containers view on an alert's container, stopped
// ones listed — a crashed container usually is one. An alert from another
// context is refused rather than switched to: a jump should not change
// which daemon the next keystroke acts on.
func (a *App) jumpToAlert(param string) tea.Cmd {
	ctx, name, _ := strings.Cut(param, "\x00")
	if ctx != a.contextName() {
		a.errFlash = "that alert is from context " + ctx + " — :ctx " + ctx + " first"
		return nil
	}
	cmd := a.switchView(style.ViewContainers)
	if v := typedView[*views.ContainersView](a, style.ViewContainers); v != nil && !v.ShowAll() {
		cmd = tea.Batch(cmd, v.ToggleAll())
	}
	a.filter = "^" + regexp.QuoteMeta(name) + "$"
	a.setActiveFilter(a.filter)
	a.flash = "container " + name
	return cmd
}
