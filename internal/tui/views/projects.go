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

// ProjectsRefreshMsg carries the container list the projects view folds.
type ProjectsRefreshMsg struct {
	Err        error
	Containers []docker.Container
}

// ProjectsView groups containers by their Compose project label.
//
// There is no Engine-API endpoint for projects — Compose is a client-side
// convention expressed entirely through labels — so this view derives its
// rows from the same container list the containers view uses.
type ProjectsView struct {
	client *docker.Client
	err    error

	filter  string
	all     []docker.Project
	visible []docker.Project
	table   table.Model

	loading  bool
	inFlight bool
}

// NewProjectsView builds the projects view.
func NewProjectsView(client *docker.Client) *ProjectsView {
	t := table.New(
		table.WithColumns(projectColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &ProjectsView{client: client, table: t, loading: true}
}

func projectColumns() []table.Column {
	return []table.Column{
		{Title: "PROJECT", Width: 32},
		{Title: "STATUS", Width: 9},
		{Title: "SERVICES", Width: 50},
		{Title: "AGE", Width: 6},
	}
}

// Init kicks off the first fetch.
func (v *ProjectsView) Init() tea.Cmd { return v.refresh() }

// Selected returns the project under the cursor.
func (v *ProjectsView) Selected() (docker.Project, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.Project{}, false
	}
	return v.visible[i], true
}

// Update folds the refresh message in.
func (v *ProjectsView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(ProjectsRefreshMsg); ok {
		v.loading = false
		v.inFlight = false
		if m.Err != nil {
			v.err = docker.FormatUserError(m.Err)
			return nil
		}
		v.err = nil
		v.all = docker.Projects(m.Containers)
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *ProjectsView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *ProjectsView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(fitColumns(projectColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *ProjectsView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first fetch is outstanding.
func (v *ProjectsView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *ProjectsView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey maps a keystroke to an app action. Entering a project drops
// into the containers view filtered to that project — which is what
// "drill into a compose project" means when projects are only a label.
func (v *ProjectsView) HandleKey(key string) (string, string) {
	p, ok := v.Selected()
	if !ok {
		return "", ""
	}
	switch key {
	case KeyEnter:
		return "project_containers", p.Name
	case "x":
		return "confirm_stop_project", p.Name
	case "s":
		return "start_project", p.Name
	case KeyCtrlD:
		return "confirm_remove_project", p.Name
	}
	return "", ""
}

// View renders the table.
func (v *ProjectsView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if len(v.visible) == 0 && !v.loading {
		return style.Muted.Render("  no compose projects — nothing on this daemon carries a com.docker.compose.project label")
	}
	return fixSelectedRow(v.table.View())
}

// Refresh refetches the container list behind the projects.
func (v *ProjectsView) Refresh() tea.Cmd { return v.refresh() }

func (v *ProjectsView) refresh() tea.Cmd {
	if v.inFlight {
		return nil
	}
	v.inFlight = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// Always `all` — a project whose containers are every one stopped
		// is exactly the one you came here to restart.
		list, err := v.client.Containers(ctx, true)
		return ProjectsRefreshMsg{Containers: list, Err: err}
	}
}

func (v *ProjectsView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]

	for _, p := range v.all {
		if !f.Empty() && !f.MatchesAny(p.Name, p.ServiceList()) {
			continue
		}
		st := p.Status()
		switch p.Running {
		case p.Total:
			st = style.StateRunning.Render(st)
		case 0:
			st = style.StateExited.Render(st)
		default:
			st = style.StateRestarting.Render(st)
		}
		rows = append(rows, table.Row{
			truncate(p.Name, 32),
			st,
			truncate(p.ServiceList(), 50),
			p.Age,
		})
		v.visible = append(v.visible, p)
	}
	setTableRows(&v.table, rows)
}
