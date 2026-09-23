package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/tail"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
)

// logBatchWindow is how long a drain waits to accumulate lines before
// handing them to the UI. A chatty container emits thousands of lines a
// second; one tea.Msg per line would spend the whole frame budget in the
// message loop and the viewport would visibly stutter.
const logBatchWindow = 80 * time.Millisecond

// logBatchMax caps a single batch so one burst cannot monopolize a frame.
const logBatchMax = 500

// LogBatchMsg carries a drained batch of log lines.
type LogBatchMsg struct {
	Lines []docker.LogLine
	// Gen identifies which stream produced these. A batch from a stream
	// the view has already replaced is dropped — without this, escaping
	// out of one container's logs and into another's interleaves the two.
	Gen int
}

// LogClosedMsg signals the stream ended.
type LogClosedMsg struct {
	Err error
	Gen int
}

// LogsView is a follow-mode tail of one container's output.
type LogsView struct {
	tail   *tail.Model
	client *docker.Client
	cancel context.CancelFunc
	stream *docker.LogStream

	err error

	containerID   string
	containerName string
	filter        string

	gen        int
	loading    bool
	timestamps bool
}

// NewLogsView opens a tail for one container. The stream itself starts on
// Init so construction stays side-effect free.
func NewLogsView(client *docker.Client, id, name string) *LogsView {
	t := tail.New()
	t.SetFollow(true)
	t.SetBackground(pkgTheme.Bg)
	return &LogsView{
		client:        client,
		tail:          t,
		containerID:   id,
		containerName: name,
		loading:       true,
	}
}

// Title is the container name, for the border title.
func (v *LogsView) Title() string { return v.containerName }

// ContainerID is the container being tailed.
func (v *LogsView) ContainerID() string { return v.containerID }

// Follow reports whether the viewport is pinned to the bottom.
func (v *LogsView) Follow() bool { return v.tail.Follow() }

// Timestamps reports whether the daemon timestamp is being rendered.
func (v *LogsView) Timestamps() bool { return v.timestamps }

// Init starts the stream.
func (v *LogsView) Init() tea.Cmd { return v.start() }

// start opens a fresh stream, canceling any previous one. The generation
// counter bumps so in-flight batches from the old stream are discarded.
func (v *LogsView) start() tea.Cmd {
	v.Stop()
	v.gen++
	v.loading = true
	v.err = nil

	gen := v.gen
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel

	id, ts := v.containerID, v.timestamps
	client := v.client
	return func() tea.Msg {
		st, err := client.StreamLogs(ctx, id, 500, ts)
		if err != nil {
			cancel()
			return LogClosedMsg{Gen: gen, Err: err}
		}
		return logStartedMsg{Gen: gen, Stream: st}
	}
}

// logStartedMsg hands the opened stream back to the view on the UI
// goroutine, so the view never touches a stream field from a tea.Cmd.
type logStartedMsg struct {
	Stream *docker.LogStream
	Gen    int
}

// Stop cancels the stream. Safe to call repeatedly.
func (v *LogsView) Stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	v.stream = nil
}

// ToggleTimestamps flips timestamp rendering. The daemon decides whether
// to send them, so this restarts the stream rather than reformatting the
// buffer — the already-received lines simply do not carry a stamp.
func (v *LogsView) ToggleTimestamps() tea.Cmd {
	v.timestamps = !v.timestamps
	return v.start()
}

// ToggleFollow pins or unpins the viewport to the tail.
func (v *LogsView) ToggleFollow() { v.tail.SetFollow(!v.tail.Follow()) }

// Update folds stream messages into the buffer.
func (v *LogsView) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case logStartedMsg:
		if m.Gen != v.gen {
			return nil
		}
		v.stream = m.Stream
		v.loading = false
		return v.drain()

	case LogBatchMsg:
		if m.Gen != v.gen {
			return nil
		}
		v.loading = false
		lines := make([]string, 0, len(m.Lines))
		for _, l := range m.Lines {
			lines = append(lines, v.render(l))
		}
		v.tail.AppendLines(lines)
		return v.drain()

	case LogClosedMsg:
		if m.Gen != v.gen {
			return nil
		}
		v.loading = false
		if m.Err != nil {
			v.err = docker.FormatUserError(m.Err)
			return nil
		}
		v.tail.AppendLine(style.Muted.Render("— stream ended —"))
		return nil
	}
	return nil
}

// render formats one log line: an optional dimmed timestamp, then the text
// in red when it came from stderr.
func (v *LogsView) render(l docker.LogLine) string {
	text := l.Text
	if l.Stderr {
		text = lipgloss.NewStyle().Foreground(style.ColorRed).Render(text)
	}
	if v.timestamps && !l.Time.IsZero() {
		return style.Muted.Render(l.Time.Local().Format("15:04:05.000")) + " " + text
	}
	return text
}

// drain returns a command that collects whatever the stream has produced
// within the batch window. Returning a fresh drain command from each batch
// is what keeps the tail running — bubbletea has no long-lived
// subscriptions, so the loop is re-armed message by message.
func (v *LogsView) drain() tea.Cmd {
	st := v.stream
	gen := v.gen
	if st == nil {
		return nil
	}
	return func() tea.Msg {
		batch := make([]docker.LogLine, 0, 64)
		deadline := time.NewTimer(logBatchWindow)
		defer deadline.Stop()

		for {
			select {
			case l, ok := <-st.Lines:
				if !ok {
					if len(batch) > 0 {
						return LogBatchMsg{Gen: gen, Lines: batch}
					}
					return LogClosedMsg{Gen: gen}
				}
				batch = append(batch, l)
				if len(batch) >= logBatchMax {
					return LogBatchMsg{Gen: gen, Lines: batch}
				}
			case err := <-st.Err:
				if err != nil {
					return LogClosedMsg{Gen: gen, Err: err}
				}
			case <-deadline.C:
				// An idle container produces an empty batch; returning it
				// re-arms the drain without burning CPU on a busy loop.
				return LogBatchMsg{Gen: gen, Lines: batch}
			}
		}
	}
}

// UpdateTable forwards scroll keys to the viewport.
func (v *LogsView) UpdateTable(msg tea.Msg) tea.Cmd { return v.tail.Update(msg) }

// Resize re-lays the viewport.
func (v *LogsView) Resize(width, height int) { v.tail.Resize(width, height) }

// Count is the number of lines currently visible under the filter.
func (v *LogsView) Count() int { return v.tail.VisibleCount() }

// Loading reports whether the stream is still opening.
func (v *LogsView) Loading() bool { return v.loading }

// SetFilter applies a regex filter to the buffered lines.
func (v *LogsView) SetFilter(f string) {
	v.filter = f
	v.tail.SetFilter(f)
}

// HandleKey maps a keystroke to an app action.
func (v *LogsView) HandleKey(key string) (string, string) {
	switch key {
	case "f":
		return "toggle_follow", ""
	case "T":
		return "toggle_timestamps", ""
	case "o":
		return "inspect_container", v.containerID
	case "e":
		return "exec", v.containerID
	case "x":
		return "stop", v.containerID
	case "R":
		return "restart", v.containerID
	}
	return "", ""
}

// View renders the viewport.
func (v *LogsView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if !v.tail.Ready() {
		return ""
	}
	if v.tail.LineCount() == 0 && !v.loading {
		return style.Muted.Render("  (no output)")
	}
	return v.tail.View()
}

// Refresh restarts the stream from scratch. `r` on a log tail means "start
// over", which is the only sensible reading when the data is a stream.
func (v *LogsView) Refresh() tea.Cmd { return v.start() }

// Status is the extra detail the app puts in the border title: follow
// state and the active filter.
func (v *LogsView) Status() string {
	parts := make([]string, 0, 2)
	if v.tail.Follow() {
		parts = append(parts, "follow")
	} else {
		parts = append(parts, "paused")
	}
	if v.filter != "" {
		parts = append(parts, "/"+v.filter)
	}
	return strings.Join(parts, " ")
}
