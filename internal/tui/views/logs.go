package views

import (
	"context"
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/tail"
	"github.com/charmbracelet/x/ansi"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
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
	// node, when set, is the Kubernetes node container this one runs in:
	// the stream is read through crictl inside it, and the docker
	// lifecycle keys do not apply.
	node   string
	filter string

	gen        int
	tailLines  int
	loading    bool
	timestamps bool
	// since, when non-zero, starts the stream that long ago instead of at
	// the last tailLines lines (the 1–5 keys).
	since time.Duration
	width int
	// fullscreen mirrors the app's, for the indicator line.
	fullscreen bool
}

// SetFullscreen tells the view whether the app has hidden header and
// crumbs for it, so the indicator line can say so.
func (v *LogsView) SetFullscreen(on bool) { v.fullscreen = on }

// LogRanges are the time ranges the digit keys pick, as k9s's log view:
// 0 is the usual backlog, 1–5 everything from that long ago.
var LogRanges = []struct {
	Key   string
	Label string
	Since time.Duration
}{
	{Key: "0", Label: "tail"},
	{Key: "1", Label: "1m", Since: time.Minute},
	{Key: "2", Label: "5m", Since: 5 * time.Minute},
	{Key: "3", Label: "15m", Since: 15 * time.Minute},
	{Key: "4", Label: "30m", Since: 30 * time.Minute},
	{Key: "5", Label: "1h", Since: time.Hour},
}

// ClaimsDigits reports that this view uses the digit keys itself — for
// time ranges — so they do not switch views here, as in k9s.
func (v *LogsView) ClaimsDigits() bool { return true }

// SetRange restarts the stream from since ago (zero: the usual backlog).
// The buffer is cleared first: the new stream re-sends lines the old one
// already showed.
func (v *LogsView) SetRange(since time.Duration) tea.Cmd {
	v.since = since
	v.tail.Clear()
	return v.start()
}

// Range is the time range in force, 0 for the usual backlog.
func (v *LogsView) Range() time.Duration { return v.since }

// ToggleWrap soft-wraps long lines, or stops wrapping them.
func (v *LogsView) ToggleWrap() { v.tail.SetWrap(!v.tail.Wrap()) }

// Wrap reports whether long lines wrap.
func (v *LogsView) Wrap() bool { return v.tail.Wrap() }

// Clear empties the buffer; the stream keeps appending.
func (v *LogsView) Clear() { v.tail.Clear() }

// Mark draws a rule across the log at its current end, stamped with now —
// k9s's m — so what arrives after a moment (a retry, a deploy) is easy to
// find again. It scrolls with the log and is copied and saved with it, and
// it is a tail marker, so filtering for what matters after the mark — the
// usual next step — still shows where the mark is.
func (v *LogsView) Mark(now time.Time) {
	stamp := " " + now.Local().Format("15:04:05") + " "
	side := max((v.width-lipgloss.Width(stamp))/2, 3)
	rule := strings.Repeat("─", side) + stamp + strings.Repeat("─", side)
	v.tail.AppendMarker(lipgloss.NewStyle().Foreground(style.ColorDockerBlue).Bold(true).Render(rule))
}

// PlainText is what the view shows — filtered, styling stripped — for
// saving or copying.
func (v *LogsView) PlainText() (string, int) {
	lines := v.tail.VisibleLines()
	for i, l := range lines {
		lines[i] = ansi.Strip(l)
	}
	return strings.Join(lines, "\n"), len(lines)
}

// NewLogsView opens a tail for one container. The stream itself starts on
// Init so construction stays side-effect free.
func NewLogsView(client *docker.Client, id, name string) *LogsView {
	t := tail.New()
	t.SetFollow(true)
	paintTailBackground(t)
	return &LogsView{
		client:        client,
		tail:          t,
		containerID:   id,
		containerName: name,
		tailLines:     defaultLogTail,
		loading:       true,
	}
}

// NewNodeLogsView tails container id inside Kubernetes node node.
func NewNodeLogsView(client *docker.Client, node, id, name string) *LogsView {
	v := NewLogsView(client, id, name)
	v.node = node
	return v
}

// defaultLogTail is the backlog a log view opens with when config.yaml
// does not set logger.tail.
const defaultLogTail = 500

// Configure sets the backlog length and whether timestamps start on, from
// config.yaml's logger block. Call it before Init; a tail below 1 keeps
// the default.
func (v *LogsView) Configure(tailLines int, timestamps bool) *LogsView {
	if tailLines > 0 {
		v.tailLines = tailLines
	}
	v.timestamps = timestamps
	return v
}

// Limits caps the lines the view keeps (0: no cap) and sets how far back
// it opens (0: the last tailLines lines) — config logger.buffer and
// logger.sinceSeconds.
func (v *LogsView) Limits(buffer int, since time.Duration) *LogsView {
	v.tail.SetMaxLines(buffer)
	v.since = since
	return v
}

// Title is the container name, for the border title.
func (v *LogsView) Title() string { return v.containerName }

// ContainerID is the container being tailed.
func (v *LogsView) ContainerID() string { return v.containerID }

// Follow reports whether the viewport is pinned to the bottom.
func (v *LogsView) Follow() bool { return v.tail.Follow() }

// TailLines is the backlog the stream opens with.
func (v *LogsView) TailLines() int { return v.tailLines }

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

	id, ts, n, node := v.containerID, v.timestamps, v.tailLines, v.node
	var since time.Time
	if v.since > 0 {
		since = time.Now().Add(-v.since)
	}
	client := v.client
	return func() tea.Msg {
		open := client.StreamLogs
		if node != "" {
			open = func(ctx context.Context, id string, n int, ts bool, since time.Time) (*docker.LogStream, error) {
				return client.StreamNodeLogs(ctx, node, id, n, ts, since)
			}
		}
		st, err := open(ctx, id, n, ts, since)
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
	case tea.MouseWheelMsg:
		return scrollTail(v.tail, m)
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
// in whatever colors the container wrote it with.
//
// Lines from stderr are not painted red. stderr is not where errors go, it
// is where logs go: logrus, slog, zap and Go's log package all default to
// it, so a registry writing `level=info` came out as a screen of red that
// read as a failing service. k9s and `docker logs` do not color by stream
// either.
func (v *LogsView) render(l docker.LogLine) string {
	text := logTextColor(sanitizeLogText(l.Text))
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
func (v *LogsView) UpdateTable(msg tea.Msg) tea.Cmd { return scrollTail(v.tail, msg) }

// Resize re-lays the viewport, one row under the indicator line.
func (v *LogsView) Resize(width, height int) {
	v.width = width
	v.tail.Resize(width, max(height-1, 1))
}

// Indicator is the toggle line k9s draws at the top of a log —
// Autoscroll, FullScreen, Timestamps, Wrap, each On or Off — so the state
// of every toggle is on screen, not only in help.
func (v *LogsView) Indicator() string {
	label := lipgloss.NewStyle().Foreground(style.ColorDockerBlue).Bold(true)
	state := func(on bool) string {
		if on {
			return style.Success.Render("On")
		}
		return style.Muted.Render("Off")
	}
	parts := []string{
		label.Render("Autoscroll:") + state(v.tail.Follow()),
		label.Render("FullScreen:") + state(v.fullscreen),
		label.Render("Timestamps:") + state(v.timestamps),
		label.Render("Wrap:") + state(v.tail.Wrap()),
	}
	return lipgloss.PlaceHorizontal(v.width, lipgloss.Center, strings.Join(parts, "      "))
}

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
	if action, param := logKey(key); action != "" {
		return action, param
	}
	if v.node != "" {
		return v.nodeKey(key)
	}
	switch key {
	case "o":
		return "inspect_container", v.containerID
	case "s":
		return "exec", v.containerID
	case "x":
		return "stop", v.containerID
	case "R":
		return "restart", v.containerID
	}
	return "", ""
}

// logKey maps the keys every log view shares, docker's and a kind node's:
// follow, timestamps, the time ranges, wrap, fullscreen, copy, save,
// clear and mark. Following k9s, except f, which was already follow here.
func logKey(key string) (string, string) {
	for _, r := range LogRanges {
		if key == r.Key {
			return "log_range", r.Label
		}
	}
	switch key {
	case "f":
		return "toggle_follow", ""
	case "T":
		return "toggle_timestamps", ""
	case "w":
		return "log_wrap", ""
	case "F":
		return "fullscreen", ""
	case "c":
		return "log_copy", ""
	case "ctrl+s":
		return "log_save", ""
	case "ctrl+k":
		return "log_clear", ""
	case "m":
		return "log_mark", ""
	}
	return "", ""
}

// nodeKey is HandleKey for a container inside a node: follow, timestamps,
// inspect and shell. Stop and restart would go to the docker daemon with a
// containerd ID it has never heard of, so they are not offered.
func (v *LogsView) nodeKey(key string) (string, string) {
	p := NodeParam(v.node, v.containerID, v.containerName)
	switch key {
	case "o":
		return "node_inspect", p
	case "s":
		return "node_shell", p
	}
	return "", ""
}

// Node is the Kubernetes node the tailed container runs in, "" for a
// docker container.
func (v *LogsView) Node() string { return v.node }

// View renders the viewport.
func (v *LogsView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if !v.tail.Ready() {
		return ""
	}
	body := v.tail.View()
	if v.tail.LineCount() == 0 && !v.loading {
		body = style.Muted.Render("  (no output)")
	}
	return v.Indicator() + "\n" + body
}

// Refresh restarts the stream from scratch. `r` on a log tail means "start
// over", which is the only sensible reading when the data is a stream.
func (v *LogsView) Refresh() tea.Cmd { return v.start() }

// Status is the extra detail the app puts in the border title: follow
// state and the active filter.
func (v *LogsView) Status() string {
	// The toggles are on the indicator line; the title carries the range
	// and filter, as k9s's "[1m]".
	parts := make([]string, 0, 2)
	if v.since > 0 {
		parts = append(parts, "last "+rangeLabel(v.since))
	}
	if v.filter != "" {
		parts = append(parts, "/"+v.filter)
	}
	return strings.Join(parts, " ")
}

// paintTailBackground gives a tail the theme's background only when the
// theme paints one. dockmaster's theme does not — it leaves the terminal's own
// background showing, as every table does — and painting Bg anyway turned the
// log and inspect panes solid black, with the terminal's color bleeding back
// through wherever a log line's own color codes reset it.
func paintTailBackground(t *tail.Model) {
	if pkgTheme.PaintBackground {
		t.SetBackground(pkgTheme.Bg)
	}
}

// nonColorEscape matches every terminal escape but SGR (color and weight,
// final byte `m`): cursor movement, erase-in-line, OSC titles and links.
var nonColorEscape = regexp.MustCompile(
	`\x1b\[[0-?]*[ -/]*[@-ln-~]` + // CSI other than SGR
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)` + // OSC, BEL- or ST-terminated
		`|\x1b[@-Z\\-_]`, // two-byte escapes
)

// logTabWidth is how far a tab expands. Terminals disagree about tab stops
// inside a frame, and lipgloss measures a tab as zero cells wide.
const logTabWidth = 4

// sanitizeLogText makes a raw log line safe to place inside a frame.
//
// Container output is written for a terminal, not for a cell in someone
// else's layout. A TTY container ends every line in \r; a progress bar
// rewrites itself with bare \r; systemd erases to the end of the line with
// \x1b[K. Inside dockmaster's frame each of those acts on the frame itself —
// a \r returns the cursor to column 0 and the next cells overwrite the box's
// left edge, an erase wipes out its right border. So: keep only what a
// terminal would finally show after the last carriage return, drop every
// escape that is not color, expand tabs, and remove the remaining control
// characters.
func sanitizeLogText(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	s = nonColorEscape.ReplaceAllString(s, "")
	s = dropBrokenEscapes(s)
	s = strings.ReplaceAll(s, "\t", strings.Repeat(" ", logTabWidth))
	return strings.Map(func(r rune) rune {
		if r == '\x1b' || r >= ' ' && r != 0x7f {
			return r
		}
		return -1
	}, s)
}

// colorEscape is one complete SGR sequence.
var colorEscape = regexp.MustCompile(`^\x1b\[[0-9;:]*m`)

// dropBrokenEscapes removes every escape that is not a complete color
// sequence, keeping the text around it.
//
// Programs that shorten their own output cut escapes in half: systemd ends
// "kubelet.service\x1b[0m" as "kubelet.service\x1b[…" when the unit name is
// too long for its console. Left in, the terminal reads the half-sequence as
// the start of a control sequence, swallows text looking for its end, and
// leaves its own state wrong — on a real kind node it dropped the canvas
// color for every row after the line. The complete non-color escapes are
// already gone by now, so any ESC that does not start a color code is one of
// these; it goes, with the "[" and parameter bytes that came with it.
func dropBrokenEscapes(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		if m := colorEscape.FindString(s[i:]); m != "" {
			b.WriteString(m)
			i += len(m)
			continue
		}
		i++ // the ESC
		if i < len(s) && s[i] == '[' {
			i++
			for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
				i++
			}
		}
	}
	return b.String()
}

// logTextFg is the raw SGR for the log foreground, derived in applyTheme
// from lipgloss so it honors the terminal's color profile.
var logTextFg string

// sgrFor is the SGR that starts text in c.
func sgrFor(c color.Color) string {
	sample := lipgloss.NewStyle().Foreground(c).Render("x")
	if i := strings.IndexByte(sample, 'x'); i > 0 && strings.HasPrefix(sample, "\x1b[") {
		return sample[:i]
	}
	return ""
}

// sgrSeq matches one SGR escape.
var sgrSeq = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// logTextColor draws a line in the log foreground, k9s's lightskyblue. A
// line that brings its own colors keeps them for their extent; each SGR that
// leaves the foreground at the terminal default — a bare reset, or 39 — is
// followed by the log color again, so the uncolored remainder of the line
// does not fall back to white.
func logTextColor(text string) string {
	if logTextFg == "" {
		return text
	}
	text = sgrSeq.ReplaceAllStringFunc(text, func(seq string) string {
		if leavesDefaultForeground(seq[2 : len(seq)-1]) {
			return seq + logTextFg
		}
		return seq
	})
	return logTextFg + text + "\x1b[0m"
}

// leavesDefaultForeground reports whether an SGR ends with the foreground
// at the terminal default. Parameters apply in order, so "0;32" — reset,
// then green — leaves green and must not be overridden, while "0" or "39"
// leaves the default. An SGR that never touches the foreground (bold, a
// background) leaves whatever was set before it, so it does not count.
// Extended-color arguments are skipped so the zeros in 48;2;0;0;0 are not
// read as resets.
func leavesDefaultForeground(params string) bool {
	if params == "" {
		return true
	}
	touched, isDefault := false, false
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		n, err := strconv.Atoi(ps[i])
		if ps[i] == "" {
			n, err = 0, nil
		}
		if err != nil {
			continue
		}
		switch {
		case n == 0, n == 39:
			touched, isDefault = true, true
		case n >= 30 && n <= 37, n >= 90 && n <= 97:
			touched, isDefault = true, false
		case n == 38, n == 48, n == 58:
			if n == 38 {
				touched, isDefault = true, false
			}
			if i+1 < len(ps) && ps[i+1] == "2" {
				i += 4
			} else {
				i += 2
			}
		}
	}
	return touched && isDefault
}

// rangeLabel names a log's time range: a digit key's own label (5m, 1h),
// else the duration without its zero units (90s, 2m30s, 1h30m).
func rangeLabel(d time.Duration) string {
	for _, r := range LogRanges {
		if r.Since == d {
			return r.Label
		}
	}
	s := d.String()
	for _, zero := range []string{"m0s", "h0m"} {
		if strings.HasSuffix(s, zero) {
			s = strings.TrimSuffix(s, zero[1:])
		}
	}
	return s
}
