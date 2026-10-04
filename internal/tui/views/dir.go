// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// DirRefreshMsg carries a directory listing.
type DirRefreshMsg struct {
	Err     error
	Dir     string
	Entries []DirEntry
}

// DirEntry is one entry of a local directory.
type DirEntry struct {
	Modified time.Time
	Name     string
	Size     int64
	Dir      bool
}

// composeFile matches the names `docker compose` reads by default, and the
// override and per-environment files people keep beside them.
var composeFile = regexp.MustCompile(`^(docker-)?compose(\.[^/]+)?\.ya?ml$`)

// IsComposeFile reports whether name is a compose file by its name.
func IsComposeFile(name string) bool { return composeFile.MatchString(name) }

// DirView browses the local filesystem for compose files (#15), as k9s's
// :dir browses manifests: u brings a project up from its file before any of
// its containers exist, which the projects view — built from containers —
// cannot. It walks directories in place, and esc goes up until the root.
type DirView struct {
	tableSort
	err     error
	dir     string
	filter  string
	all     []DirEntry
	visible []DirEntry
	table   table.Model
	loading bool
}

// NewDirView browses dir.
func NewDirView(dir string) *DirView {
	t := table.New(
		table.WithColumns(dirColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &DirView{tableSort: tableSort{layoutKey: "dir"}, dir: filepath.Clean(dir), table: t, loading: true}
}

func dirColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 48},
		{Title: "KIND", Width: 8},
		{Title: "SIZE", Width: 9},
		{Title: "AGE", Width: 6},
	}
}

// Title is the directory on screen.
func (v *DirView) Title() string { return v.dir }

// Dir is the directory on screen.
func (v *DirView) Dir() string { return v.dir }

// Init lists the directory.
func (v *DirView) Init() tea.Cmd { return v.Refresh() }

// Refresh re-reads the directory. It is a local read, cheap enough for r.
func (v *DirView) Refresh() tea.Cmd {
	dir := v.dir
	return func() tea.Msg {
		es, err := ReadDir(dir)
		return DirRefreshMsg{Dir: dir, Entries: es, Err: err}
	}
}

// ReadDir lists dir: directories first, then compose files, then the rest,
// each by name. Hidden entries are left out, as a shell's ls leaves them.
func ReadDir(dir string) ([]DirEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]DirEntry, 0, len(des))
	for _, d := range des {
		if strings.HasPrefix(d.Name(), ".") {
			continue
		}
		e := DirEntry{Name: d.Name(), Dir: d.IsDir()}
		if info, err := d.Info(); err == nil {
			e.Size, e.Modified = info.Size(), info.ModTime()
			// A symlink to a directory walks like one.
			if !e.Dir && d.Type()&os.ModeSymlink != 0 {
				if st, err := os.Stat(filepath.Join(dir, d.Name())); err == nil && st.IsDir() {
					e.Dir = true
				}
			}
		}
		out = append(out, e)
	}
	rank := func(e DirEntry) int {
		switch {
		case e.Dir:
			return 0
		case IsComposeFile(e.Name):
			return 1
		default:
			return 2
		}
	}
	slices.SortStableFunc(out, func(a, b DirEntry) int {
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

// Update folds a listing in; one for a directory since left is dropped.
func (v *DirView) Update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(DirRefreshMsg)
	if !ok || m.Dir != v.dir {
		return nil
	}
	v.loading, v.err, v.all = false, m.Err, m.Entries
	v.rebuildRows()
	return nil
}

func (v *DirView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, e := range v.all {
		if !f.Empty() && !f.MatchesAny(e.Name) {
			continue
		}
		name, kind, size := e.Name, "file", docker.HumanSize(e.Size)
		switch {
		case e.Dir:
			name, kind, size = style.Title.Render(e.Name+"/"), "dir", ""
		case IsComposeFile(e.Name):
			name, kind = style.Success.Render(e.Name), "compose"
		}
		rows = append(rows, table.Row{name, kind, size, since(e.Modified)})
		v.visible = append(v.visible, e)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// Selected is the entry under the cursor.
func (v *DirView) Selected() (DirEntry, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return DirEntry{}, false
	}
	return v.visible[i], true
}

// HandleKey maps a keystroke to an app action: enter opens a directory in
// place or shows a file; u and e bring a compose file's project up, e after
// editing it.
func (v *DirView) HandleKey(key string) (string, string) {
	e, ok := v.Selected()
	if !ok {
		return "", ""
	}
	p := filepath.Join(v.dir, e.Name)
	switch key {
	case KeyEnter:
		if e.Dir {
			return "dir_open", p
		}
		return "dir_file", p
	case "u", "e":
		if e.Dir || !IsComposeFile(e.Name) {
			return "not_compose", e.Name
		}
		if key == "u" {
			return "compose_up_file", p
		}
		return "compose_edit_file", p
	}
	return "", ""
}

// Open moves into dir and lists it.
func (v *DirView) Open(dir string) tea.Cmd {
	v.dir = filepath.Clean(dir)
	v.loading, v.all = true, nil
	v.rebuildRows()
	v.table.SetCursor(0)
	return v.Refresh()
}

// Back moves up a directory, reporting false at the filesystem root, where
// esc leaves the view instead.
func (v *DirView) Back() (tea.Cmd, bool) {
	parent := filepath.Dir(v.dir)
	if parent == v.dir {
		return nil, false
	}
	return v.Open(parent), true
}

// UpdateTable forwards navigation keys.
func (v *DirView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *DirView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(dirColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *DirView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the listing is outstanding.
func (v *DirView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *DirView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// View renders the listing.
func (v *DirView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  empty directory")
	}
	return fixSelectedRow(v.table.View())
}

// Table is the view's table, for the keys every table shares.
func (v *DirView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *DirView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// CopyFields is the entry's name and its full path.
func (v *DirView) CopyFields() (string, string, bool) {
	e, ok := v.Selected()
	return e.Name, filepath.Join(v.dir, e.Name), ok
}

// Relayout lays the table out again after the column layouts changed.
func (v *DirView) Relayout() { v.relayout(&v.table, v.rebuildRows) }
