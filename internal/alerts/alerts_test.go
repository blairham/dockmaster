// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"slices"
	"testing"
	"time"

	"github.com/blairham/dockmaster/internal/docker"
)

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// ev is a container event for web at t0+at; attrs alternate key, value.
func ev(at time.Duration, action string, attrs ...string) docker.Event {
	m := map[string]string{"name": "web"}
	for i := 0; i+1 < len(attrs); i += 2 {
		m[attrs[i]] = attrs[i+1]
	}
	return docker.Event{Time: t0.Add(at), Type: "container", Action: action, ID: "abc123", Name: "web", Attrs: m}
}

// crash is an unasked die with code, then the restart policy's start.
func crash(at time.Duration, code string) []docker.Event {
	return []docker.Event{ev(at, "die", "exitCode", code), ev(at+time.Second, "start")}
}

func seq(parts ...[]docker.Event) []docker.Event { return slices.Concat(parts...) }

func one(e docker.Event) []docker.Event { return []docker.Event{e} }

// TestDetector feeds each case's events through one Detector and compares
// the alerts raised, as "[name] message" strings, in order.
func TestDetector(t *testing.T) {
	rules := Rules{Count: 3, Window: 5 * time.Minute}
	cases := []struct {
		name   string
		events []docker.Event
		want   []string
	}{
		{
			name:   "unhealthy is reported once, however often it is repeated",
			events: seq(one(ev(0, "health_status: unhealthy")), one(ev(30*time.Second, "health_status: unhealthy"))),
			want:   []string{"[web] unhealthy"},
		},
		{
			name: "unhealthy again after healthy is a new transition",
			events: seq(
				one(ev(0, "health_status: unhealthy")),
				one(ev(time.Minute, "health_status: healthy")),
				one(ev(2*time.Minute, "health_status: unhealthy")),
			),
			want: []string{"[web] unhealthy", "[web] unhealthy"},
		},
		{
			name:   "healthy and starting raise nothing",
			events: seq(one(ev(0, "health_status: starting")), one(ev(time.Second, "health_status: healthy"))),
		},
		{
			name:   "an OOM kill is reported, and its die is the same crash",
			events: seq(one(ev(0, "oom")), one(ev(time.Second, "die", "exitCode", "137"))),
			want:   []string{"[web] OOM-killed"},
		},
		{
			name:   "a non-zero exit is reported with its code",
			events: one(ev(0, "die", "exitCode", "1")),
			want:   []string{"[web] exited with code 1"},
		},
		{
			name:   "a clean exit is not",
			events: one(ev(0, "die", "exitCode", "0")),
		},
		{
			name: "docker stop and kill are asked for, whatever the exit code",
			events: seq(
				one(ev(0, "kill", "signal", "15")), one(ev(time.Second, "die", "exitCode", "143")),
				one(ev(time.Minute, "start")),
				one(ev(2*time.Minute, "kill", "signal", "9")), one(ev(2*time.Minute, "die", "exitCode", "137")),
			),
		},
		{
			name: "a kill is forgotten at the die it explains",
			events: seq(
				one(ev(0, "kill", "signal", "15")), one(ev(time.Second, "die", "exitCode", "143")),
				one(ev(10*time.Minute, "start")),
				one(ev(20*time.Minute, "die", "exitCode", "2")),
			),
			want: []string{"[web] exited with code 2"},
		},
		{
			name: "a crash loop is one crash, then one loop",
			events: seq(
				crash(0, "1"), crash(10*time.Second, "1"), crash(20*time.Second, "1"),
				crash(30*time.Second, "1"), crash(40*time.Second, "1"),
			),
			want: []string{"[web] exited with code 1", "[web] restart-looping: 3 restarts in 5m"},
		},
		{
			name: "restarts spread wider than the window are no loop",
			events: seq(
				crash(0, "1"), crash(6*time.Minute, "1"), crash(12*time.Minute, "1"), crash(18*time.Minute, "1"),
			),
			want: []string{
				"[web] exited with code 1", "[web] exited with code 1",
				"[web] exited with code 1", "[web] exited with code 1",
			},
		},
		{
			name: "a loop that cooled off is news again",
			events: seq(
				crash(0, "1"), crash(10*time.Second, "1"), crash(20*time.Second, "1"),
				crash(time.Hour, "1"), crash(time.Hour+10*time.Second, "1"), crash(time.Hour+20*time.Second, "1"),
			),
			want: []string{
				"[web] exited with code 1", "[web] restart-looping: 3 restarts in 5m",
				"[web] exited with code 1", "[web] restart-looping: 3 restarts in 5m",
			},
		},
		{
			name: "a container that exits 0 and is restarted over and over is looping too",
			events: seq(
				crash(0, "0"), crash(10*time.Second, "0"), crash(20*time.Second, "0"),
			),
			want: []string{"[web] restart-looping: 3 restarts in 5m"},
		},
		{
			name: "docker restart is not a crash restart",
			events: seq(
				one(ev(0, "kill", "signal", "15")), crash(time.Second, "143"),
				one(ev(10*time.Second, "kill", "signal", "15")), crash(11*time.Second, "143"),
				one(ev(20*time.Second, "kill", "signal", "15")), crash(21*time.Second, "143"),
			),
		},
		{
			name: "a restart clears unhealthy, so the next unhealthy is reported",
			events: seq(
				one(ev(0, "health_status: unhealthy")),
				one(ev(time.Minute, "kill", "signal", "15")), crash(time.Minute, "143"),
				one(ev(2*time.Minute, "health_status: unhealthy")),
			),
			want: []string{"[web] unhealthy", "[web] unhealthy"},
		},
		{
			name: "destroy forgets the container",
			events: seq(
				one(ev(0, "health_status: unhealthy")), one(ev(time.Minute, "destroy")),
				one(ev(2*time.Minute, "health_status: unhealthy")),
			),
			want: []string{"[web] unhealthy", "[web] unhealthy"},
		},
		{
			name: "events that are not a container's are ignored",
			events: []docker.Event{
				{Time: t0, Type: "image", Action: "die", ID: "x", Attrs: map[string]string{"exitCode": "1"}},
				{Time: t0, Type: "network", Action: "oom", ID: "y"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDetector(rules)
			var got []string
			for _, e := range tc.events {
				for _, a := range d.Observe(e) {
					got = append(got, a.String())
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("alerts\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestDetectorKeepsContainersApart: one container's crash or health says
// nothing about another's.
func TestDetectorKeepsContainersApart(t *testing.T) {
	d := NewDetector(Rules{Count: 3, Window: 5 * time.Minute})
	a := docker.Event{
		Time: t0, Type: "container", Action: "die", ID: "a", Name: "a",
		Attrs: map[string]string{"exitCode": "1"},
	}
	b := a
	b.ID, b.Name = "b", "b"
	if got := len(d.Observe(a)) + len(d.Observe(b)); got != 2 {
		t.Errorf("two containers crashing raised %d alerts, want 2", got)
	}
}

// TestRestartLoopOff: a Count below 1 turns loop detection off.
func TestRestartLoopOff(t *testing.T) {
	d := NewDetector(Rules{Window: 5 * time.Minute})
	for _, e := range seq(crash(0, "0"), crash(time.Second, "0"), crash(2*time.Second, "0"), crash(3*time.Second, "0")) {
		if as := d.Observe(e); len(as) > 0 {
			t.Fatalf("loop detection off raised %v", as)
		}
	}
}

// TestSetRulesKeepsHistory: a reload's new thresholds apply to the
// restarts already counted.
func TestSetRulesKeepsHistory(t *testing.T) {
	d := NewDetector(Rules{Count: 5, Window: 5 * time.Minute})
	for _, e := range seq(crash(0, "0"), crash(10*time.Second, "0")) {
		d.Observe(e)
	}
	d.SetRules(Rules{Count: 3, Window: 5 * time.Minute})
	got := make([]Alert, 0, 1)
	for _, e := range crash(20*time.Second, "0") {
		got = append(got, d.Observe(e)...)
	}
	if len(got) != 1 || got[0].Kind != RestartLoop || got[0].Restarts != 3 {
		t.Errorf("after SetRules got %+v, want one 3-restart loop", got)
	}
}
