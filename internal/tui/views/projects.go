package views

import (
	"fmt"
	"os"
	"strings"
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

	// busy names the compose operation in flight per project. `up` can
	// pull and build for minutes, and the row should say so.
	busy map[string]string

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
	return &ProjectsView{client: client, table: t, loading: true, busy: map[string]string{}}
}

func projectColumns() []table.Column {
	return []table.Column{
		{Title: "PROJECT", Width: 28},
		{Title: "STATUS", Width: 12},
		{Title: "SERVICES", Width: 40},
		{Title: "COMPOSE FILE", Width: 44},
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
//
// The lifecycle keys ask for compose verbs; the app falls back to acting on
// the containers one by one when the project's compose files are not on
// this machine.
func (v *ProjectsView) HandleKey(key string) (string, string) {
	p, ok := v.Selected()
	if !ok {
		return "", ""
	}
	switch key {
	case "u", "x", "R", "p", KeyCtrlD:
		if op := v.busy[p.Name]; op != "" {
			return "project_busy", p.Name + "\x00" + op
		}
	}
	switch key {
	case KeyEnter:
		return "project_containers", p.Name
	case "x":
		return "confirm_stop_project", p.Name
	case "u":
		return "compose_up", p.Name
	case "R":
		return "compose_restart", p.Name
	case "p":
		return "compose_pull", p.Name
	case KeyCtrlD:
		return "confirm_compose_down", p.Name
	}
	return "", ""
}

// Project returns the named project from the last listing.
func (v *ProjectsView) Project(name string) (docker.Project, bool) {
	for _, p := range v.all {
		if p.Name == name {
			return p, true
		}
	}
	return docker.Project{}, false
}

// SetBusy marks a project as mid-operation; an empty op clears it.
func (v *ProjectsView) SetBusy(project, op string) {
	if op == "" {
		delete(v.busy, project)
	} else {
		v.busy[project] = op
	}
	v.rebuildRows()
}

// Busy reports the operation in flight on a project, if any.
func (v *ProjectsView) Busy(project string) string { return v.busy[project] }

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
		ctx, cancel := v.client.RequestContext(20 * time.Second)
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
		switch {
		case v.busy[p.Name] != "":
			st = style.StateRestarting.Render(v.busy[p.Name] + "…")
		case p.Running == p.Total:
			st = style.StateRunning.Render(st)
		case p.Running == 0:
			st = style.StateExited.Render(st)
		default:
			st = style.StateRestarting.Render(st)
		}
		rows = append(rows, table.Row{
			truncate(p.Name, 28),
			st,
			truncate(p.ServiceList(), 40),
			composeFileCell(p),
			p.Age,
		})
		v.visible = append(v.visible, p)
	}
	setTableRows(&v.table, rows)
}

// composeFileCell shows where a project's compose file is, home-relative,
// or why compose verbs cannot run for it from here.
func composeFileCell(p docker.Project) string {
	if missing := p.MissingFile(); missing != "" {
		if len(p.ConfigFiles) == 0 {
			return style.Muted.Render("— none recorded")
		}
		return style.Muted.Render("— not on this machine")
	}
	cell := homeRelative(p.ConfigFiles[0])
	if n := len(p.ConfigFiles) - 1; n > 0 {
		cell += fmt.Sprintf(" +%d", n)
	}
	return truncateLeft(cell, 44)
}

// homeRelative abbreviates the home directory to ~.
func homeRelative(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, ok := strings.CutPrefix(path, home); ok && (rest == "" || rest[0] == '/') {
			return "~" + rest
		}
	}
	return path
}

// truncateLeft keeps the tail of s, where a path's distinguishing part is.
func truncateLeft(s string, maxLen int) string {
	r := []rune(s)
	if maxLen < 2 || len(r) <= maxLen {
		return s
	}
	return "…" + string(r[len(r)-maxLen+1:])
}
