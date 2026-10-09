// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"strings"
	"sync"
	"time"
)

// LogSource is one container feeding a merged log stream.
type LogSource struct {
	ID    string
	Label string // the prefix its lines carry: "web-1"
}

// LogOpener opens one container's stream: the last tail lines, or
// everything since a time when since is set.
type LogOpener func(ctx context.Context, id string, since time.Time) (*LogStream, error)

// MergeLogs fans several containers' streams into one, each line tagged
// with its source's label, as `docker compose logs -f` does.
//
// starts, when not nil, carries containers that start after the stream
// opened: a new one joins, and one whose stream ended — it stopped, then
// started again — rejoins from the moment it started, so its history is
// not repeated. A member that stops leaves a note rather than ending the
// merged stream, which runs until ctx is canceled: a project whose
// containers are all down is still worth watching for the next `up`.
func MergeLogs(ctx context.Context, sources []LogSource, open LogOpener, starts <-chan LogSource) *LogStream {
	m := &logMerger{ctx: ctx, open: open, out: make(chan LogLine, 512), active: map[string]bool{}}
	errc := make(chan error, 1)
	for _, src := range sources {
		m.follow(src, time.Time{})
	}
	go func() {
		m.watchStarts(starts)
		<-ctx.Done()
		m.wg.Wait()
		close(m.out)
		errc <- nil
	}()
	labels := make([]string, 0, len(sources))
	for _, s := range sources {
		labels = append(labels, s.Label)
	}
	return &LogStream{Lines: m.out, Err: errc, Sources: labels}
}

// logMerger is the state of one MergeLogs: the merged channel and which
// containers are being followed.
type logMerger struct {
	ctx    context.Context
	open   LogOpener
	out    chan LogLine
	active map[string]bool
	wg     sync.WaitGroup
	mu     sync.Mutex
}

// emit sends a line, or reports false once ctx is canceled.
func (m *logMerger) emit(l LogLine) bool {
	select {
	case m.out <- l:
		return true
	case <-m.ctx.Done():
		return false
	}
}

// note emits a note under a source's label, unless the stream is closing.
func (m *logMerger) note(src LogSource, text string) {
	if m.ctx.Err() == nil {
		m.emit(LogLine{Time: time.Now(), Source: src.Label, Note: true, Text: text})
	}
}

// follow starts following src from since, unless it is already followed.
func (m *logMerger) follow(src LogSource, since time.Time) {
	m.mu.Lock()
	if m.active[src.ID] {
		m.mu.Unlock()
		return
	}
	m.active[src.ID] = true
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.active, src.ID)
			m.mu.Unlock()
		}()
		st, err := m.open(m.ctx, src.ID, since)
		if err != nil {
			m.note(src, "logs unavailable: "+err.Error())
			return
		}
		if m.pump(src, st) {
			m.note(src, "— stopped —")
		}
	}()
}

// pump copies one stream's lines, labeled, until it ends (true) or ctx is
// canceled (false).
func (m *logMerger) pump(src LogSource, st *LogStream) bool {
	for {
		select {
		case l, ok := <-st.Lines:
			if !ok {
				return true
			}
			l.Source = src.Label
			if !m.emit(l) {
				return false
			}
		case <-m.ctx.Done():
			return false
		}
	}
}

// watchStarts follows each container that starts, from the moment it
// started, until starts closes or ctx is canceled.
func (m *logMerger) watchStarts(starts <-chan LogSource) {
	for starts != nil {
		select {
		case src, ok := <-starts:
			if !ok {
				return
			}
			m.follow(src, time.Now())
		case <-m.ctx.Done():
			return
		}
	}
}

// StreamProjectLogs follows every container of a compose project as one
// stream — running and stopped, as `docker compose logs` shows them — and
// picks up containers that start while it is open.
func (c *Client) StreamProjectLogs(
	ctx context.Context,
	project string,
	tail int,
	timestamps bool,
	since time.Time,
) (*LogStream, error) {
	all, err := c.Containers(ctx, true)
	if err != nil {
		return nil, err
	}
	var sources []LogSource
	for _, ctr := range all {
		if ctr.Project == project {
			sources = append(sources, LogSource{ID: ctr.ID, Label: ProjectLogLabel(project, ctr.Name)})
		}
	}

	starts := make(chan LogSource)
	events := c.StreamEvents(ctx, 0)
	go func() {
		defer close(starts)
		for e := range events.Events {
			if e.Type != "container" || e.Action != "start" || e.Attrs[LabelProject] != project {
				continue
			}
			select {
			case starts <- LogSource{ID: e.ID, Label: ProjectLogLabel(project, e.Attrs["name"])}:
			case <-ctx.Done():
				return
			}
		}
	}()

	open := func(ctx context.Context, id string, joined time.Time) (*LogStream, error) {
		from := since
		if !joined.IsZero() {
			from = joined
		}
		return c.StreamLogs(ctx, id, tail, timestamps, from)
	}
	return MergeLogs(ctx, sources, open, starts), nil
}

// ProjectLogLabel is the prefix a project member's lines carry: its name
// without the project's, so "shop-web-1" reads "web-1", as compose prints it.
func ProjectLogLabel(project, name string) string {
	if l := strings.TrimPrefix(name, project+"-"); l != "" && l != name {
		return l
	}
	return name
}
