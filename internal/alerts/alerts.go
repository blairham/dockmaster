// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package alerts turns the daemon's event stream into the few things worth
// interrupting someone for (#109): a container turning unhealthy, being
// OOM-killed, dying with a non-zero exit, or restart-looping.
//
// The Detector is pure — events in, alerts out, judged by the events' own
// times — so every rule is tested on hand-built events with no daemon and
// no clock. It reports transitions, not states: a container that stays
// unhealthy is reported once, and a crash loop is reported as a loop, not
// as one alert per crash.
package alerts

import (
	"fmt"
	"strings"
	"time"

	"github.com/blairham/dockmaster/internal/docker"
)

// Kind is what an alert is about.
type Kind int

// The kinds of alert.
const (
	Unhealthy Kind = iota + 1
	OOM
	Died
	RestartLoop
)

// String is the kind's short name, as the :alerts view shows it.
func (k Kind) String() string {
	switch k {
	case Unhealthy:
		return "unhealthy"
	case OOM:
		return "oom"
	case Died:
		return "died"
	case RestartLoop:
		return "restart-loop"
	}
	return "unknown"
}

// Alert is one transition worth flashing.
type Alert struct {
	Time     time.Time
	ID       string
	Name     string
	ExitCode string
	Kind     Kind
	// Restarts and Window are a restart loop's count and the window it
	// was counted in.
	Restarts int
	Window   time.Duration
}

// Message is what happened, without the container: "OOM-killed".
func (a Alert) Message() string {
	switch a.Kind {
	case Unhealthy:
		return "unhealthy"
	case OOM:
		return "OOM-killed"
	case Died:
		return "exited with code " + a.ExitCode
	case RestartLoop:
		return fmt.Sprintf("restart-looping: %d restarts in %s", a.Restarts, shortDuration(a.Window))
	}
	return "alert"
}

// String is the flash: "[web] OOM-killed".
func (a Alert) String() string { return "[" + a.Name + "] " + a.Message() }

// shortDuration drops a Duration's zero tails: 5m, not 5m0s.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Rules are the restart-loop thresholds: Count restarts within Window is a
// loop. A Count below 1 turns loop detection off.
type Rules struct {
	Window time.Duration
	Count  int
}

// oomDieGap is how long after an oom event the container's die is taken
// to be the same crash, already reported as the OOM.
const oomDieGap = 10 * time.Second

// container is what the Detector remembers about one container.
//
//   - unhealthy: its last health_status was unhealthy, already reported.
//   - killed: a kill event since its last die — someone asked it to stop
//     (docker stop, kill, restart and compose down all send one), so the
//     die that follows is not a crash.
//   - crashed: it died since its last start, unasked; its next start is a
//     restart that counts toward a loop.
//   - looping: a loop was reported and has not yet cooled off.
type container struct {
	oomAt     time.Time
	lastCrash time.Time
	restarts  []time.Time
	unhealthy bool
	killed    bool
	crashed   bool
	looping   bool
}

// Detector folds container events into alerts. It is not safe for
// concurrent use; the app calls it from its update loop.
type Detector struct {
	ctrs  map[string]*container
	rules Rules
}

// NewDetector is a Detector with no history.
func NewDetector(r Rules) *Detector {
	return &Detector{rules: r, ctrs: map[string]*container{}}
}

// SetRules replaces the restart-loop thresholds, keeping what is known
// about each container (a config reload).
func (d *Detector) SetRules(r Rules) { d.rules = r }

// Observe folds one event in and returns the alerts it raises, usually
// none. Events must arrive in the order the daemon sent them.
func (d *Detector) Observe(e docker.Event) []Alert {
	if e.Type != "container" || e.ID == "" {
		return nil
	}
	action := strings.ToLower(strings.TrimSpace(e.Action))
	if action == "destroy" {
		delete(d.ctrs, e.ID)
		return nil
	}
	s := d.ctrs[e.ID]
	if s == nil {
		s = &container{}
		d.ctrs[e.ID] = s
	}
	alert := func(k Kind) Alert { return Alert{Time: e.Time, ID: e.ID, Name: e.Name, Kind: k} }

	switch {
	case strings.HasPrefix(action, "health_status"):
		was := s.unhealthy
		s.unhealthy = strings.HasSuffix(action, "unhealthy")
		if s.unhealthy && !was {
			return []Alert{alert(Unhealthy)}
		}
	case action == "kill":
		s.killed = true
	case action == "oom":
		s.oomAt = e.Time
		if d.crashQuiet(s, e.Time) {
			return nil
		}
		s.lastCrash = e.Time
		return []Alert{alert(OOM)}
	case action == "die":
		return d.die(s, e, alert)
	case action == "start":
		return d.start(s, e, alert)
	}
	return nil
}

// die handles a container stopping: a crash unless a kill asked for it,
// reported unless it exited 0, was the OOM just reported, or is part of a
// crash already reported.
func (d *Detector) die(s *container, e docker.Event, alert func(Kind) Alert) []Alert {
	requested := s.killed
	s.killed, s.unhealthy = false, false
	if requested {
		return nil
	}
	s.crashed = true
	d.cool(s, e.Time)
	code := e.Attrs["exitCode"]
	if code == "" || code == "0" {
		return nil
	}
	if !s.oomAt.IsZero() && e.Time.Sub(s.oomAt) <= oomDieGap {
		return nil
	}
	if d.crashQuiet(s, e.Time) {
		return nil
	}
	s.lastCrash = e.Time
	a := alert(Died)
	a.ExitCode = code
	return []Alert{a}
}

// start handles a container starting: after an unasked die it is a
// restart, and Count of them within Window is a loop, reported once.
func (d *Detector) start(s *container, e docker.Event, alert func(Kind) Alert) []Alert {
	crashed := s.crashed
	s.crashed, s.killed, s.unhealthy = false, false, false
	if !crashed || d.rules.Count < 1 {
		return nil
	}
	s.restarts = append(s.restarts, e.Time)
	d.cool(s, e.Time)
	if len(s.restarts) < d.rules.Count || s.looping {
		return nil
	}
	s.looping = true
	a := alert(RestartLoop)
	a.Restarts, a.Window = len(s.restarts), d.rules.Window
	return []Alert{a}
}

// cool drops the restarts older than the window, and ends a reported loop
// once fewer than Count are left in it — the next loop is news again.
func (d *Detector) cool(s *container, now time.Time) {
	keep := s.restarts[:0]
	for _, t := range s.restarts {
		if now.Sub(t) < d.rules.Window {
			keep = append(keep, t)
		}
	}
	s.restarts = keep
	if len(s.restarts) < d.rules.Count {
		s.looping = false
	}
}

// crashQuiet is whether a crash at now goes unreported: the container is
// in a reported loop, or one of its crashes was reported within the
// window — a container that keeps crashing is one alert, then a loop.
func (d *Detector) crashQuiet(s *container, now time.Time) bool {
	if s.looping {
		return true
	}
	return !s.lastCrash.IsZero() && now.Sub(s.lastCrash) < d.rules.Window
}
