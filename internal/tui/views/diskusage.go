package views

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// diskUsageTimeout bounds `docker system df`. The daemon sizes every volume
// to answer, which on a large or VM-backed host is slow.
const diskUsageTimeout = 5 * time.Minute

// DiskUsageRefreshMsg carries a refreshed disk-usage summary.
type DiskUsageRefreshMsg struct {
	Err  error
	Rows []docker.DiskUsageRow
}

// DiskUsageView is `docker system df`: what each kind of object costs on
// disk and how much a prune would give back, with P on a row to prune that
// kind. It loads on open and on r, never on the poll — the volume walk is
// too expensive to repeat every three seconds.
type DiskUsageView struct {
	tableSort
	client *docker.Client
	err    error

	rows  []docker.DiskUsageRow
	table table.Model

	loading  bool
	inFlight bool
}

// NewDiskUsageView builds the view.
func NewDiskUsageView(client *docker.Client) *DiskUsageView {
	t := table.New(
		table.WithColumns(diskUsageColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &DiskUsageView{client: client, table: t, loading: true}
}

func diskUsageColumns() []table.Column {
	return []table.Column{
		{Title: "TYPE", Width: 16},
		{Title: "TOTAL", Width: 7},
		{Title: "ACTIVE", Width: 7},
		{Title: "SIZE", Width: 10},
		{Title: "RECLAIMABLE", Width: 18},
	}
}

// Init loads the summary.
func (v *DiskUsageView) Init() tea.Cmd { return v.refresh() }

// Selected returns the row under the cursor.
func (v *DiskUsageView) Selected() (docker.DiskUsageRow, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.rows) {
		return docker.DiskUsageRow{}, false
	}
	return v.rows[i], true
}

// Update folds the refresh message in.
func (v *DiskUsageView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(DiskUsageRefreshMsg); ok {
		v.loading = false
		v.inFlight = false
		if m.Err != nil {
			v.err = docker.FormatUserError(m.Err)
			return nil
		}
		v.err = nil
		v.rows = m.Rows
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *DiskUsageView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *DiskUsageView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.columns(fitColumns(diskUsageColumns(), width)))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the number of rows.
func (v *DiskUsageView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first load is outstanding.
func (v *DiskUsageView) Loading() bool { return v.loading }

// SetFilter — four fixed rows are not worth filtering.
func (v *DiskUsageView) SetFilter(string) {}

// HandleKey — P prunes the kind under the cursor, through the same confirmed
// prunes the per-resource views use.
func (v *DiskUsageView) HandleKey(key string) (string, string) {
	r, ok := v.Selected()
	if !ok || key != "P" {
		return "", ""
	}
	switch r.Type {
	case docker.DiskImages:
		return "confirm_prune_images", ""
	case docker.DiskContainers:
		return "confirm_prune_containers", ""
	case docker.DiskVolumes:
		return "confirm_prune_volumes", ""
	case docker.DiskBuildCache:
		return "confirm_prune_cache", ""
	}
	return "", ""
}

// View renders the table.
func (v *DiskUsageView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	return fixSelectedRow(v.table.View())
}

// Refresh reloads the summary.
func (v *DiskUsageView) Refresh() tea.Cmd { return v.refresh() }

func (v *DiskUsageView) refresh() tea.Cmd {
	if v.inFlight || v.client == nil {
		return nil
	}
	v.inFlight = true
	client := v.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(diskUsageTimeout)
		defer cancel()
		rows, err := client.DiskUsage(ctx)
		return DiskUsageRefreshMsg{Rows: rows, Err: err}
	}
}

func (v *DiskUsageView) rebuildRows() {
	rows := make([]table.Row, 0, len(v.rows))
	for _, r := range v.rows {
		rows = append(rows, table.Row{
			r.Type,
			fmt.Sprintf("%d", r.Total),
			fmt.Sprintf("%d", r.Active),
			docker.HumanSize(r.Size),
			reclaimableCell(r),
		})
	}
	sortRows(&v.tableSort, rows, v.rows)
	setTableRows(&v.table, rows)
}

// reclaimableCell is "196MB (12%)", as `docker system df` prints it.
func reclaimableCell(r docker.DiskUsageRow) string {
	cell := docker.HumanSize(r.Reclaimable)
	if r.Size > 0 {
		cell += fmt.Sprintf(" (%d%%)", r.Reclaimable*100/r.Size)
	}
	return cell
}

// Table is the view's table, for the keys every table shares.
func (v *DiskUsageView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *DiskUsageView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }
