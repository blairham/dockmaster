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
	out := make(chan LogLine, 512)
	errc := make(chan error, 1)

	var (
		mu     sync.Mutex
		active = map[string]bool{}
		wg     sync.WaitGroup
	)
	emit := func(l LogLine) bool {
		select {
		case out <- l:
			return true
		case <-ctx.Done():
			return false
		}
	}
	follow := func(src LogSource, since time.Time) {
		mu.Lock()
		if active[src.ID] {
			mu.Unlock()
			return
		}
		active[src.ID] = true
		mu.Unlock()

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				mu.Lock()
				delete(active, src.ID)
				mu.Unlock()
			}()
			st, err := open(ctx, src.ID, since)
			if err != nil {
				if ctx.Err() == nil {
					emit(LogLine{Time: time.Now(), Source: src.Label, Note: true, Text: "logs unavailable: " + err.Error()})
				}
				return
			}
			for done := false; !done; {
				select {
				case l, ok := <-st.Lines:
					if !ok {
						done = true
						break
					}
					l.Source = src.Label
					if !emit(l) {
						return
					}
				case <-ctx.Done():
					return
				}
			}
			if ctx.Err() == nil {
				emit(LogLine{Time: time.Now(), Source: src.Label, Note: true, Text: "— stopped —"})
			}
		}()
	}

	for _, src := range sources {
		follow(src, time.Time{})
	}
	go func() {
		for starts != nil {
			select {
			case src, ok := <-starts:
				if !ok {
					starts = nil
					break
				}
				follow(src, time.Now())
			case <-ctx.Done():
				starts = nil
			}
		}
		<-ctx.Done()
		wg.Wait()
		close(out)
		errc <- nil
	}()
	labels := make([]string, 0, len(sources))
	for _, s := range sources {
		labels = append(labels, s.Label)
	}
	return &LogStream{Lines: out, Err: errc, Sources: labels}
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
