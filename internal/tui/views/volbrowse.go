// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// Volume browsing goes through these, so tests can stand in for the
// helper container.
var (
	BrowseVolume = func(ctx context.Context, c *docker.Client, volume, dir string) ([]docker.VolumeEntry, error) {
		return c.BrowseVolume(ctx, volume, dir)
	}
	ReadVolumeFile = func(ctx context.Context, c *docker.Client, volume, file string, limit int) ([]byte, bool, error) {
		return c.ReadVolumeFile(ctx, volume, file, limit)
	}
)

// dirStyle draws directory names, as ls colors them; derived in applyTheme.
var dirStyle lipgloss.Style

// volumeFileLimit caps how much of a file the viewer reads.
const volumeFileLimit = 1 << 20

// volumeBrowseTimeout bounds one listing: a helper container is created,
// run and removed each time.
const volumeBrowseTimeout = 30 * time.Second

// VolumeBrowseMsg carries one directory listing.
type VolumeBrowseMsg struct {
	Err     error
	Volume  string
	Dir     string
	Entries []docker.VolumeEntry
}

// VolumeBrowseView lists a volume's files a directory at a time. It moves
// through directories in place — enter goes in, esc comes back out — so
// the drill stack holds one browse view however deep the walk.
type VolumeBrowseView struct {
	tableSort
	client *docker.Client
	err    error

	volume string
	dir    string
	filter string

	all     []docker.VolumeEntry
	visible []docker.VolumeEntry
	table   table.Model

	loading bool
}

// NewVolumeBrowseView opens volume at its root.
func NewVolumeBrowseView(client *docker.Client, volume string) *VolumeBrowseView {
	t := table.New(
		table.WithColumns(volumeBrowseColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &VolumeBrowseView{
		tableSort: tableSort{layoutKey: "files"},
		client:    client,
		table:     t,
		volume:    volume,
		dir:       "/",
		loading:   true,
	}
}

func volumeBrowseColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 40},
		{Title: "SIZE", Width: 10},
		{Title: "MODE", Width: 11},
		{Title: "AGE", Width: 6},
	}
}

// Title is volume:/dir, for the border title.
func (v *VolumeBrowseView) Title() string { return v.volume + ":" + v.dir }

// Dir is the directory on screen.
func (v *VolumeBrowseView) Dir() string { return v.dir }

// Init lists the root.
func (v *VolumeBrowseView) Init() tea.Cmd { return v.Refresh() }

// Update folds a listing in, if it is for the directory on screen.
func (v *VolumeBrowseView) Update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(VolumeBrowseMsg)
	if !ok || m.Volume != v.volume || m.Dir != v.dir {
		return nil
	}
	v.loading = false
	if m.Err != nil {
		v.err = m.Err
		v.all = nil
		v.rebuildRows()
		return nil
	}
	v.err = nil
	v.all = m.Entries
	v.rebuildRows()
	return nil
}

// UpdateTable forwards navigation keys.
func (v *VolumeBrowseView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *VolumeBrowseView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(volumeBrowseColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *VolumeBrowseView) Count() int { return len(v.table.Rows()) }

// Loading reports whether a listing is outstanding.
func (v *VolumeBrowseView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *VolumeBrowseView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// Selected returns the entry under the cursor.
func (v *VolumeBrowseView) Selected() (docker.VolumeEntry, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.VolumeEntry{}, false
	}
	return v.visible[i], true
}

// HandleKey opens, on enter, a directory in place or a file in the viewer.
func (v *VolumeBrowseView) HandleKey(key string) (string, string) {
	e, ok := v.Selected()
	if !ok || key != KeyEnter {
		return "", ""
	}
	p := path.Join(v.dir, e.Name)
	if e.Dir() {
		return "volume_dir", p
	}
	return "volume_file", v.volume + "\x00" + p
}

// Open moves into dir and lists it.
func (v *VolumeBrowseView) Open(dir string) tea.Cmd {
	v.dir = docker.VolumePath(dir)
	v.loading = true
	v.all = nil
	v.rebuildRows()
	v.table.SetCursor(0)
	return v.Refresh()
}

// Back moves up one directory, reporting false at the volume's root,
// where esc leaves the view instead.
func (v *VolumeBrowseView) Back() (tea.Cmd, bool) {
	if v.dir == "/" {
		return nil, false
	}
	return v.Open(path.Dir(v.dir)), true
}

// View renders the listing.
func (v *VolumeBrowseView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  empty directory")
	}
	return fixSelectedRow(v.table.View())
}

// Refresh lists the directory on screen.
func (v *VolumeBrowseView) Refresh() tea.Cmd {
	client, volume, dir := v.client, v.volume, v.dir
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(volumeBrowseTimeout)
		defer cancel()
		entries, err := BrowseVolume(ctx, client, volume, dir)
		return VolumeBrowseMsg{Volume: volume, Dir: dir, Entries: entries, Err: err}
	}
}

func (v *VolumeBrowseView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, e := range v.all {
		if !f.Empty() && !f.MatchesAny(e.Name) {
			continue
		}
		name, size := e.Name, docker.HumanSize(e.Size)
		switch e.Type {
		case "dir":
			name = dirStyle.Render(e.Name + "/")
			size = style.Muted.Render("—")
		case "link":
			name = style.Muted.Render(e.Name + "@")
		}
		rows = append(rows, table.Row{truncate(name, 40), size, e.Mode, since(e.Modified)})
		v.visible = append(v.visible, e)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// Table is the view's table, for the keys every table shares.
func (v *VolumeBrowseView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *VolumeBrowseView) SortKey(key string) bool {
	return v.sortKey(key, &v.table, v.rebuildRows)
}

// NewVolumeFileView shows the start of a file in a volume — the first
// megabyte, as text, or a note that it is binary.
func NewVolumeFileView(client *docker.Client, volume, file string) *InspectView {
	v := NewInspectFetchView(volume+":"+file, func(ctx context.Context) ([]byte, error) {
		b, more, err := ReadVolumeFile(ctx, client, volume, file, volumeFileLimit)
		if err != nil {
			return nil, err
		}
		return []byte(volumeFileText(b, more)), nil
	})
	v.plain = true
	return v
}

// volumeFileText is a file's bytes as the viewer shows them: text with its
// control characters made safe, or a note for binary content.
func volumeFileText(b []byte, more bool) string {
	if bytes.IndexByte(b, 0) >= 0 {
		return style.Muted.Render(fmt.Sprintf("  binary file — not shown (%s read)", docker.HumanSize(int64(len(b)))))
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i, l := range lines {
		lines[i] = sanitizeLogText(l)
	}
	text := strings.Join(lines, "\n")
	if more {
		text += "\n" + style.Muted.Render(fmt.Sprintf("— first %s shown —", docker.HumanSize(int64(len(b)))))
	}
	return text
}

// Relayout lays the table out again after the column layouts changed.
func (v *VolumeBrowseView) Relayout() { v.relayout(&v.table, v.rebuildRows) }
