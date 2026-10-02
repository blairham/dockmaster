// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"cmp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blairham/dockmaster/internal/tui/style"
)

// form is the field machinery the drill-in forms share — the runtime form
// and the run form: focus, cycling a choice, typing into an input, and
// drawing the rows. A form view embeds it and adds its own fields,
// validation and footer.
type form struct {
	onChoice   func(key string)
	err        string
	fields     []formField
	focus      int
	valueWidth int
}

// cycleChoice steps the focused choice field with ←/→ (h/l, space); a
// locked field and any other key leave it alone.
func (v *form) cycleChoice(key string) {
	f := &v.fields[v.focus]
	if f.locked {
		return
	}
	switch key {
	case "left", "h":
		f.choice = (f.choice + len(f.choices) - 1) % len(f.choices)
	case "right", "l", "space", " ":
		f.choice = (f.choice + 1) % len(f.choices)
	default:
		return
	}
	if v.onChoice != nil {
		v.onChoice(f.key)
	}
	v.err = ""
}

// UpdateTable takes every key the app does not: field navigation, cycling
// a choice, and typing into the focused field.
func (v *form) UpdateTable(msg tea.Msg) tea.Cmd {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "tab", "down":
			v.moveFocus(1)
			return nil
		case "shift+tab", "up":
			v.moveFocus(-1)
			return nil
		case KeyEnter:
			return nil
		}
		if v.fields[v.focus].choices != nil {
			v.cycleChoice(km.String())
			return nil
		}
	}
	f := &v.fields[v.focus]
	if f.choices != nil || f.locked {
		return nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	v.err = ""
	return cmd
}

func (v *form) moveFocus(delta int) {
	i := v.focus
	for range v.fields {
		i = (i + delta + len(v.fields)) % len(v.fields)
		if !v.fields[i].locked {
			v.focusField(i)
			return
		}
	}
}

func (v *form) focusField(i int) {
	if i < 0 {
		i = 0
	}
	for j := range v.fields {
		v.fields[j].input.Blur()
	}
	v.focus = i
	if v.fields[i].choices == nil {
		v.fields[i].input.Focus()
	}
}

func (v *form) indexOf(key string) int {
	for i := range v.fields {
		if v.fields[i].key == key {
			return i
		}
	}
	return -1
}

func (v *form) field(key string) *formField {
	if i := v.indexOf(key); i >= 0 {
		return &v.fields[i]
	}
	return nil
}

// render draws the fields, a footer line, and the error if any.
func (v *form) render(footer string) string {
	label := lipgloss.NewStyle().Foreground(style.ColorDockerBlue).Width(10)
	focused := lipgloss.NewStyle().Foreground(style.ColorCyan).Bold(true).Width(10)
	box := lipgloss.NewStyle().Foreground(style.ColorWhite).Bold(true)

	var b strings.Builder
	b.WriteString("\n")
	for i := range v.fields {
		f := &v.fields[i]
		marker, l := "   ", label
		if i == v.focus {
			marker, l = style.Success.Render(" ▸ "), focused
		}

		var val string
		switch {
		case f.choices != nil && f.locked:
			val = style.Muted.Render(f.value())
		case f.choices != nil:
			val = box.Render("‹ " + f.value() + " ›")
		case f.locked:
			val = style.Muted.Render(f.value())
		default:
			val = "[" + f.input.View() + "]"
		}
		if f.unit != "" {
			val += " " + f.unit
		}

		line := marker + l.Render(f.label) + lipgloss.NewStyle().Width(cmp.Or(v.valueWidth, 22)).Render(val)
		if f.hint != "" {
			line += style.Muted.Render(f.hint)
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n   " + style.Muted.Render(footer) + "\n")
	if v.err != "" {
		b.WriteString("\n   " + style.Error.Render(v.err) + "\n")
	}
	return b.String()
}
