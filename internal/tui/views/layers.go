package views

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
)

// LayersRefreshMsg carries an image's build history.
type LayersRefreshMsg struct {
	Err     error
	ImageID string
	Layers  []docker.ImageLayer
}

// LayersView shows an image's build history, newest layer first.
type LayersView struct {
	client *docker.Client
	err    error

	imageID  string
	imageRef string
	filter   string

	all     []docker.ImageLayer
	visible []docker.ImageLayer
	table   table.Model

	loading bool
}

// NewLayersView builds the image-history drill-in.
func NewLayersView(client *docker.Client, imageID, ref string) *LayersView {
	t := table.New(
		table.WithColumns(layerColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &LayersView{client: client, table: t, imageID: imageID, imageRef: ref, loading: true}
}

func layerColumns() []table.Column {
	return []table.Column{
		{Title: "LAYER", Width: 13},
		{Title: "SIZE", Width: 10},
		{Title: "AGE", Width: 6},
		{Title: "CREATED BY", Width: 70},
	}
}

// Title is the image reference, for the border title.
func (v *LayersView) Title() string { return v.imageRef }

// Init kicks off the fetch.
func (v *LayersView) Init() tea.Cmd { return v.Refresh() }

// Update folds the refresh message in.
func (v *LayersView) Update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(LayersRefreshMsg)
	if !ok || m.ImageID != v.imageID {
		return nil
	}
	v.loading = false
	if m.Err != nil {
		v.err = docker.FormatUserError(m.Err)
		return nil
	}
	v.err = nil
	v.all = m.Layers
	v.rebuildRows()
	return nil
}

// UpdateTable forwards navigation keys.
func (v *LayersView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *LayersView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(fitColumns(layerColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *LayersView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the fetch is outstanding.
func (v *LayersView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *LayersView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey has no layer-level actions — layers are immutable.
func (v *LayersView) HandleKey(string) (string, string) { return "", "" }

// View renders the table.
func (v *LayersView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	return fixSelectedRow(v.table.View())
}

// Refresh refetches the history.
func (v *LayersView) Refresh() tea.Cmd {
	id, client := v.imageID, v.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		layers, err := client.ImageHistory(ctx, id)
		return LayersRefreshMsg{ImageID: id, Layers: layers, Err: err}
	}
}

func (v *LayersView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]

	for _, l := range v.all {
		if !f.Empty() && !f.MatchesAny(l.CreatedBy, l.Comment) {
			continue
		}
		id := "<missing>"
		if l.ID != "" && l.ID != "<missing>" {
			id = l.ID
			if len(id) > 12 {
				id = id[:12]
			}
		}
		size := docker.HumanSize(l.Size)
		if l.Size == 0 {
			size = style.Muted.Render("0B")
		}
		rows = append(rows, table.Row{
			id,
			size,
			since(l.Created),
			truncate(l.CreatedBy, 70),
		})
		v.visible = append(v.visible, l)
	}
	setTableRows(&v.table, rows)
}

// since is a local copy of the docker package's age formatter — the views
// need it for types that carry a time.Time rather than a pre-formatted Age.
func since(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}
