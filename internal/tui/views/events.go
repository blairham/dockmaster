// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/tail"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// eventsSince is how much history the events view opens with, so it starts
// on what just happened rather than an empty screen.
const eventsSince = 10 * time.Minute

// EventsBatchMsg carries a drained batch of daemon events.
type EventsBatchMsg struct {
	Events []docker.Event
	Gen    int
}

// EventsClosedMsg signals the event stream ended.
type EventsClosedMsg struct {
	Err error
	Gen int
}

// EventsView is `docker events`: a live feed of everything happening on
// the daemon — containers starting and dying, images pulled, networks
// connected — colored by what it means.
type EventsView struct {
	tail   *tail.Model
	client *docker.Client
	cancel context.CancelFunc
	stream *docker.EventStream
	err    error

	// events is the feed as received, so ctrl-z can show only the faults
	// and back without losing what was hidden. Capped at the tail's size.
	events  []docker.Event
	filter  string
	gen     int
	width   int
	height  int
	loading bool
	faults  bool
}

// maxEvents caps what the feed remembers, as the tail caps its lines.
const maxEvents = 5000

// NewEventsView builds the view. The stream starts on Init.
func NewEventsView(client *docker.Client) *EventsView {
	t := tail.New()
	t.SetFollow(true)
	return &EventsView{client: client, tail: t, loading: true}
}

// Title names the feed in the border.
func (v *EventsView) Title() string { return "events" }

// Init starts the stream.
func (v *EventsView) Init() tea.Cmd { return v.start() }

func (v *EventsView) start() tea.Cmd {
	v.Stop()
	if v.client == nil {
		v.loading = false
		return nil
	}
	v.gen++
	v.loading = true
	v.err = nil
	if v.tail.LineCount() > 0 {
		// A restart replays the history window; a fresh feed keeps it from
		// appearing twice.
		follow := v.tail.Follow()
		v.events = nil
		v.tail = tail.New()
		v.tail.SetFollow(follow)
		v.tail.SetFilter(v.filter)
		if v.width > 0 {
			v.tail.Resize(v.width, v.height)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	v.stream = v.client.StreamEvents(ctx, eventsSince)
	return v.drain()
}

// Stop cancels the stream. Safe to call repeatedly.
func (v *EventsView) Stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	v.stream = nil
}

// drain collects what the stream produced within the batch window, the way
// the logs view does: one message per event would swamp the loop on a busy
// daemon.
func (v *EventsView) drain() tea.Cmd {
	st, gen := v.stream, v.gen
	if st == nil {
		return nil
	}
	return func() tea.Msg {
		batch := make([]docker.Event, 0, 32)
		deadline := time.NewTimer(logBatchWindow)
		defer deadline.Stop()
		for {
			select {
			case e, ok := <-st.Events:
				if !ok {
					if len(batch) > 0 {
						return EventsBatchMsg{Gen: gen, Events: batch}
					}
					return EventsClosedMsg{Gen: gen}
				}
				batch = append(batch, e)
				if len(batch) >= logBatchMax {
					return EventsBatchMsg{Gen: gen, Events: batch}
				}
			case err := <-st.Err:
				if err != nil {
					return EventsClosedMsg{Gen: gen, Err: err}
				}
			case <-deadline.C:
				return EventsBatchMsg{Gen: gen, Events: batch}
			}
		}
	}
}

// Update folds stream messages into the feed.
func (v *EventsView) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.MouseWheelMsg:
		return scrollTail(v.tail, m)
	case EventsBatchMsg:
		if m.Gen != v.gen {
			return nil
		}
		v.loading = false
		v.events = append(v.events, m.Events...)
		if over := len(v.events) - maxEvents; over > 0 {
			v.events = v.events[over:]
		}
		v.tail.AppendLines(v.render(m.Events))
		return v.drain()
	case EventsClosedMsg:
		if m.Gen != v.gen {
			return nil
		}
		v.loading = false
		if m.Err != nil && !strings.Contains(m.Err.Error(), "context canceled") {
			v.err = docker.FormatUserError(m.Err)
			return nil
		}
		v.tail.AppendLine(style.Muted.Render("— event stream ended —"))
	}
	return nil
}

// Event feed styles, derived in applyTheme.
var eventTimeStyle, eventTypeStyle, eventNameStyle lipgloss.Style

// renderEvent is one line of the feed:
//
//	15:04:05  container  die                web            exitCode=137 image=nginx
func renderEvent(e docker.Event) string {
	action := fmt.Sprintf("%-22s", truncate(e.Action, 22))
	switch e.Severity() {
	case "bad":
		action = style.StateDead.Render(action)
	case "good":
		action = style.StateRunning.Render(action)
	default:
		action = style.StateRestarting.Render(action)
	}
	return eventTimeStyle.Render(e.Time.Local().Format("15:04:05")) + "  " +
		eventTypeStyle.Render(fmt.Sprintf("%-9s", e.Type)) + "  " +
		action + "  " +
		eventNameStyle.Render(fmt.Sprintf("%-24s", truncate(e.Name, 24))) + "  " +
		eventTimeStyle.Render(e.Details())
}

// UpdateTable forwards scroll keys to the viewport.
func (v *EventsView) UpdateTable(msg tea.Msg) tea.Cmd { return scrollTail(v.tail, msg) }

// Resize re-lays the viewport.
func (v *EventsView) Resize(width, height int) {
	v.width, v.height = width, height
	v.tail.Resize(width, height)
}

// Count is the number of events shown under the filter.
func (v *EventsView) Count() int { return v.tail.VisibleCount() }

// Loading is always false: a quiet daemon has no events, and that is an
// answer — the view says so instead of spinning until something happens.
func (v *EventsView) Loading() bool { return false }

// SetFilter applies a regex filter to the feed.
func (v *EventsView) SetFilter(f string) {
	v.filter = f
	v.tail.SetFilter(f)
}

// HandleKey binds ctrl-z, the faults filter, as the containers view does.
// f (follow) and the scroll keys reach the tail through UpdateTable, where
// tuikit's HandleScrollKey owns them; toggling f here as well would cancel
// tuikit's toggle out.
func (v *EventsView) HandleKey(key string) (string, string) {
	if key == "ctrl+z" {
		return "toggle_event_faults", ""
	}
	return "", ""
}

// ToggleFaults shows only the events that mean something failed or was
// destroyed — a die with a non-zero exit, a kill, an OOM, an unhealthy
// check — or everything again, rebuilding the feed from what it received.
func (v *EventsView) ToggleFaults() {
	v.faults = !v.faults
	v.tail.ReplaceLines(v.render(v.events))
}

// Faults reports whether only faults are shown.
func (v *EventsView) Faults() bool { return v.faults }

// Status names the faults filter in the border title.
func (v *EventsView) Status() string {
	if v.faults {
		return "faults"
	}
	return ""
}

// render is the feed lines for es, only the faults while that is on.
func (v *EventsView) render(es []docker.Event) []string {
	lines := make([]string, 0, len(es))
	for _, e := range es {
		if v.faults && e.Severity() != "bad" {
			continue
		}
		lines = append(lines, renderEvent(e))
	}
	return lines
}

// Follow reports whether the feed is pinned to the newest event.
func (v *EventsView) Follow() bool { return v.tail.Follow() }

// View renders the feed.
func (v *EventsView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if !v.tail.Ready() {
		return ""
	}
	if v.tail.LineCount() == 0 {
		return style.Muted.Render(
			fmt.Sprintf("  no events in the last %d minutes — waiting for the next one", int(eventsSince.Minutes())),
		)
	}
	return v.tail.View()
}

// Refresh restarts the stream.
func (v *EventsView) Refresh() tea.Cmd { return v.start() }
