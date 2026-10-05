// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// contentBorderRGB is the color the content box's border is drawn in: its
// top-left corner, on the line carrying the view's title, and its left
// side beside a row. They must agree, or one of them was not repainted.
func contentBorderRGB(t *testing.T, a *App) string {
	t.Helper()
	find := func(mark, char string) string {
		re := regexp.MustCompile(`\x1b\[38;2;(\d+);(\d+);(\d+)(?:;[\d;]*)?m` + char)
		for _, l := range strings.Split(renderStyled(a), "\n") {
			if strings.Contains(l, mark) {
				if m := re.FindStringSubmatch(l); m != nil {
					return m[1] + "," + m[2] + "," + m[3]
				}
			}
		}
		t.Fatalf("no %s on the line with %q", char, mark)
		return ""
	}
	corner, side := find("containers", "╭"), find("nginx:1.27", "│")
	if corner != side {
		t.Errorf("corner %s, side %s: the box is two colors", corner, side)
	}
	return corner
}

func rgbString(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("%d,%d,%d", r>>8, g>>8, b>>8)
}

// TestContentBorderFollowsFocus: the table's border is k9s's focusColor
// while it has the keyboard and its fgColor while the command bar, the
// filter bar or a confirm does, as tview draws k9s's table.
func TestContentBorderFollowsFocus(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	focus, plain := rgbString(a.chrome.Theme.FocusBorder()), rgbString(a.chrome.Theme.Border)
	if focus == plain {
		t.Fatalf("the theme's focus and plain borders are both %s; the test cannot tell them apart", focus)
	}
	if got := contentBorderRGB(t, a); got != focus {
		t.Errorf("table focused: border %s, want focus %s", got, focus)
	}
	for _, k := range []string{":", "/"} {
		b := newTestApp(t)
		loadContainers(b)
		step(b, tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		if got := contentBorderRGB(t, b); got != plain {
			t.Errorf("%s bar open: border %s, want plain %s", k, got, plain)
		}
		step(b, key("esc"))
		if got := contentBorderRGB(t, b); got != focus {
			t.Errorf("%s bar closed: border %s, want focus %s", k, got, focus)
		}
	}
	c := newTestApp(t)
	loadContainers(c)
	step(c, key("ctrl+d"))
	if !c.confirm.Active() {
		t.Fatal("ctrl+d did not open a confirm")
	}
	if got := contentBorderRGB(t, c); got != plain {
		t.Errorf("confirm open: border %s, want plain %s", got, plain)
	}
}
