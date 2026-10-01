package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

var sgrRe = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)

// unpaintedCells walks a styled frame tracking the SGR background the way a
// terminal would, and reports every printed cell that had none — the cells
// that show the terminal's own background through dockmaster's canvas.
func unpaintedCells(frame string) []string {
	var holes []string
	for row, line := range strings.Split(frame, "\n") {
		hasBg := false
		col := 0
		rest := line
		for rest != "" {
			if loc := sgrRe.FindStringSubmatchIndex(rest); loc != nil && loc[0] == 0 {
				params := rest[loc[2]:loc[3]]
				hasBg = applySGR(hasBg, params)
				rest = rest[loc[1]:]
				continue
			}
			if strings.HasPrefix(rest, "\x1b") {
				// Some other escape: skip to its final byte.
				i := 1
				for i < len(rest) && (rest[i] < '@' || rest[i] > '~' || i == 1 && rest[i] == '[') {
					i++
				}
				rest = rest[min(i+1, len(rest)):]
				continue
			}
			r := []rune(rest)[0]
			if !hasBg {
				holes = append(holes, fmt.Sprintf("row %d col %d %q", row, col, r))
			}
			col++
			rest = rest[len(string(r)):]
		}
	}
	return holes
}

func applySGR(hasBg bool, params string) bool {
	if params == "" {
		return false
	}
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		n, _ := strconv.Atoi(ps[i])
		switch {
		case n == 0:
			hasBg = false
		case n == 49:
			hasBg = false
		case n >= 40 && n <= 47, n >= 100 && n <= 107:
			hasBg = true
		case n == 48:
			hasBg = true
			if i+1 < len(ps) && ps[i+1] == "2" {
				i += 4
			} else {
				i += 2
			}
		case n == 38:
			if i+1 < len(ps) && ps[i+1] == "2" {
				i += 4
			} else {
				i += 2
			}
		}
	}
	return hasBg
}

// TestWholeFrameIsPainted pins the canvas: every cell of every frame sits
// on dockmaster's background, not the terminal's. Spans styled without a
// background end in a reset that drops the canvas for the rest of the line,
// which is how only the log pane came to be black.
func TestWholeFrameIsPainted(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	frames := map[string]string{"containers": renderStyled(a)}

	step(a, key("ctrl+d"))
	frames["confirm"] = renderStyled(a)
	step(a, key("n"))

	step(a, key("?"))
	frames["help"] = renderStyled(a)
	step(a, key("esc"))

	lv := views.NewLogsView(nil, "abc", "web")
	a.setView(style.ViewLogs, lv)
	a.pushView(style.ViewLogs)
	step(a, views.LogBatchMsg{Lines: systemdLines})
	frames["logs"] = renderStyled(a)

	c, _ := newColimaApp(t, Options{})
	frames["colima"] = renderStyled(c)
	step(c, key("n"))
	frames["colima form"] = renderStyled(c)

	n := NewApp(nil, Options{Version: "test", Logoless: true})
	n.splashActive, n.loading = false, false
	step(n, tea.WindowSizeMsg{Width: 100, Height: 30})
	frames["logoless"] = renderStyled(n)

	for name, f := range frames {
		if holes := unpaintedCells(f); len(holes) > 0 {
			t.Errorf("%s: %d cells on the terminal background, first: %s", name, len(holes), holes[0])
		}
	}
}
