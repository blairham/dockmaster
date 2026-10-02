package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// Event is one daemon event: something happened to an object.
type Event struct {
	Time   time.Time
	Attrs  map[string]string
	Type   string // container, image, volume, network, daemon, ...
	Action string // create, start, die, kill, pull, health_status: healthy, ...
	ID     string
	Name   string
}

// EventStream is a live feed of daemon events.
type EventStream struct {
	Events <-chan Event
	Err    <-chan error
}

// StreamEvents follows the daemon's event feed, starting since ago so the
// view opens on recent history rather than an empty screen.
func (c *Client) StreamEvents(ctx context.Context, since time.Duration) *EventStream {
	opts := client.EventsListOptions{}
	if since > 0 {
		opts.Since = fmt.Sprintf("%d", time.Now().Add(-since).Unix())
	}
	res := c.api.Events(ctx, opts)
	msgs, errs := res.Messages, res.Err
	out := make(chan Event, 256)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-msgs:
				if !ok {
					return
				}
				select {
				case out <- newEvent(m):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return &EventStream{Events: out, Err: errs}
}

func newEvent(m events.Message) Event {
	t := time.Unix(0, m.TimeNano)
	if m.TimeNano == 0 {
		t = time.Unix(m.Time, 0)
	}
	e := Event{
		Time: t, Type: string(m.Type), Action: string(m.Action),
		ID: m.Actor.ID, Attrs: m.Actor.Attributes,
	}
	e.Name = e.Attrs["name"]
	if e.Name == "" {
		e.Name = shortID(e.ID)
	}
	return e
}

// eventDetailKeys are the attributes worth a column, in order. Everything
// else — labels, mostly; compose stamps a dozen on every container — is
// noise in a one-line feed.
var eventDetailKeys = []string{"exitCode", "signal", "image", "container", "driver", "type", "execID"}

// Details is the event's notable attributes as "key=value" pairs.
func (e Event) Details() string {
	var parts []string
	for _, k := range eventDetailKeys {
		v := e.Attrs[k]
		if v == "" || (k == "image" && e.Type == "image") {
			continue
		}
		if k == "container" || k == "execID" {
			v = shortID(v)
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

// Severity classes an event for coloring: "bad" for an object failing or
// being destroyed, "good" for one coming up, "" for the rest.
func (e Event) Severity() string {
	a := strings.ToLower(e.Action)
	switch {
	case a == "die" && e.Attrs["exitCode"] == "0":
		return ""
	case a == "die", a == "kill", a == "oom", a == "destroy", a == "delete", a == "remove",
		strings.Contains(a, "unhealthy"):
		return "bad"
	case a == "start", a == "create", a == "pull", a == "restart", a == "unpause",
		a == "connect", a == "mount", strings.HasSuffix(a, ": healthy"):
		return "good"
	}
	return ""
}

// SortedAttrs is every attribute as "key=value", sorted, for a full view.
func (e Event) SortedAttrs() []string {
	out := make([]string, 0, len(e.Attrs))
	for k, v := range e.Attrs {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
