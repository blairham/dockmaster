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
	"github.com/blairham/dockmaster/internal/registry"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// Column titles the registry views share with others.
const (
	colRepository = "REPOSITORY"
	colSize       = "SIZE"
	colAge        = "AGE"
	colState      = "STATE"
)

// NewRegistryClient is the v2 API client for a registry address; a var so
// tests can hand it a credential without the docker config.
var NewRegistryClient = registry.New

// registryBrowseTimeout bounds one listing: the catalog, or a repository's
// tags with a manifest and a config read for each.
const registryBrowseTimeout = 60 * time.Second

// registryContext is the deadline for a registry request. The registry is
// not the daemon, so --request-timeout does not apply.
func registryContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), registryBrowseTimeout)
}

// The browse views' actions (#120).
const (
	ActRegistryTags      = "registry_tags"
	ActRegistryDeleteTag = "confirm_registry_delete_tag"
)

// gcKey is P in a registry view: the registry container's garbage
// collection, or why there is none.
func gcKey(t RegistryTarget) (string, string) {
	if t.Container == "" {
		return ActNotLocal, t.Addr + " is not a registry container on this daemon — its garbage collection is its own"
	}
	return ActRegistryGC, t.Container
}

// RegistryCatalogMsg carries a registry's repositories.
type RegistryCatalogMsg struct {
	Err   error
	Addr  string
	Repos []string
}

// RegistryCatalogView lists a registry's repositories, from GET
// /v2/_catalog (#120). It loads on open and r, never on the poll.
type RegistryCatalogView struct {
	tableSort
	err     error
	target  RegistryTarget
	filter  string
	all     []string
	visible []string
	table   table.Model
	loading bool
}

// NewRegistryCatalogView opens the catalog of the registry t names.
func NewRegistryCatalogView(t RegistryTarget) *RegistryCatalogView {
	tbl := table.New(
		table.WithColumns(registryCatalogColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &RegistryCatalogView{
		tableSort: tableSort{layoutKey: "repositories"},
		target:    RegistryTarget{Addr: t.Addr, Container: t.Container},
		table:     tbl,
		loading:   true,
	}
}

func registryCatalogColumns() []table.Column {
	return []table.Column{{Title: colRepository, Width: 60}}
}

// Title is the registry's address.
func (v *RegistryCatalogView) Title() string { return v.target.Addr }

// Target is the registry on screen.
func (v *RegistryCatalogView) Target() RegistryTarget { return v.target }

// Init reads the catalog.
func (v *RegistryCatalogView) Init() tea.Cmd { return v.Refresh() }

// Refresh reads the catalog again.
func (v *RegistryCatalogView) Refresh() tea.Cmd {
	v.loading = true
	addr := v.target.Addr
	return func() tea.Msg {
		ctx, cancel := registryContext()
		defer cancel()
		repos, err := NewRegistryClient(addr).Catalog(ctx)
		return RegistryCatalogMsg{Addr: addr, Repos: repos, Err: err}
	}
}

// Update folds a catalog in, if it is this registry's.
func (v *RegistryCatalogView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(RegistryCatalogMsg); ok && m.Addr == v.target.Addr {
		v.loading = false
		v.err = m.Err
		v.all = m.Repos
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *RegistryCatalogView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *RegistryCatalogView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(registryCatalogColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *RegistryCatalogView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the catalog is outstanding.
func (v *RegistryCatalogView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *RegistryCatalogView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// Selected is the repository under the cursor.
func (v *RegistryCatalogView) Selected() (string, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return "", false
	}
	return v.visible[i], true
}

// HandleKey maps a keystroke to an app action: enter lists the selected
// repository's tags, P garbage-collects the registry (the app confirms).
func (v *RegistryCatalogView) HandleKey(key string) (string, string) {
	if key == "P" {
		return gcKey(v.target)
	}
	repo, ok := v.Selected()
	if !ok || key != KeyEnter {
		return "", ""
	}
	t := v.target
	t.Repo = repo
	return ActRegistryTags, t.Encode()
}

// CopyFields is the selected repository's reference, addr/repo.
func (v *RegistryCatalogView) CopyFields() (string, string, bool) {
	repo, ok := v.Selected()
	ref := v.target.Addr + "/" + repo
	return ref, ref, ok
}

// View renders the table.
func (v *RegistryCatalogView) View() string {
	if v.err != nil && !v.loading {
		return style.Error.Render("  " + docker.FormatUserError(v.err).Error())
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  no repositories in " + v.target.Addr + " — push one (p on an image)")
	}
	return fixSelectedRow(v.table.View())
}

func (v *RegistryCatalogView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, repo := range v.all {
		if !f.Empty() && !f.MatchesAny(repo) {
			continue
		}
		rows = append(rows, table.Row{truncate(repo, 120)})
		v.visible = append(v.visible, repo)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// Table is the view's table, for the keys every table shares.
func (v *RegistryCatalogView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *RegistryCatalogView) SortKey(key string) bool {
	return v.sortKey(key, &v.table, v.rebuildRows)
}

// Relayout lays the table out again after the column layouts changed.
func (v *RegistryCatalogView) Relayout() { v.relayout(&v.table, v.rebuildRows) }

// RegistryTagsMsg carries a repository's tags, each read from its manifest.
type RegistryTagsMsg struct {
	Err    error
	Addr   string
	Repo   string
	Images []registry.Image
}

// RegistryTagsView lists a repository's tags (#120): GET
// /v2/<repo>/tags/list, then each tag's manifest for its digest and size
// and its config for when it was built. A manifest and a config per tag,
// so it loads on open and r, never on the poll.
type RegistryTagsView struct {
	tableSort
	err     error
	target  RegistryTarget
	filter  string
	all     []registry.Image
	visible []registry.Image
	table   table.Model
	loading bool
}

// NewRegistryTagsView opens the tags of the repository t names.
func NewRegistryTagsView(t RegistryTarget) *RegistryTagsView {
	tbl := table.New(
		table.WithColumns(registryTagColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &RegistryTagsView{
		tableSort: tableSort{layoutKey: "tags"},
		target:    RegistryTarget{Addr: t.Addr, Container: t.Container, Repo: t.Repo},
		table:     tbl,
		loading:   true,
	}
}

func registryTagColumns() []table.Column {
	return []table.Column{
		{Title: "TAG", Width: 24},
		{Title: "DIGEST", Width: 19},
		{Title: colSize, Width: 10},
		{Title: "PLATFORMS", Width: 30},
		{Title: colAge, Width: 6},
	}
}

// Title is addr/repo.
func (v *RegistryTagsView) Title() string { return v.target.Addr + "/" + v.target.Repo }

// Init reads the tags.
func (v *RegistryTagsView) Init() tea.Cmd { return v.Refresh() }

// Refresh reads the tags and their manifests again.
func (v *RegistryTagsView) Refresh() tea.Cmd {
	v.loading = true
	addr, repo := v.target.Addr, v.target.Repo
	return func() tea.Msg {
		ctx, cancel := registryContext()
		defer cancel()
		c := NewRegistryClient(addr)
		tags, err := c.Tags(ctx, repo)
		if err != nil {
			return RegistryTagsMsg{Addr: addr, Repo: repo, Err: err}
		}
		return RegistryTagsMsg{Addr: addr, Repo: repo, Images: c.Images(ctx, repo, tags)}
	}
}

// Update folds the tags in, if they are this repository's.
func (v *RegistryTagsView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(RegistryTagsMsg); ok && m.Addr == v.target.Addr && m.Repo == v.target.Repo {
		v.loading = false
		v.err = m.Err
		v.all = m.Images
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *RegistryTagsView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *RegistryTagsView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(registryTagColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *RegistryTagsView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the listing is outstanding.
func (v *RegistryTagsView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *RegistryTagsView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// Selected is the tag under the cursor.
func (v *RegistryTagsView) Selected() (registry.Image, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return registry.Image{}, false
	}
	return v.visible[i], true
}

// HandleKey maps a keystroke to an app action: ctrl+d deletes the selected
// tag's manifest by digest, P garbage-collects the registry — both
// confirmed by the app.
func (v *RegistryTagsView) HandleKey(key string) (string, string) {
	if key == "P" {
		return gcKey(v.target)
	}
	img, ok := v.Selected()
	if !ok {
		return "", ""
	}
	t := v.target
	t.Tag, t.Digest = img.Tag, img.Digest
	if key == KeyCtrlD {
		if img.Digest == "" {
			return ActNotLocal, t.Ref() + " has no digest to delete by — its manifest did not read"
		}
		return ActRegistryDeleteTag, t.Encode()
	}
	return "", ""
}

// CopyFields is the selected tag's reference and its digest.
func (v *RegistryTagsView) CopyFields() (string, string, bool) {
	img, ok := v.Selected()
	t := v.target
	t.Tag = img.Tag
	return t.Ref(), img.Digest, ok
}

// View renders the table.
func (v *RegistryTagsView) View() string {
	if v.err != nil && !v.loading {
		return style.Error.Render("  " + docker.FormatUserError(v.err).Error())
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  no tags in " + v.Title())
	}
	return fixSelectedRow(v.table.View())
}

func (v *RegistryTagsView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, img := range v.all {
		platforms := strings.Join(img.Platforms, ",")
		if !f.Empty() && !f.MatchesAny(img.Tag, img.Digest, platforms) {
			continue
		}
		digest, size, age := shortRegistryDigest(img.Digest), "", ""
		if img.Size > 0 {
			size = docker.HumanSize(img.Size)
		}
		if !img.Created.IsZero() {
			age = since(img.Created)
		}
		if img.Err != nil {
			platforms = style.Error.Render(truncate(img.Err.Error(), 60))
		}
		rows = append(rows, table.Row{truncate(img.Tag, 60), digest, size, platforms, age})
		v.visible = append(v.visible, img)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// shortRegistryDigest is sha256:<12 hex>, as docker images --digests trims.
func shortRegistryDigest(d string) string {
	if len(d) > len("sha256:")+12 {
		return d[:len("sha256:")+12]
	}
	return d
}

// Table is the view's table, for the keys every table shares.
func (v *RegistryTagsView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *RegistryTagsView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// Relayout lays the table out again after the column layouts changed.
func (v *RegistryTagsView) Relayout() { v.relayout(&v.table, v.rebuildRows) }
