// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/tail"
)

// scrollTail routes a navigation key or wheel event into a streaming tail
// the way k9s does: any scroll pauses follow so new lines stop yanking the
// view back to the bottom, and G / end resumes it. Keys go through tuikit's
// HandleScrollKey — fed the arrow and page names TranslateNavKey has
// already mapped the vim keys onto — and anything it does not claim falls
// through to the viewport.
func scrollTail(t *tail.Model, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.KeyMsg:
		if t.HandleScrollKey(m.String()) {
			return nil
		}
	case tea.MouseWheelMsg:
		if m.Mouse().Button == tea.MouseWheelUp {
			t.SetFollow(false)
		}
	}
	return t.Update(msg)
}
