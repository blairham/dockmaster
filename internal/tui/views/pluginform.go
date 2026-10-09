// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"encoding/json"
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"
)

// Plugin input types, as k9s names them.
const (
	InputString   = "string"
	InputNumber   = "number"
	InputBool     = "bool"
	InputDropdown = "dropdown"
)

// PluginInput is one field of a plugin's form, already validated
// (tui.Plugins): Type is one of the Input constants, Label is never empty,
// a Default is valid for its type, and a dropdown has Options.
type PluginInput struct {
	Name     string
	Label    string
	Type     string
	Default  string
	Options  []string
	Required bool
}

// PluginInputs is a submitted plugin form: the plugin, and each input's
// value by its name.
type PluginInputs struct {
	Values map[string]string `json:"values"`
	Plugin string            `json:"plugin"`
}

// pluginInputWidth is a text input's width in the plugin form.
const pluginInputWidth = 32

// PluginFormView asks for a plugin's inputs before it runs — k9s's plugin
// inputs dialog. It runs nothing itself: enter returns the values, and the
// app runs the plugin with them; esc abandons it.
type PluginFormView struct {
	plugin string
	desc   string
	inputs []PluginInput
	form
}

// NewPluginForm builds the form for plugin name, described as desc, with
// each input's default filled in. A bool is a toggle, false unless its
// default says otherwise; a dropdown with no default starts on no choice.
func NewPluginForm(name, desc string, inputs []PluginInput) *PluginFormView {
	v := &PluginFormView{plugin: name, desc: desc, inputs: inputs}
	v.valueWidth = pluginInputWidth + 4
	for _, in := range inputs {
		if w := len([]rune(in.Label)) + 2; w > v.labelWidth {
			v.labelWidth = w
		}
		v.fields = append(v.fields, pluginField(in))
	}
	v.labelWidth = max(v.labelWidth, 10)
	v.focusField(0)
	return v
}

// pluginField is the form field for one input.
func pluginField(in PluginInput) formField {
	f := formField{key: in.Name, label: in.Label}
	switch in.Type {
	case InputBool:
		f.choices = []string{"false", "true"}
		if in.Default == f.choices[1] {
			f.choice = 1
		}
		f.hint = "space or y/n"
	case InputDropdown:
		f.choices, f.choice = dropdownChoices(in)
		f.hint = "←/→ to choose"
	default:
		f.input = newFormInput(in.Default, 1024)
		f.input.SetWidth(pluginInputWidth)
		if in.Type == InputNumber {
			f.hint = "a number"
		}
	}
	if in.Required {
		f.hint = joinHint("required", f.hint)
	}
	return f
}

// dropdownChoices are a dropdown's choices and the one its default picks; a
// dropdown with no default starts on an empty choice put first.
func dropdownChoices(in PluginInput) ([]string, int) {
	choice := -1
	for i, o := range in.Options {
		if o == in.Default {
			choice = i
		}
	}
	if choice < 0 {
		return append([]string{""}, in.Options...), 0
	}
	return in.Options, choice
}

func joinHint(a, b string) string {
	if b == "" {
		return a
	}
	return a + " · " + b
}

// CapturesInput reports that the form takes every typed key.
func (v *PluginFormView) CapturesInput() bool { return true }

// Title names the plugin, for the border title.
func (v *PluginFormView) Title() string { return "plugin " + v.desc }

// Plugin is the name of the plugin the form is for.
func (v *PluginFormView) Plugin() string { return v.plugin }

// Init has nothing to start.
func (v *PluginFormView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *PluginFormView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *PluginFormView) Resize(int, int) {}

// Count is the number of fields.
func (v *PluginFormView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *PluginFormView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *PluginFormView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *PluginFormView) Refresh() tea.Cmd { return nil }

// View renders the form.
func (v *PluginFormView) View() string {
	return v.render("enter runs it · tab next field · esc cancel")
}

// Values reads every field, by input name.
func (v *PluginFormView) Values() map[string]string {
	out := make(map[string]string, len(v.fields))
	for i := range v.fields {
		out[v.fields[i].key] = v.fields[i].value()
	}
	return out
}

// HandleKey sets a focused bool with y/n, and submits on enter once every
// required input has a value and every number parses — as a float, k9s's
// own test of a number.
func (v *PluginFormView) HandleKey(key string) (string, string) {
	f := &v.fields[v.focus]
	if v.inputs[v.focus].Type == InputBool && (key == "y" || key == "n") {
		f.choice = 0
		if key == "y" {
			f.choice = 1
		}
		v.err = ""
		return "", ""
	}
	if key != KeyEnter {
		return "", ""
	}
	values := v.Values()
	for i, in := range v.inputs {
		val := values[in.Name]
		switch {
		case in.Required && val == "":
			v.err = in.Label + " is required"
		case in.Type == InputNumber && val != "" && !isNumber(val):
			v.err = fmt.Sprintf("%s: %q is not a number", in.Label, val)
		default:
			continue
		}
		v.focusField(i)
		return "", ""
	}
	b, err := json.Marshal(PluginInputs{Plugin: v.plugin, Values: values})
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	return "plugin_inputs", string(b)
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// DecodePluginInputs reads a submitted plugin form back.
func DecodePluginInputs(s string) (PluginInputs, error) {
	var p PluginInputs
	err := json.Unmarshal([]byte(s), &p)
	return p, err
}
