// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/tui/style"
)

// Command kinds, as the aliases view's KIND column shows them.
const (
	CommandKindView    = "view"
	CommandKindCommand = "command"
	CommandKindAlias   = "alias"
)

// Command is one row of the aliases view: a `:` command, the other
// spellings the palette takes for it, and what it does. A user's alias is
// a row of its own, its Desc the command line it stands for.
type Command struct {
	Name string
	Kind string
	Desc string
	Also []string
	// NeedsArg is a command that does nothing without an argument (:pull):
	// enter opens the palette on it rather than running it bare.
	NeedsArg bool
}

// AliasesView is k9s's aliases view (ctrl-a, :aliases): every `:` command
// dockmaster has, with its spellings, and the user's own aliases, as a
// table. enter runs the selected one as if typed at the palette.
type AliasesView struct {
	tableSort
	filter  string
	all     []Command
	visible []Command
	table   table.Model
}

// NewAliasesView lists commands. The list is built by the app, which owns
// the command tables and the aliases file; nothing here is fetched.
func NewAliasesView(commands []Command) *AliasesView {
	t := table.New(
		table.WithColumns(aliasColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	v := &AliasesView{tableSort: tableSort{layoutKey: "aliases"}, table: t, all: commands}
	v.rebuildRows()
	return v
}

func aliasColumns() []table.Column {
	return []table.Column{
		{Title: "COMMAND", Width: 18},
		{Title: "ALSO", Width: 40},
		{Title: "KIND", Width: 8},
		{Title: "DESCRIPTION", Width: 50},
	}
}

// Init has nothing to load.
func (v *AliasesView) Init() tea.Cmd { return nil }

// Selected returns the command under the cursor.
func (v *AliasesView) Selected() (Command, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return Command{}, false
	}
	return v.visible[i], true
}

// Update has no messages of its own.
func (v *AliasesView) Update(tea.Msg) tea.Cmd { return nil }

// UpdateTable forwards navigation keys.
func (v *AliasesView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *AliasesView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(aliasColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *AliasesView) Count() int { return len(v.table.Rows()) }

// Loading is always false: the list is in hand when the view is built.
func (v *AliasesView) Loading() bool { return false }

// SetFilter applies a row filter over every column.
func (v *AliasesView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// Refresh has nothing to re-read: the commands are the binary's and the
// aliases file is read at startup and on reload.
func (v *AliasesView) Refresh() tea.Cmd { return nil }

// HandleKey runs the selected command on enter, or opens the palette on one
// that needs an argument.
func (v *AliasesView) HandleKey(key string) (string, string) {
	c, ok := v.Selected()
	if !ok || key != KeyEnter {
		return "", ""
	}
	if c.NeedsArg {
		return "palette", c.Name + " "
	}
	return "run_command", c.Name
}

// View renders the table.
func (v *AliasesView) View() string {
	if len(v.visible) == 0 {
		return style.Muted.Render("  no command matches the filter")
	}
	return fixSelectedRow(v.table.View())
}

func (v *AliasesView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, c := range v.all {
		also := strings.Join(c.Also, ", ")
		if !f.Empty() && !f.MatchesAny(c.Name, also, c.Kind, c.Desc) {
			continue
		}
		kind := c.Kind
		if kind == CommandKindAlias {
			kind = style.Success.Render(kind)
		}
		rows = append(rows, table.Row{
			truncate(c.Name, 18),
			truncate(also, 40),
			kind,
			truncate(c.Desc, 50),
		})
		v.visible = append(v.visible, c)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// Table is the view's table, for the keys every table shares.
func (v *AliasesView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *AliasesView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// Relayout lays the table out again after the column layouts changed.
func (v *AliasesView) Relayout() { v.relayout(&v.table, v.rebuildRows) }
