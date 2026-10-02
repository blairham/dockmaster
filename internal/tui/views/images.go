package views

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// imageListTimeout bounds the image listing. See refresh() for why it is
// so much larger than every other call's.
const imageListTimeout = 5 * time.Minute

// ImagesRefreshMsg carries a refreshed image list.
type ImagesRefreshMsg struct {
	Err    error
	Images []docker.Image
}

// ImagesView lists local images.
type ImagesView struct {
	tableMarks
	tableSort
	client *docker.Client
	err    error

	filter  string
	all     []docker.Image
	visible []docker.Image
	table   table.Model

	loading  bool
	showAll  bool
	inFlight bool
}

// NewImagesView builds the images view. showAll includes intermediate
// build layers (`docker images -a`).
func NewImagesView(client *docker.Client, showAll bool) *ImagesView {
	t := table.New(
		table.WithColumns(imageColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &ImagesView{client: client, table: t, loading: true, showAll: showAll}
}

func imageColumns() []table.Column {
	return []table.Column{
		{Title: "REPOSITORY", Width: 44},
		{Title: "TAG", Width: 22},
		{Title: "IMAGE ID", Width: 13},
		{Title: "SIZE", Width: 10},
		{Title: "USED BY", Width: 8},
		{Title: "AGE", Width: 6},
	}
}

// Init kicks off the first fetch.
func (v *ImagesView) Init() tea.Cmd { return v.refresh() }

// ToggleAll flips intermediate-layer visibility and refetches.
func (v *ImagesView) ToggleAll() tea.Cmd {
	v.showAll = !v.showAll
	return v.refresh()
}

// ShowAll reports whether intermediate layers are included.
func (v *ImagesView) ShowAll() bool { return v.showAll }

// Selected returns the image under the cursor.
func (v *ImagesView) Selected() (docker.Image, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.Image{}, false
	}
	return v.visible[i], true
}

// Update folds the refresh message in.
func (v *ImagesView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(ImagesRefreshMsg); ok {
		v.loading = false
		v.inFlight = false
		if m.Err != nil {
			v.err = docker.FormatUserError(m.Err)
			return nil
		}
		v.err = nil
		v.all = m.Images
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *ImagesView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *ImagesView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.columns(fitColumns(imageColumns(), width)))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *ImagesView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first fetch is outstanding.
func (v *ImagesView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *ImagesView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey maps a keystroke to an app action.
func (v *ImagesView) HandleKey(key string) (string, string) {
	im, ok := v.Selected()
	if !ok {
		switch key {
		case "a":
			return "toggle_all", ""
		case "P":
			return "confirm_prune_images", ""
		}
		return "", ""
	}
	return v.keyFor(key, im)
}

// keyFor is the action key asks for on one row.
func (v *ImagesView) keyFor(key string, im docker.Image) (string, string) {
	switch key {
	case KeyEnter, "L":
		return "layers", im.ID
	case "o":
		return "inspect_image", im.ID
	case "u":
		// Run it: by reference, or by ID for a dangling image.
		if im.Dangling || im.Repo == "" || im.Repo == "<none>" {
			return "run_image", im.ID
		}
		return "run_image", im.Ref()
	case "a":
		return "toggle_all", ""
	case "P":
		return "confirm_prune_images", ""
	case KeyCtrlD:
		// Remove by reference when we have one: deleting by ID fails on a
		// multi-tagged image ("image is referenced in multiple
		// repositories") and the user meant this row, which is one tag.
		ref := im.Ref()
		if im.Dangling {
			ref = im.ID
		}
		return "confirm_remove_image", ref
	}
	return "", ""
}

// View renders the table.
func (v *ImagesView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  no images match")
	}
	return fixSelectedRow(v.table.View())
}

// Refresh refetches the image list.
func (v *ImagesView) Refresh() tea.Cmd { return v.refresh() }

func (v *ImagesView) refresh() tea.Cmd {
	if v.inFlight {
		return nil
	}
	v.inFlight = true
	all := v.showAll
	return func() tea.Msg {
		// Generous on purpose. `docker images` is O(local image store) and
		// on a VM-backed daemon with a few hundred images it is measured in
		// minutes, not seconds — a host here took over two minutes for 651
		// images through the CLI itself. The single-flight guard above is
		// what keeps that from piling up; the timeout only has to be longer
		// than the daemon's worst honest answer.
		ctx, cancel := v.client.RequestContext(imageListTimeout)
		defer cancel()
		list, err := v.client.Images(ctx, all)
		return ImagesRefreshMsg{Images: list, Err: err}
	}
}

func (v *ImagesView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]

	for _, im := range v.all {
		if !f.Empty() && !f.MatchesAny(im.Repo, im.Tag, im.Ref(), im.Short()) {
			continue
		}

		repo, tag := im.Repo, im.Tag
		if im.Dangling {
			repo = style.Muted.Render("<none>")
			tag = style.Muted.Render("<none>")
		}

		used := "—"
		switch {
		case im.Containers > 0:
			used = fmt.Sprintf("%d", im.Containers)
		case im.Containers == 0:
			used = style.Muted.Render("0")
		}

		rows = append(rows, table.Row{
			truncate(repo, 44),
			truncate(tag, 22),
			im.Short(),
			docker.HumanSize(im.Size),
			used,
			im.Age(),
		})
		v.visible = append(v.visible, im)
	}
	sortRows(&v.tableSort, rows, v.visible)
	rows = markRows(&v.tableMarks, rows, v.visible, v.all, imageMarkKey)
	setTableRows(&v.table, rows)
}

// RefFor resolves an image ID to its repo:tag reference.
func (v *ImagesView) RefFor(id string) string {
	for i := range v.all {
		if v.all[i].ID == id || v.all[i].Ref() == id {
			return v.all[i].Ref()
		}
	}
	return ""
}

// Total is the unfiltered image-row count, for the info panel.
func (v *ImagesView) Total() int { return len(v.all) }

// Table is the view's table, for the keys every table shares.
func (v *ImagesView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *ImagesView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// imageMarkKey identifies a row for marks.
func imageMarkKey(im docker.Image) string { return im.ID + "\x00" + im.Ref() }

// MarkKey marks rows on space, ctrl+space and ctrl+\.
func (v *ImagesView) MarkKey(key string) bool {
	if !markKey(&v.tableMarks, key, v.visible, v.table.Cursor(), imageMarkKey) {
		return false
	}
	v.rebuildRows()
	return true
}

// ClearMarks unmarks every row.
func (v *ImagesView) ClearMarks() {
	if v.marks != nil {
		v.marks.Clear()
		v.rebuildRows()
	}
}

// BulkKey is key's action on every marked row; nil when none is marked.
func (v *ImagesView) BulkKey(key string) []Action {
	return bulk(&v.tableMarks, v.visible, imageMarkKey, func(im docker.Image) Action {
		name, param := v.keyFor(key, im)
		return Action{Name: name, Param: param, Label: im.Ref()}
	})
}
