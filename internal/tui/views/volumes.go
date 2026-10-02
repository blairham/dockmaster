package views

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// VolumesRefreshMsg carries a refreshed volume list.
type VolumesRefreshMsg struct {
	Err     error
	Volumes []docker.Volume
}

// VolumesView lists volumes.
type VolumesView struct {
	tableSort
	client *docker.Client
	err    error

	filter  string
	all     []docker.Volume
	visible []docker.Volume
	table   table.Model

	loading  bool
	withSize bool
	inFlight bool
}

// NewVolumesView builds the volumes view.
func NewVolumesView(client *docker.Client) *VolumesView {
	t := table.New(
		table.WithColumns(volumeColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &VolumesView{client: client, table: t, loading: true}
}

func volumeColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 40},
		{Title: "DRIVER", Width: 10},
		{Title: "SIZE", Width: 10},
		{Title: "REFS", Width: 6},
		{Title: "PROJECT", Width: 18},
		{Title: "MOUNTPOINT", Width: 40},
		{Title: "AGE", Width: 6},
	}
}

// Init kicks off the first fetch.
func (v *VolumesView) Init() tea.Cmd { return v.refresh() }

// ToggleSize turns the (expensive) size/ref walk on and off.
func (v *VolumesView) ToggleSize() tea.Cmd {
	v.withSize = !v.withSize
	return v.refresh()
}

// SizeEnabled reports whether size/ref counts are being fetched.
func (v *VolumesView) SizeEnabled() bool { return v.withSize }

// Selected returns the volume under the cursor.
func (v *VolumesView) Selected() (docker.Volume, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.Volume{}, false
	}
	return v.visible[i], true
}

// Update folds the refresh message in.
func (v *VolumesView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(VolumesRefreshMsg); ok {
		v.loading = false
		v.inFlight = false
		if m.Err != nil {
			v.err = docker.FormatUserError(m.Err)
			return nil
		}
		v.err = nil
		v.all = m.Volumes
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *VolumesView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *VolumesView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.columns(fitColumns(volumeColumns(), width)))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *VolumesView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first fetch is outstanding.
func (v *VolumesView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *VolumesView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey maps a keystroke to an app action.
func (v *VolumesView) HandleKey(key string) (string, string) {
	vol, ok := v.Selected()
	if !ok {
		switch key {
		case "z":
			return "toggle_size", ""
		case "P":
			return "confirm_prune_volumes", ""
		}
		return "", ""
	}
	switch key {
	case KeyEnter, "o":
		return "inspect_volume", vol.Name
	case "z":
		return "toggle_size", ""
	case "P":
		return "confirm_prune_volumes", ""
	case KeyCtrlD:
		return "confirm_remove_volume", vol.Name
	}
	return "", ""
}

// View renders the table.
func (v *VolumesView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  no volumes match")
	}
	return fixSelectedRow(v.table.View())
}

// Refresh refetches the volume list.
func (v *VolumesView) Refresh() tea.Cmd { return v.refresh() }

func (v *VolumesView) refresh() tea.Cmd {
	if v.inFlight {
		return nil
	}
	v.inFlight = true
	withSize := v.withSize
	return func() tea.Msg {
		ctx, cancel := v.client.RequestContext(60 * time.Second)
		defer cancel()
		list, err := v.client.Volumes(ctx, withSize)
		return VolumesRefreshMsg{Volumes: list, Err: err}
	}
}

func (v *VolumesView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]

	for _, vol := range v.all {
		if !f.Empty() && !f.MatchesAny(vol.Name, vol.Driver, vol.Project, vol.Mountpoint) {
			continue
		}

		size, refs := "—", "—"
		if vol.Size >= 0 {
			size = docker.HumanSize(vol.Size)
		}
		if vol.Refs >= 0 {
			refs = fmt.Sprintf("%d", vol.Refs)
			if vol.Refs == 0 {
				refs = style.Muted.Render("0")
			}
		}

		rows = append(rows, table.Row{
			truncate(vol.Name, 40),
			vol.Driver,
			size,
			refs,
			truncate(vol.Project, 18),
			truncate(vol.Mountpoint, 40),
			vol.Age(),
		})
		v.visible = append(v.visible, vol)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

// Total is the unfiltered volume count, for the info panel.
func (v *VolumesView) Total() int { return len(v.all) }

// Table is the view's table, for the keys every table shares.
func (v *VolumesView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *VolumesView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }
