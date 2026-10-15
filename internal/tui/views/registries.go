// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// RegistriesRefreshMsg carries the docker config's registries and the
// registry containers on the daemon.
type RegistriesRefreshMsg struct {
	Err        error
	LocalErr   error
	Registries []docker.RegistryAuth
	Local      []docker.LocalRegistry
}

// ListRegistries reads the docker config and asks each credential helper;
// a var so tests can feed the view without a config or a helper.
var ListRegistries = docker.Registries

// ListLocalRegistries lists the registry containers on the daemon (#120);
// a var so tests can feed the view without one. Nothing without a client.
var ListLocalRegistries = func(ctx context.Context, c *docker.Client) ([]docker.LocalRegistry, error) {
	if c == nil {
		return nil, nil
	}
	return c.LocalRegistries(ctx)
}

// registryLocalTimeout bounds the daemon listing: a docker ps -a and an
// inspect per registry container.
const registryLocalTimeout = 30 * time.Second

// RegistryRow is one row of the registries view: a registry the docker
// config knows, a registry container on the daemon, or both — a local
// registry the user has logged in to is one row.
type RegistryRow struct {
	// Local is the container, nil for a registry only the config names.
	Local *docker.LocalRegistry
	Auth  docker.RegistryAuth
}

// Address is the row's registry address: the container's
// localhost:<port>, else the config's key.
func (r RegistryRow) Address() string {
	if r.Local != nil && r.Local.Address() != "" {
		return r.Local.Address()
	}
	return r.Auth.Server
}

// Container is the registry container's name, "" for a config-only row.
func (r RegistryRow) Container() string {
	if r.Local == nil {
		return ""
	}
	return r.Local.Name
}

// registryRows folds the config's registries into the daemon's: a local
// registry comes first and takes the config entry for its address, so it
// is one row.
func registryRows(local []docker.LocalRegistry, auths []docker.RegistryAuth) []RegistryRow {
	byHost := map[string]int{}
	out := make([]RegistryRow, 0, len(local)+len(auths))
	for i := range local {
		l := local[i]
		out = append(out, RegistryRow{Local: &l})
		if a := l.Address(); a != "" {
			byHost[docker.RegistryHost(a)] = len(out) - 1
		}
	}
	for _, a := range auths {
		if i, ok := byHost[docker.RegistryHost(a.Server)]; ok {
			out[i].Auth = a
			continue
		}
		out = append(out, RegistryRow{Auth: a})
	}
	return out
}

// registryListTimeout bounds one listing: a credential helper per row, and
// a keychain helper can wait on an unlock prompt.
const registryListTimeout = 30 * time.Second

// RegistriesView lists the registry containers on the daemon (#120) and
// the registries the docker config knows (auths and credHelpers), where
// each one's credential lives and whether it resolves. Never a secret.
// Each config row costs a credential-helper process, so it loads when
// opened and on r, never on the poll.
type RegistriesView struct {
	tableSort
	err      error
	localErr error
	client   *docker.Client
	filter   string
	all      []RegistryRow
	visible  []RegistryRow
	table    table.Model
	loading  bool
}

// NewRegistriesView builds the registries view; client finds the registry
// containers, and may be nil.
func NewRegistriesView(client *docker.Client) *RegistriesView {
	t := table.New(
		table.WithColumns(registryColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &RegistriesView{tableSort: tableSort{layoutKey: "registries"}, client: client, table: t, loading: true}
}

func registryColumns() []table.Column {
	return []table.Column{
		{Title: "REGISTRY", Width: 30},
		{Title: "CONTAINER", Width: 22},
		{Title: colState, Width: 8},
		{Title: "USER", Width: 16},
		{Title: "STORE", Width: 12},
		{Title: "LOGIN", Width: 30},
	}
}

// Init reads the config.
func (v *RegistriesView) Init() tea.Cmd { return v.Refresh() }

// Refresh re-reads the config and asks the helpers again.
func (v *RegistriesView) Refresh() tea.Cmd {
	v.loading = true
	client := v.client
	return func() tea.Msg {
		dctx, dcancel := client.RequestContext(registryLocalTimeout)
		local, lerr := ListLocalRegistries(dctx, client)
		dcancel()
		ctx, cancel := context.WithTimeout(context.Background(), registryListTimeout)
		defer cancel()
		rs, err := ListRegistries(ctx)
		return RegistriesRefreshMsg{Registries: rs, Err: err, Local: local, LocalErr: lerr}
	}
}

// Selected returns the registry under the cursor.
func (v *RegistriesView) Selected() (RegistryRow, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return RegistryRow{}, false
	}
	return v.visible[i], true
}

// Local is the dockmaster registry, else the first registry container,
// from the last listing — what :catalog opens with no address.
func (v *RegistriesView) Local() (docker.LocalRegistry, bool) {
	for _, r := range v.all {
		if r.Local != nil && r.Local.Address() != "" {
			return *r.Local, true
		}
	}
	return docker.LocalRegistry{}, false
}

// Update folds the listing in.
func (v *RegistriesView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(RegistriesRefreshMsg); ok {
		v.loading = false
		v.err, v.localErr = m.Err, m.LocalErr
		v.all = registryRows(m.Local, m.Registries)
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

// The registries view's actions (#120).
const (
	ActRegistryUp      = "registry_up"
	ActRegistryStop    = "registry_stop"
	ActRegistryRemove  = "confirm_registry_remove"
	ActRegistryGC      = "confirm_registry_gc"
	ActRegistryCatalog = "registry_catalog"
	ActNotLocal        = "registry_not_local"
)

// RegistryTarget is what a registry action acts on, carried in its param:
// a registry's address, its container on the daemon ("" for a remote
// registry), and a repository, tag and digest in it as far as they apply.
type RegistryTarget struct {
	Addr      string
	Container string
	Repo      string
	Tag       string
	Digest    string
}

// Encode is the target as one action param.
func (t RegistryTarget) Encode() string {
	return strings.Join([]string{t.Addr, t.Container, t.Repo, t.Tag, t.Digest}, "\x00")
}

// Ref is the image reference the target names: addr/repo:tag.
func (t RegistryTarget) Ref() string {
	ref := t.Addr + "/" + t.Repo
	if t.Tag != "" {
		ref += ":" + t.Tag
	}
	return ref
}

// DecodeRegistryTarget reads Encode's param.
func DecodeRegistryTarget(param string) RegistryTarget {
	f := strings.SplitN(param, "\x00", 5)
	for len(f) < 5 {
		f = append(f, "")
	}
	return RegistryTarget{Addr: f[0], Container: f[1], Repo: f[2], Tag: f[3], Digest: f[4]}
}

// HandleKey maps a keystroke to an app action: enter browses the selected
// registry's repositories, n logs in to a registry, l logs in again to the
// selected one; u starts the local registry (the selected container, else
// dockmaster's own), and on a registry container x stops it, P
// garbage-collects it and ctrl+d removes it; on any other row ctrl+d logs
// out of it. The app confirms what is destructive.
func (v *RegistriesView) HandleKey(key string) (string, string) {
	switch key {
	case "n":
		return "login_form", ""
	case "u":
		if r, ok := v.Selected(); ok && r.Local != nil {
			return ActRegistryUp, r.Local.Name
		}
		return ActRegistryUp, ""
	}
	r, ok := v.Selected()
	if !ok {
		return "", ""
	}
	switch key {
	case KeyEnter:
		if r.Address() == "" {
			return ActNotLocal, r.Container() + " publishes no port, so it cannot be browsed from here"
		}
		return ActRegistryCatalog, RegistryTarget{Addr: r.Address(), Container: r.Container()}.Encode()
	case "l":
		return "login_form", r.Address()
	case "x", "P":
		if r.Local == nil {
			return ActNotLocal, r.Address() + " is not a registry container on this daemon"
		}
		if key == "x" {
			return ActRegistryStop, r.Local.Name
		}
		return ActRegistryGC, r.Local.Name
	case KeyCtrlD:
		if r.Local != nil {
			return ActRegistryRemove, r.Local.Name + "\x00" + r.Local.Kind
		}
		return "confirm_registry_logout", r.Auth.Server
	}
	return "", ""
}

// Status says when the registry containers could not be listed — the
// config's registries still are.
func (v *RegistriesView) Status() string {
	if v.localErr == nil {
		return ""
	}
	return "containers: " + truncate(v.localErr.Error(), 60)
}

// View renders the table.
func (v *RegistriesView) View() string {
	if v.err != nil && !v.loading {
		return style.Error.Render("  " + v.err.Error())
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  no registries — u starts a local one, n or :login <registry> logs in to one")
	}
	return fixSelectedRow(v.table.View())
}

func (v *RegistriesView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, r := range v.all {
		a := r.Auth
		store, state := "", ""
		if a.Server != "" {
			store = a.Helper
			if store == "" {
				store = "config.json"
			}
		}
		if r.Local != nil {
			state = style.Muted.Render("stopped")
			if r.Local.Running {
				state = style.Success.Render("running")
			}
		}
		if !f.Empty() && !f.MatchesAny(r.Address(), r.Container(), a.Username, store) {
			continue
		}
		status := style.Muted.Render("not logged in")
		switch {
		case a.Err != "":
			status = style.Error.Render(truncate(a.Err, 40))
		case a.LoggedIn:
			status = style.Success.Render("logged in")
		case r.Local != nil && a.Server == "":
			status = style.Muted.Render(r.Local.Kind + " registry")
		}
		addr := r.Address()
		if addr == "" {
			addr = style.Muted.Render("(no port)")
		}
		rows = append(rows, table.Row{
			truncate(addr, 30),
			truncate(r.Container(), 22),
			state,
			truncate(a.Username, 16),
			truncate(store, 12),
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
