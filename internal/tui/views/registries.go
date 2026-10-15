// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// RegistriesRefreshMsg carries the docker config's registries.
type RegistriesRefreshMsg struct {
	Err        error
	Registries []docker.RegistryAuth
}

// ListRegistries reads the docker config and asks each credential helper;
// a var so tests can feed the view without a config or a helper.
var ListRegistries = docker.Registries

// registryListTimeout bounds one listing: a credential helper per row, and
// a keychain helper can wait on an unlock prompt.
const registryListTimeout = 30 * time.Second

// RegistriesView lists the registries the docker config knows (auths and
// credHelpers), where each one's credential lives and whether it resolves.
// Never a secret. Each row costs a credential-helper process, so it loads
// when opened and on r, never on the poll.
type RegistriesView struct {
	tableSort
	err     error
	filter  string
	all     []docker.RegistryAuth
	visible []docker.RegistryAuth
	table   table.Model
	loading bool
}

// NewRegistriesView builds the registries view.
func NewRegistriesView() *RegistriesView {
	t := table.New(
		table.WithColumns(registryColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &RegistriesView{tableSort: tableSort{layoutKey: "registries"}, table: t, loading: true}
}

func registryColumns() []table.Column {
	return []table.Column{
		{Title: "REGISTRY", Width: 36},
		{Title: "USER", Width: 20},
		{Title: "STORE", Width: 14},
		{Title: "LOGIN", Width: 40},
	}
}

// Init reads the config.
func (v *RegistriesView) Init() tea.Cmd { return v.Refresh() }

// Refresh re-reads the config and asks the helpers again.
func (v *RegistriesView) Refresh() tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), registryListTimeout)
		defer cancel()
		rs, err := ListRegistries(ctx)
		return RegistriesRefreshMsg{Registries: rs, Err: err}
	}
}

// Selected returns the registry under the cursor.
func (v *RegistriesView) Selected() (docker.RegistryAuth, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.RegistryAuth{}, false
	}
	return v.visible[i], true
}

// Update folds the listing in.
func (v *RegistriesView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(RegistriesRefreshMsg); ok {
		v.loading = false
		v.err = m.Err
		v.all = m.Registries
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *RegistriesView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *RegistriesView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(registryColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *RegistriesView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the listing is outstanding.
func (v *RegistriesView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *RegistriesView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey maps a keystroke to an app action: n logs in to a registry,
// enter logs in again to the selected one, ctrl+d logs out of it (the app
// confirms).
func (v *RegistriesView) HandleKey(key string) (string, string) {
	if key == "n" {
		return "login_form", ""
	}
	r, ok := v.Selected()
	if !ok {
		return "", ""
	}
	switch key {
	case KeyEnter:
		return "login_form", r.Server
	case KeyCtrlD:
		return "confirm_registry_logout", r.Server
	}
	return "", ""
}

// View renders the table.
func (v *RegistriesView) View() string {
	if v.err != nil && !v.loading {
		return style.Error.Render("  " + v.err.Error())
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  no registries in the docker config — n or :login <registry> logs in to one")
	}
	return fixSelectedRow(v.table.View())
}

func (v *RegistriesView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, r := range v.all {
		store := r.Helper
		if store == "" {
			store = "config.json"
		}
		if !f.Empty() && !f.MatchesAny(r.Server, r.Username, store) {
			continue
		}
		status := style.Muted.Render("not logged in")
		switch {
		case r.Err != "":
			status = style.Error.Render(truncate(r.Err, 40))
		case r.LoggedIn:
			status = style.Success.Render("logged in")
		}
		rows = append(rows, table.Row{
			truncate(r.Server, 36),
			truncate(r.Username, 20),
			truncate(store, 14),
			status,
		})
		v.visible = append(v.visible, r)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// Table is the view's table, for the keys every table shares.
func (v *RegistriesView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *RegistriesView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// Relayout lays the table out again after the column layouts changed.
func (v *RegistriesView) Relayout() { v.relayout(&v.table, v.rebuildRows) }
