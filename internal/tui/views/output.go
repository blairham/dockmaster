// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/tail"
	"github.com/charmbracelet/x/ansi"

	"github.com/blairham/dockmaster/internal/tui/style"
)

// OutputView is a window onto one Build (#121) — an image build, a compose
// build — in a log-style pane: autoscroll, wrap, copy and save, as a log.
// The app owns the build and drains it whichever view is showing; the view
// only shows what it has kept so far and what arrives while it is open.
// Leaving the view detaches from the build, never stops it: stopping is x,
// which the app confirms.
type OutputView struct {
	build      *Build
	tail       *tail.Model
	filter     string
	width      int
	fullscreen bool
}

// NewOutputView attaches a view to b, showing everything it has printed.
func NewOutputView(b *Build) *OutputView {
	t := tail.New()
	t.SetMaxLines(MaxBuildLines)
	t.SetFollow(true)
	paintTailBackground(t)
	t.AppendLines(append([]string(nil), b.Lines()...))
	return &OutputView{build: b, tail: t}
}

// Build is the build the view is attached to.
func (v *OutputView) Build() *Build { return v.build }

// Title names the command, for the border.
func (v *OutputView) Title() string { return v.build.Title }

// Init does nothing: the build is already running, or done.
func (v *OutputView) Init() tea.Cmd { return nil }

// Refresh does nothing: r — or a compose action finishing elsewhere — must
// never run a build again. Running it again is the key that opened it.
func (v *OutputView) Refresh() tea.Cmd { return nil }

// Append shows lines the build has just added.
func (v *OutputView) Append(lines []string) { v.tail.AppendLines(lines) }

// Update takes the mouse wheel; the build's own messages go to the app.
func (v *OutputView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(tea.MouseWheelMsg); ok {
		return scrollTail(v.tail, m)
	}
	return nil
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

// HandleKey maps the log keys this view shares — s autoscroll, w wrap,
// f fullscreen, c copy, ctrl-s save — and x, which stops the build after
// a confirm.
func (v *OutputView) HandleKey(key string) (string, string) {
	switch key {
	case "x":
		if v.build.Replay {
			return "", ""
		}
		if v.build.Running() {
			return "confirm_stop_build", strconv.FormatInt(v.build.ID(), 10)
		}
		return "build_not_running", v.build.Title
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

// Status is the build's state, for the border title.
func (v *OutputView) Status() string { return v.build.Status() }

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
	b := v.build
	switch {
	case b.Running():
		run = style.StateRestarting.Render(fmt.Sprintf(
			"running %ds — esc leaves it running, x stops it", int(time.Since(b.Started).Seconds()),
		))
	case b.Err() != nil:
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
	if v.tail.LineCount() == 0 && v.build.Running() {
		body = style.Muted.Render("  starting…")
	}
	return v.Indicator() + "\n" + body
}
