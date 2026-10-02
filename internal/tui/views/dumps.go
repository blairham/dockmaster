package views

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// DumpKinds are the subdirectories of the state directory that ctrl+s
// writes to: table dumps and saved logs.
var DumpKinds = []string{"dumps", "logs"}

// Dump is one saved file.
type Dump struct {
	Modified time.Time
	Name     string
	Kind     string
	Path     string
	Size     int64
}

// DumpsRefreshMsg carries the listing of saved files.
type DumpsRefreshMsg struct {
	Err   error
	Dumps []Dump
}

// DumpsView lists what ctrl+s has saved — k9s's :sd — so a dump can be read
// back or cleared out without leaving dockmaster.
type DumpsView struct {
	err error
	tableSort
	root    string
	filter  string
	all     []Dump
	visible []Dump
	table   table.Model
	loading bool
}

// NewDumpsView lists the saves under root, the state directory.
func NewDumpsView(root string) *DumpsView {
	t := table.New(
		table.WithColumns(dumpColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &DumpsView{table: t, root: root, loading: true}
}

func dumpColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 56},
		{Title: "KIND", Width: 6},
		{Title: "SIZE", Width: 9},
		{Title: "AGE", Width: 6},
	}
}

// Init reads the directories.
func (v *DumpsView) Init() tea.Cmd { return v.Refresh() }

// Selected returns the save under the cursor.
func (v *DumpsView) Selected() (Dump, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return Dump{}, false
	}
	return v.visible[i], true
}

// Update folds the listing in.
func (v *DumpsView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(DumpsRefreshMsg); ok {
		v.loading = false
		v.all, v.err = m.Dumps, m.Err
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *DumpsView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *DumpsView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.columns(fitColumns(dumpColumns(), width)))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *DumpsView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the listing is outstanding.
func (v *DumpsView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *DumpsView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey maps a keystroke to an app action.
func (v *DumpsView) HandleKey(key string) (string, string) {
	d, ok := v.Selected()
	if !ok {
		return "", ""
	}
	switch key {
	case KeyEnter:
		return "open_dump", d.Path
	case KeyCtrlD:
		return "confirm_remove_dump", d.Path
	}
	return "", ""
}

// View renders the table.
func (v *DumpsView) View() string {
	if v.err != nil {
		return style.Error.Render("  " + v.err.Error())
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  nothing saved yet — ctrl+s in a table or a log saves one")
	}
	return fixSelectedRow(v.table.View())
}

// Refresh re-reads the directories. It is a filesystem read, so it is
// cheap enough to do on the ordinary poll.
func (v *DumpsView) Refresh() tea.Cmd {
	root := v.root
	return func() tea.Msg {
		d, err := ListDumps(root)
		return DumpsRefreshMsg{Dumps: d, Err: err}
	}
}

// ListDumps reads every save under root's dump directories, newest first.
// A directory that does not exist yet is an empty one: nothing has been
// saved there.
func ListDumps(root string) ([]Dump, error) {
	var out []Dump
	for _, kind := range DumpKinds {
		dir := filepath.Join(root, kind)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue // removed between the read and the stat
			}
			out = append(out, Dump{
				Name: e.Name(), Kind: kind, Path: filepath.Join(dir, e.Name()),
				Size: info.Size(), Modified: info.ModTime(),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

func (v *DumpsView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]

	for _, d := range v.all {
		if !f.Empty() && !f.MatchesAny(d.Name, d.Kind) {
			continue
		}
		rows = append(rows, table.Row{
			truncate(d.Name, 56),
			d.Kind,
			docker.HumanSize(d.Size),
			since(d.Modified),
		})
		v.visible = append(v.visible, d)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// Table is the view's table, for the keys every table shares.
func (v *DumpsView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *DumpsView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// dumpFileLimit caps how much of a save the viewer reads. Saved logs are
// bounded by logger.buffer, but a file dropped in by hand is not.
const dumpFileLimit = volumeFileLimit

// NewDumpFileView shows a saved file in the plain-text viewer.
func NewDumpFileView(path string) *InspectView {
	v := NewInspectFetchView(filepath.Base(path), func(context.Context) ([]byte, error) {
		b, more, err := readHead(path, dumpFileLimit)
		if err != nil {
			return nil, err
		}
		return []byte(volumeFileText(b, more)), nil
	})
	v.plain = true
	return v
}

// readHead reads at most limit bytes of path, and whether there was more.
func readHead(path string, limit int) ([]byte, bool, error) {
	f, err := os.Open(path) //nolint:gosec // a path the dumps listing produced
	if err != nil {
		return nil, false, err
	}
	defer f.Close() //nolint:errcheck // read-only file
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(b) > limit {
		return b[:limit], true, nil
	}
	return b, false, nil
}

// IsDumpPath reports whether path is a file directly inside one of root's
// dump directories — the only files :sd may delete.
func IsDumpPath(root, path string) bool {
	dir := filepath.Dir(filepath.Clean(path))
	for _, kind := range DumpKinds {
		if dir == filepath.Join(root, kind) {
			return true
		}
	}
	return false
}
