// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/tail"
	"github.com/charmbracelet/x/ansi"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// OutputStartFunc starts the command an output view shows. It is the app's
// closure over the docker CLI, so the view never decides what runs.
type OutputStartFunc func(ctx context.Context) (*docker.CommandStream, error)

// OutputStartedMsg reports the command started, or why it could not.
type OutputStartedMsg struct {
	Err    error
	Stream *docker.CommandStream
	Gen    int64
}

// OutputBatchMsg carries a drained batch of output lines.
type OutputBatchMsg struct {
	Lines []string
	Gen   int64
}

// OutputDoneMsg reports the command finished: nil for a zero exit.
type OutputDoneMsg struct {
	Err error
	Gen int64
}

// statusRunning is a running command's status, for the title.
const statusRunning = "running"

// outputGen numbers every command any output view starts, across views: a
// message from a view since replaced can never match its successor, which
// a per-view counter starting at 1 each time could.
var outputGen atomic.Int64

// OutputView is a command's output as it runs — an image build, a compose
// build — in a log-style pane: autoscroll, wrap, copy and save, as a log.
// A failure leaves the output on screen with the reason under it, rather
// than only a flash. Leaving the view stops the command: it is the only
// place its output goes.
type OutputView struct {
	started  time.Time
	finished time.Time
	err      error
	tail     *tail.Model
	cancel   context.CancelFunc
	stream   *docker.CommandStream
	start    OutputStartFunc
	title    string
	// thenAction and thenParam are what the app runs when the command
	// succeeds — compose build's "then up" — "" for nothing.
	thenAction string
	thenParam  string
	filter     string
	gen        int64
	width      int
	running    bool
	fullscreen bool
}

// NewOutputView builds the view; the command starts on Init.
func NewOutputView(title string, start OutputStartFunc) *OutputView {
	t := tail.New()
	t.SetFollow(true)
	paintTailBackground(t)
	return &OutputView{title: title, start: start, tail: t}
}

// SetThen names the action the app runs once the command succeeds.
func (v *OutputView) SetThen(action, param string) { v.thenAction, v.thenParam = action, param }

// Then is the action to run on success, "" for none.
func (v *OutputView) Then() (string, string) { return v.thenAction, v.thenParam }

// Title names the command, for the border.
func (v *OutputView) Title() string { return v.title }

// Gen is the command's number, to match its messages.
func (v *OutputView) Gen() int64 { return v.gen }

// Running reports whether the command is still going.
func (v *OutputView) Running() bool { return v.running }

// Err is why the command failed, nil while it runs or when it succeeded.
func (v *OutputView) Err() error { return v.err }

// Init starts the command.
func (v *OutputView) Init() tea.Cmd {
	v.Stop()
	v.gen = outputGen.Add(1)
	v.running, v.err, v.started, v.finished = true, nil, time.Now(), time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	gen, start := v.gen, v.start
	return func() tea.Msg {
		st, err := start(ctx)
		return OutputStartedMsg{Gen: gen, Stream: st, Err: err}
	}
}

// Refresh does nothing: r — or a compose action finishing elsewhere — must
// never run a build again. Running it again is the key that opened it.
func (v *OutputView) Refresh() tea.Cmd { return nil }

// Stop kills a running command. Safe to call repeatedly.
func (v *OutputView) Stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	if v.running {
		v.running = false
		v.finished = time.Now()
		v.err = context.Canceled
	}
}

// Update folds the command's messages in; one from another command is
// dropped by its number.
func (v *OutputView) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.MouseWheelMsg:
		return scrollTail(v.tail, m)
	case OutputStartedMsg:
		if m.Gen != v.gen || !v.running {
			return nil
		}
		if m.Err != nil {
			v.finish(m.Err)
			return nil
		}
		v.stream = m.Stream
		return v.drain()
	case OutputBatchMsg:
		if m.Gen != v.gen || !v.running {
			return nil
		}
		lines := make([]string, 0, len(m.Lines))
		for _, l := range m.Lines {
			lines = append(lines, logTextColor(sanitizeLogText(l)))
		}
		v.tail.AppendLines(lines)
		return v.drain()
	case OutputDoneMsg:
		if m.Gen != v.gen || !v.running {
			return nil
		}
		v.finish(m.Err)
	}
	return nil
}

// finish records how the command ended and says so under its output.
func (v *OutputView) finish(err error) {
	v.running, v.err, v.finished, v.stream = false, err, time.Now(), nil
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	took := v.finished.Sub(v.started).Round(100 * time.Millisecond)
	if err != nil {
		v.tail.AppendLine(style.Error.Render(fmt.Sprintf("— failed after %s: %v —", took, err)))
		return
	}
	v.tail.AppendLine(style.Success.Render(fmt.Sprintf("— done in %s —", took)))
}

// drain collects what the command printed within the batch window, the way
// the log view does, and reads its exit once the output has ended.
func (v *OutputView) drain() tea.Cmd {
	st, gen := v.stream, v.gen
	if st == nil {
		return nil
	}
	return func() tea.Msg {
		batch, closed, err := collectBatch(st.Lines, nil, 64)
		switch {
		case err != nil:
			return OutputDoneMsg{Gen: gen, Err: err}
		case closed && len(batch) == 0:
			return OutputDoneMsg{Gen: gen, Err: <-st.Done}
		}
		return OutputBatchMsg{Gen: gen, Lines: batch}
	}
}

// ToggleFollow pins or unpins the view to the newest line.
func (v *OutputView) ToggleFollow() { v.tail.SetFollow(!v.tail.Follow()) }

// Follow reports whether the view follows the newest line.
func (v *OutputView) Follow() bool { return v.tail.Follow() }

// ToggleWrap soft-wraps long lines, or stops wrapping them.
func (v *OutputView) ToggleWrap() { v.tail.SetWrap(!v.tail.Wrap()) }

// Wrap reports whether long lines wrap.
func (v *OutputView) Wrap() bool { return v.tail.Wrap() }

// SetFullscreen tells the view whether the app has hidden header and
// crumbs for it, for the indicator line.
func (v *OutputView) SetFullscreen(on bool) { v.fullscreen = on }

// PlainText is what the view shows — filtered, styling stripped — for
// saving or copying.
func (v *OutputView) PlainText() (string, int) {
	lines := v.tail.VisibleLines()
	for i, l := range lines {
		lines[i] = ansi.Strip(l)
	}
	return strings.Join(lines, "\n"), len(lines)
}

// HandleKey maps the log keys this view shares: s autoscroll, w wrap,
// f fullscreen, c copy, ctrl-s save.
func (v *OutputView) HandleKey(key string) (string, string) {
	switch key {
	case "s":
		return "output_follow", ""
	case "w":
		return "output_wrap", ""
	case "f":
		return actFullscreen, ""
	case "c":
		return "output_copy", ""
	case KeyCtrlS:
		return "output_save", ""
	}
	return "", ""
}

// UpdateTable forwards scroll keys to the viewport.
func (v *OutputView) UpdateTable(msg tea.Msg) tea.Cmd { return scrollTail(v.tail, msg) }

// Resize re-lays the viewport, one row under the indicator line.
func (v *OutputView) Resize(width, height int) {
	v.width = width
	v.tail.Resize(width, max(height-1, 1))
}

// Count is the number of lines shown under the filter.
func (v *OutputView) Count() int { return v.tail.VisibleCount() }

// Loading is false: the indicator line says the command is running, and
// how long for, which a spinner would not.
func (v *OutputView) Loading() bool { return false }

// SetFilter applies a regex filter to the output.
func (v *OutputView) SetFilter(f string) {
	v.filter = f
	v.tail.SetFilter(f)
}

// Status is the command's state, for the border title.
func (v *OutputView) Status() string {
	switch {
	case v.running:
		return statusRunning
	case errors.Is(v.err, context.Canceled):
		return "canceled"
	case v.err != nil:
		return "failed"
	}
	return "done"
}

// Indicator is the toggle line, as the log view's, with the command's
// state and how long it has run at the end.
func (v *OutputView) Indicator() string {
	label := lipgloss.NewStyle().Foreground(style.ColorDockerBlue).Bold(true)
	state := func(on bool) string {
		if on {
			return style.Success.Render("On")
		}
		return style.Muted.Render("Off")
	}
	var run string
	switch {
	case v.running:
		run = style.StateRestarting.Render(fmt.Sprintf("running %ds — esc stops it", int(time.Since(v.started).Seconds())))
	case v.err != nil:
		run = style.Error.Render(v.Status())
	default:
		run = style.Success.Render(v.Status())
	}
	parts := []string{
		label.Render("Autoscroll:") + state(v.tail.Follow()),
		label.Render("FullScreen:") + state(v.fullscreen),
		label.Render("Wrap:") + state(v.tail.Wrap()),
		run,
	}
	return lipgloss.PlaceHorizontal(v.width, lipgloss.Center, strings.Join(parts, "      "))
}

// View renders the indicator line and the output.
func (v *OutputView) View() string {
	if !v.tail.Ready() {
		return ""
	}
	body := v.tail.View()
	if v.tail.LineCount() == 0 && v.running {
		body = style.Muted.Render("  starting…")
	}
	return v.Indicator() + "\n" + body
}
