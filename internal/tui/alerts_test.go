// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/alerts"
	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

var alertsOn = config.Alerts{Enabled: true, RestartLoop: config.RestartLoop{Count: 3, Window: 5 * time.Minute}}

// alertApp is an app with alerts as given and a log to read back.
func alertApp(t *testing.T, s config.Alerts) (*App, func() string) {
	t.Helper()
	opts, log := logTo(t, "warn")
	opts.Alerts = s
	a := newSizedApp(t, opts, 160, 44)
	a.splashActive = false
	loadContainers(a)
	return a, log
}

// feedAlerts stands a stream holding events in for the daemon's, as
// ensureAlerts would subscribe one, and returns the drain over it. The
// tests have no client, so nothing else ever subscribes.
func feedAlerts(a *App, events ...docker.Event) tea.Cmd {
	ch := make(chan docker.Event, len(events))
	for _, e := range events {
		ch <- e
	}
	// Closed, so the drain returns at once and none is left blocked: the
	// burst arrives as the stream's last.
	close(ch)
	w := &a.alerts
	if w.detector == nil {
		w.detector = alerts.NewDetector(rulesOf(a.alertSettings()))
	}
	w.gen++
	w.stream = &docker.EventStream{Events: ch, Err: make(chan error)}
	return drainAlerts(w.stream, w.gen)
}

func ctrEvent(id, name, action string, attrs ...string) docker.Event {
	m := map[string]string{"name": name}
	for i := 0; i+1 < len(attrs); i += 2 {
		m[attrs[i]] = attrs[i+1]
	}
	return docker.Event{Time: time.Now(), Type: "container", Action: action, ID: id, Name: name, Attrs: m}
}

// hasBell is whether cmd, run, rings the terminal bell.
func hasBell(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch m := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range m {
			if hasBell(c) {
				return true
			}
		}
	case tea.RawMsg:
		return m.Msg == "\a"
	}
	return false
}

// TestAlertFlashesInAnyViewAndIsLogged: an OOM kill raises one flash over
// whatever view is on screen, names the container, and is written to the
// log once — as the alert it is, not again as an error flash (#109).
func TestAlertFlashesInAnyViewAndIsLogged(t *testing.T) {
	a, log := alertApp(t, alertsOn)
	step(a, key("2")) // volumes: nowhere near the events view
	msg := feedAlerts(a,
		ctrEvent("aaaaaaaaaaaa1111", "web", "oom"),
		ctrEvent("aaaaaaaaaaaa1111", "web", "die", "exitCode", "137"),
	)()
	step(a, msg)
	if a.errFlash != "[web] OOM-killed" {
		t.Fatalf("flash %q, want [web] OOM-killed", a.errFlash)
	}
	if !strings.Contains(render(a), "[web] OOM-killed") {
		t.Error("the alert is not on screen")
	}
	step(a, tea.WindowSizeMsg{Width: 160, Height: 44}) // logFlash runs again
	got := log()
	if n := strings.Count(got, "OOM-killed"); n != 1 {
		t.Errorf("the alert is in the log %d times, want once:\n%s", n, got)
	}
	if !strings.Contains(got, "msg=alert") || !strings.Contains(got, "kind=oom") ||
		!strings.Contains(got, "container=web") {
		t.Errorf("log line does not name the alert:\n%s", got)
	}
}

// TestAlertBell: alerts.bell rings the terminal bell with an alert, and
// only with one.
func TestAlertBell(t *testing.T) {
	bell := alertsOn
	bell.Bell = true
	for _, tc := range []struct {
		event docker.Event
		name  string
		s     config.Alerts
		want  bool
	}{
		{name: "bell on, alert", s: bell, event: ctrEvent("a", "web", "die", "exitCode", "1"), want: true},
		{name: "bell on, no alert", s: bell, event: ctrEvent("a", "web", "die", "exitCode", "0")},
		{name: "bell off, alert", s: alertsOn, event: ctrEvent("a", "web", "die", "exitCode", "1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := alertApp(t, tc.s)
			cmd := step(a, feedAlerts(a, tc.event)())
			if got := hasBell(cmd); got != tc.want {
				t.Errorf("bell %v, want %v", got, tc.want)
			}
		})
	}
}

// TestStaleAlertBurstIsDropped: a burst from a subscription since
// replaced — a context switch, a reload that turned alerts off — raises
// nothing.
func TestStaleAlertBurstIsDropped(t *testing.T) {
	a, _ := alertApp(t, alertsOn)
	msg := feedAlerts(a, ctrEvent("a", "web", "health_status: unhealthy"))()
	a.stopAlerts()
	step(a, msg)
	if a.errFlash != "" || len(a.alerts.fired) != 0 {
		t.Errorf("a stale burst fired: flash %q, fired %v", a.errFlash, a.alerts.fired)
	}
}

// TestAlertStreamEndIsMadeAgain: a stream that ends leaves no
// subscription behind, and with no drain re-armed on it.
func TestAlertStreamEndIsMadeAgain(t *testing.T) {
	a, _ := alertApp(t, alertsOn)
	feedAlerts(a)
	gen := a.alerts.gen
	step(a, alertEventsMsg{gen: gen, closed: true, err: errors.New("EOF")})
	if a.alerts.stream != nil || a.alerts.cancel != nil {
		t.Error("an ended stream is still held; the tick would never subscribe again")
	}
}

// TestCollectBurst: a drain returns a burst once its window passes after
// the first event, and says when the stream closed or failed.
func TestCollectBurst(t *testing.T) {
	ch := make(chan int, 3)
	ch <- 1
	ch <- 2
	got, closed, err := collectBurst(ch, nil, 10*time.Millisecond)
	if len(got) != 2 || closed || err != nil {
		t.Errorf("open stream: %v closed %v err %v, want [1 2] open", got, closed, err)
	}
	ch <- 3
	close(ch)
	got, closed, _ = collectBurst(ch, nil, time.Hour)
	if len(got) != 1 || !closed {
		t.Errorf("closed stream: %v closed %v, want [3] closed", got, closed)
	}
	errs := make(chan error, 1)
	errs <- errors.New("gone")
	if _, closed, err := collectBurst(make(chan int), errs, time.Hour); !closed || err == nil {
		t.Errorf("failed stream: closed %v err %v", closed, err)
	}
}

// TestAlertsView: :alerts lists what fired, newest first, and enter jumps
// to the container in the containers view, stopped ones listed; an alert
// from another context is refused rather than switched to.
func TestAlertsView(t *testing.T) {
	a, _ := alertApp(t, alertsOn)
	step(a, feedAlerts(a, ctrEvent("cccccccccccc3333", "migrate", "die", "exitCode", "2"))())
	step(a, feedAlerts(a, ctrEvent("aaaaaaaaaaaa1111", "web", "health_status: unhealthy"))())

	if msg, _ := a.dispatchCommand("alerts"); msg != "" {
		t.Fatal(msg)
	}
	av := typedView[*views.AlertsView](a, style.ViewAlerts)
	if a.view != style.ViewAlerts || av == nil || av.Count() != 2 {
		t.Fatalf(":alerts opened %s", style.ViewName(a.view))
	}
	if first, _ := av.Selected(); first.Name != "web" {
		t.Errorf("first row %q, want the newest, web", first.Name)
	}
	out := render(a)
	for _, want := range []string{"unhealthy", "exited with code 2", "migrate", "alerts(all)[2]"} {
		if !strings.Contains(out, want) {
			t.Errorf(":alerts does not show %q in\n%s", want, out)
		}
	}

	// A new alert while the view is open lands in it.
	step(a, feedAlerts(a, ctrEvent("bbbbbbbbbbbb2222", "api", "oom"))())
	if av.Count() != 3 {
		t.Errorf("a new alert did not reach the open view: %d rows", av.Count())
	}

	step(a, key("j"))
	step(a, key("j")) // migrate, the oldest
	step(a, key("enter"))
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	if a.view != style.ViewContainers || cv == nil || !cv.ShowAll() || a.filter != "^migrate$" {
		t.Fatalf("enter went to %s filter %q", style.ViewName(a.view), a.filter)
	}

	a.alerts.fired = append(a.alerts.fired, views.FiredAlert{
		Context: "prod",
		Alert:   alerts.Alert{Name: "db", Kind: alerts.Unhealthy, Time: time.Now()},
	})
	a.dispatchCommand("alerts")
	step(a, key("enter"))
	if a.view != style.ViewAlerts || !strings.Contains(a.errFlash, "from context prod") {
		t.Errorf("an alert from another context: view %s flash %q", style.ViewName(a.view), a.errFlash)
	}
}

// TestAlertSettingsPerContext: a context's alerts entry lays its keys over
// the top-level ones, and :alerts says when alerts are off on it.
func TestAlertSettingsPerContext(t *testing.T) {
	off := false
	opts, _ := logTo(t, "warn")
	opts.Alerts = alertsOn
	// The test app has no client, so it is on the context named "".
	opts.Contexts = map[string]ContextSettings{"": {Alerts: &config.ContextAlerts{Enabled: &off}}}
	a := newSizedApp(t, opts, 160, 44)
	if a.alertSettings().Enabled {
		t.Error("the context's enabled: false did not apply")
	}
	if a.alertSettings().RestartLoop != alertsOn.RestartLoop {
		t.Errorf("restartLoop not inherited: %+v", a.alertSettings().RestartLoop)
	}
	a.dispatchCommand("alerts")
	if !strings.Contains(a.flash, "alerts are off") {
		t.Errorf("flash %q, want it to say alerts are off", a.flash)
	}
}
