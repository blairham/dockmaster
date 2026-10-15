// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// OutputStartFunc starts the command a build runs. It is the app's closure
// over the docker CLI, so nothing here decides what runs.
type OutputStartFunc func(ctx context.Context) (*docker.CommandStream, error)

// OutputStartedMsg reports a build's command started, or why it could not.
type OutputStartedMsg struct {
	Err    error
	Stream *docker.CommandStream
	Gen    int64
}

// OutputBatchMsg carries a drained batch of a build's output lines.
type OutputBatchMsg struct {
	Lines []string
	Gen   int64
}

// OutputDoneMsg reports a build's command finished: nil for a zero exit.
type OutputDoneMsg struct {
	Err error
	Gen int64
}

// statusRunning is a running command's status, for the title.
const statusRunning = "running"

// MaxBuildLines is how much of a build's output is kept, as a log's
// backlog is: the oldest lines go first.
const MaxBuildLines = 20000

// buildGen numbers every build: its ID, carried by each of its messages.
var buildGen atomic.Int64

// Build is one streamed CLI command — an image build, a compose build —
// that runs in the background (#121). The app owns it, not a view: it is
// drained whichever view is showing, keeps its own bounded output, and an
// OutputView is only a window onto it, so leaving that view never stops
// the command. Only the app's update loop touches a Build.
type Build struct {
	Started  time.Time
	Finished time.Time
	err      error
	cancel   context.CancelFunc
	stream   *docker.CommandStream
	start    OutputStartFunc
	// Title names the command ("build app:dev"), Name its subject
	// ("app:dev"), for the flash when it ends.
	Title string
	Name  string
	// Project is the compose project a compose build is for, "" for an
	// image build; Context the docker context it runs against.
	Project string
	Context string
	// ThenAction and ThenParam are what the app runs when the command
	// succeeds — compose build's "then up" — "" for nothing.
	ThenAction string
	ThenParam  string
	lines      []string
	id         int64
	running    bool
	stopped    bool
}

// NewBuild numbers a build; it starts on Start.
func NewBuild(title, name string, start OutputStartFunc) *Build {
	return &Build{Title: title, Name: name, start: start, id: buildGen.Add(1)}
}

// ID is the build's number, which its messages carry.
func (b *Build) ID() int64 { return b.id }

// Running reports whether the command is still going.
func (b *Build) Running() bool { return b.running }

// Err is why the command failed, nil while it runs or when it succeeded.
func (b *Build) Err() error { return b.err }

// Lines is the output kept so far, the newest last.
func (b *Build) Lines() []string { return b.lines }

// Start runs the command.
func (b *Build) Start() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel, b.running, b.Started = cancel, true, time.Now()
	id, start := b.id, b.start
	return func() tea.Msg {
		st, err := start(ctx)
		return OutputStartedMsg{Gen: id, Stream: st, Err: err}
	}
}

// Stop cancels a running command. Its end still arrives as an
// OutputDoneMsg, which the drain reads, so the output up to the stop is
// kept. Safe to call repeatedly.
func (b *Build) Stop() {
	if b.cancel != nil && b.running {
		b.stopped = true
		b.cancel()
	}
}

// Canceled reports the command ended because it was stopped.
func (b *Build) Canceled() bool {
	return !b.running && (b.stopped || errors.Is(b.err, context.Canceled))
}

// Update folds one of the build's messages in. It returns the lines it
// added — for a view attached to the build — the command that reads the
// next batch, and whether the build has now ended.
func (b *Build) Update(msg tea.Msg) (added []string, next tea.Cmd, ended bool) {
	if !b.running {
		return nil, nil, false
	}
	switch m := msg.(type) {
	case OutputStartedMsg:
		if m.Err != nil {
			return b.finish(m.Err), nil, true
		}
		b.stream = m.Stream
		return nil, b.drain(), false
	case OutputBatchMsg:
		added = make([]string, 0, len(m.Lines))
		for _, l := range m.Lines {
			added = append(added, logTextColor(sanitizeLogText(l)))
		}
		b.append(added...)
		return added, b.drain(), false
	case OutputDoneMsg:
		return b.finish(m.Err), nil, true
	}
	return nil, nil, false
}

// append keeps lines, dropping the oldest past MaxBuildLines.
func (b *Build) append(lines ...string) {
	b.lines = append(b.lines, lines...)
	if over := len(b.lines) - MaxBuildLines; over > 0 {
		b.lines = append(b.lines[:0:0], b.lines[over:]...)
	}
}

// finish records how the command ended and says so under its output.
func (b *Build) finish(err error) []string {
	if b.stopped && err != nil {
		err = context.Canceled
	}
	b.running, b.err, b.Finished, b.stream = false, err, time.Now(), nil
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	took := b.Finished.Sub(b.Started).Round(100 * time.Millisecond)
	var line string
	switch {
	case b.Canceled():
		line = style.Error.Render(fmt.Sprintf("— stopped after %s —", took))
	case err != nil:
		line = style.Error.Render(fmt.Sprintf("— failed after %s: %v —", took, err))
	default:
		line = style.Success.Render(fmt.Sprintf("— done in %s —", took))
	}
	b.append(line)
	return []string{line}
}

// Status is the build's state: running, done, failed or canceled.
func (b *Build) Status() string {
	switch {
	case b.running:
		return statusRunning
	case b.Canceled():
		return "canceled"
	case b.err != nil:
		return "failed"
	}
	return "done"
}

// drain collects what the command printed within the batch window, the way
// the log view does, and reads its exit once the output has ended.
func (b *Build) drain() tea.Cmd {
	st, id := b.stream, b.id
	if st == nil {
		return nil
	}
	return func() tea.Msg {
		batch, closed, err := collectBatch(st.Lines, nil, 64)
		switch {
		case err != nil:
			return OutputDoneMsg{Gen: id, Err: err}
		case closed && len(batch) == 0:
			return OutputDoneMsg{Gen: id, Err: <-st.Done}
		}
		return OutputBatchMsg{Gen: id, Lines: batch}
	}
}
