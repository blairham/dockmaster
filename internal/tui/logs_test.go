package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// openLogs puts a log view for "web" on screen with two lines in it.
func openLogs(t *testing.T) (*App, *views.LogsView) {
	t.Helper()
	a := newTestApp(t)
	loadContainers(a)
	lv := views.NewLogsView(nil, "abc", "web")
	a.setView(style.ViewLogs, lv)
	a.pushView(style.ViewLogs)
	step(a, views.LogBatchMsg{Lines: []docker.LogLine{
		{Text: "\x1b[32mGET /health 200\x1b[0m"},
		{Text: "POST /orders " + strings.Repeat("x", 200) + "END"},
	}})
	return a, lv
}

// TestLogRangeKeys: in a log the digits pick a time range, as in k9s,
// instead of switching views; the range shows in the border title.
func TestLogRangeKeys(t *testing.T) {
	a, lv := openLogs(t)
	step(a, key("2"))
	if a.view != style.ViewLogs {
		t.Fatalf("2 in a log switched to %v", a.view)
	}
	if lv.Range() != 5*time.Minute || !strings.Contains(lv.Status(), "last 5m") {
		t.Errorf("2: range %v status %q, want the last 5m", lv.Range(), lv.Status())
	}
	if lv.Count() != 0 {
		t.Errorf("a new range kept %d old lines; the restarted stream re-sends them", lv.Count())
	}
	step(a, key("0"))
	if lv.Range() != 0 || strings.Contains(lv.Status(), "last") {
		t.Errorf("0: range %v status %q, want the usual backlog", lv.Range(), lv.Status())
	}
	step(a, key("esc"))
	step(a, key("1"))
	if a.view != style.ViewImages {
		t.Errorf("outside a log 1 opened %v, want images", a.view)
	}
}

func TestLogWrapAndClear(t *testing.T) {
	a, lv := openLogs(t)
	if strings.Contains(render(a), "END") {
		t.Fatal("setup: the long line already fits")
	}
	step(a, key("w"))
	if !lv.Wrap() || !strings.Contains(render(a), "END") || !strings.Contains(render(a), "Wrap:On") {
		t.Errorf("w did not wrap the long line, or the indicator does not say so (wrap %v)", lv.Wrap())
	}
	step(a, key("ctrl+k"))
	if lv.Count() != 0 {
		t.Errorf("ctrl-k left %d lines", lv.Count())
	}
	step(a, views.LogBatchMsg{Lines: []docker.LogLine{{Text: "after clear"}}})
	if !strings.Contains(render(a), "after clear") {
		t.Error("lines stopped arriving after ctrl-k")
	}
}

// TestLogCopyAndSave: c and ctrl-s take what the view shows — filtered,
// styling stripped — to the clipboard and to a file in the state dir.
func TestLogCopyAndSave(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	a, lv := openLogs(t)
	lv.SetFilter("health")

	if cmd := step(a, key("c")); cmd == nil || a.flash != "copied 1 lines" {
		t.Errorf("c: cmd %v flash %q", cmd != nil, a.flash)
	}

	step(a, key("ctrl+s"))
	files, _ := filepath.Glob(filepath.Join(state, "dockmaster", "logs", "web-*.log"))
	if len(files) != 1 {
		t.Fatalf("ctrl-s wrote %v (flash %q %q)", files, a.flash, a.errFlash)
	}
	b, _ := os.ReadFile(files[0])
	if string(b) != "GET /health 200\n" {
		t.Errorf("saved %q, want the filtered line with its styling stripped", b)
	}
	if !strings.Contains(a.flash, files[0]) {
		t.Errorf("flash %q does not say where it saved", a.flash)
	}
}

// TestLogFullscreen: F hides header and crumbs; leaving the log puts back
// what was there, including a header already hidden with ctrl-e.
func TestLogFullscreen(t *testing.T) {
	a, _ := openLogs(t)
	step(a, key("F"))
	if !a.chrome.HeaderHidden || !a.chrome.CrumbsHidden || strings.Contains(render(a), "Context:") {
		t.Fatal("F did not hide the header and crumbs")
	}
	// No header, no crumbs: the box runs from the top line to the bottom.
	lines := strings.Split(strings.TrimRight(render(a), "\n"), "\n")
	if len(lines) != a.height || !strings.Contains(lines[len(lines)-1], "╰") || !strings.Contains(lines[0], "╭") {
		t.Errorf("fullscreen frame: %d lines for a %d-line terminal, first %q last %q",
			len(lines), a.height, lines[0], lines[len(lines)-1])
	}
	step(a, key("esc"))
	if a.chrome.HeaderHidden || a.chrome.CrumbsHidden || a.fullscreen {
		t.Error("leaving the log did not end fullscreen")
	}

	b, _ := openLogs(t)
	step(b, key("ctrl+e")) // the user hides the header first
	step(b, key("F"))
	step(b, key("F"))
	if !b.chrome.HeaderHidden || b.chrome.CrumbsHidden {
		t.Errorf("F twice: header hidden %v crumbs hidden %v, want the header the user hid to stay hidden",
			b.chrome.HeaderHidden, b.chrome.CrumbsHidden)
	}
}

// TestLogsHelpShowsTheLogKeys: help over a log lists the log's keys, whole.
func TestLogsHelpShowsTheLogKeys(t *testing.T) {
	a, _ := openLogs(t)
	step(a, key("?"))
	out := render(a)
	for _, want := range []string{"LOGS", "Last 5m", "Usual backlog", "Wrap", "Fullscreen", "Save to file", "Clear", "Mark"} {
		if !strings.Contains(out, want) {
			t.Errorf("help over a log is missing %q", want)
		}
	}
	if strings.Contains(out, "CONTAINER") {
		t.Error("help over a log still shows the CONTAINER column")
	}
}

// TestLogHeaderAndIndicatorFollowTheLog: in a log the header's digit column
// lists the time ranges the digits now pick, not views they no longer
// reach, and the indicator line tracks every toggle.
func TestLogHeaderAndIndicatorFollowTheLog(t *testing.T) {
	a, _ := openLogs(t)
	header := strings.Join(strings.Split(render(a), "\n")[:6], "\n")
	for _, want := range []string{"<0>", "tail", "<2>", "5m", "<5>", "1h", "Copy", "Clear"} {
		if !strings.Contains(header, want) {
			t.Errorf("log header is missing %q:\n%s", want, header)
		}
	}
	for _, gone := range []string{"Images", "Volumes", "Runtimes"} {
		if strings.Contains(header, gone) {
			t.Errorf("log header still lists the view %q", gone)
		}
	}
	indicator := func() string {
		for _, l := range strings.Split(render(a), "\n") {
			if strings.Contains(l, "Autoscroll:") {
				return strings.Join(strings.Fields(l), " ")
			}
		}
		return ""
	}
	if got := indicator(); !strings.Contains(got, "Autoscroll:On FullScreen:Off Timestamps:Off Wrap:Off") {
		t.Fatalf("indicator = %q", got)
	}
	step(a, key("w"))
	step(a, key("F"))
	step(a, key("k"))
	if got := indicator(); !strings.Contains(got, "Autoscroll:Off FullScreen:On Timestamps:Off Wrap:On") {
		t.Errorf("after w, F, k: indicator = %q", got)
	}
}

// TestLogMark: m draws a stamped rule at the end of what has arrived, so
// the lines after it are the ones that came later — on screen, and in a
// copy.
func TestLogMark(t *testing.T) {
	a, lv := openLogs(t)
	step(a, key("m"))
	step(a, views.LogBatchMsg{Lines: []docker.LogLine{{Text: "after the mark"}}})

	text, n := lv.PlainText()
	lines := strings.Split(text, "\n")
	if n != 4 {
		t.Fatalf("%d lines after a mark and one more, want 4:\n%s", n, text)
	}
	if !strings.Contains(lines[1], "END") || !strings.HasPrefix(lines[2], "───") || lines[3] != "after the mark" {
		t.Errorf("the mark is not between what came before and after:\n%s", text)
	}
	if !strings.Contains(render(a), "───") {
		t.Error("the mark is not drawn")
	}
}

func TestLogMarkStampsTheTime(t *testing.T) {
	lv := views.NewLogsView(nil, "abc", "web")
	lv.Resize(40, 10)
	at := time.Date(2026, 10, 1, 9, 5, 7, 0, time.Local)
	lv.Mark(at)
	text, _ := lv.PlainText()
	if want := "─────────────── 09:05:07 ───────────────"; text != want { // the full 40 columns
		t.Errorf("mark = %q\nwant   %q", text, want)
	}
}
