// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
)

func TestNewEvent(t *testing.T) {
	e := newEvent(events.Message{
		Type: "container", Action: "die", TimeNano: time.Date(2026, 10, 1, 12, 0, 0, 5, time.UTC).UnixNano(),
		Actor: events.Actor{ID: "abcdef1234567890", Attributes: map[string]string{
			"name": "web", "image": "nginx:1.27", "exitCode": "137",
			"com.docker.compose.project": "shop",
		}},
	})
	if e.Name != "web" || e.Type != "container" || e.Action != "die" || e.Time.Nanosecond() != 5 {
		t.Errorf("event = %+v", e)
	}
	if got := e.Details(); got != "exitCode=137 image=nginx:1.27" {
		t.Errorf("Details = %q — labels are noise, the exit code and image are not", got)
	}
	if e.Severity() != "bad" {
		t.Errorf("die 137 severity = %q", e.Severity())
	}
	if noName := newEvent(events.Message{Actor: events.Actor{ID: "0123456789abcdef0"}}); noName.Name != "0123456789ab" {
		t.Errorf("unnamed actor = %q, want the short ID", noName.Name)
	}
}

func TestEventSeverity(t *testing.T) {
	for _, tc := range []struct {
		action, exit, want string
	}{
		{action: "start", want: "good"},
		{action: "health_status: healthy", want: "good"},
		{action: "health_status: unhealthy", want: "bad"},
		{action: "die", exit: "0", want: ""},
		{action: "die", exit: "1", want: "bad"},
		{action: "oom", want: "bad"},
		{action: "exec_start: sh", want: ""},
	} {
		e := Event{Action: tc.action, Attrs: map[string]string{"exitCode": tc.exit}}
		if got := e.Severity(); got != tc.want {
			t.Errorf("%s exit=%q: %q, want %q", tc.action, tc.exit, got, tc.want)
		}
	}
}
