package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// PodsRefreshMsg carries the pods of every running podman machine.
type PodsRefreshMsg struct {
	Err  error
	Pods []engines.Pod
}

// PodsView lists podman pods. The Docker API — which every other view
// speaks — has no pods, so these come from the podman CLI, across every
// running podman machine.
type PodsView struct {
	tableSort
	podman *engines.Podman // nil when podman is not installed
	err    error

	filter  string
	all     []engines.Pod
	visible []engines.Pod
	table   table.Model

	loading  bool
	inFlight bool
}

// NewPodsView builds the view; podman is nil when it is not installed.
func NewPodsView(podman *engines.Podman) *PodsView {
	t := table.New(
		table.WithColumns(podColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &PodsView{podman: podman, table: t, loading: podman != nil}
}

func podColumns() []table.Column {
	return []table.Column{
		{Title: "MACHINE", Width: 18},
		{Title: "NAME", Width: 28},
		{Title: "STATUS", Width: 10},
		{Title: "READY", Width: 7},
		{Title: "CONTAINERS", Width: 40},
		{Title: "AGE", Width: 6},
	}
}

// PodKey identifies a pod: its machine and ID.
func PodKey(p engines.Pod) string { return p.Machine + "\x00" + p.ID + "\x00" + p.Name }

// SplitPodKey is the inverse of PodKey.
func SplitPodKey(k string) (machine, id, name string) {
	parts := strings.SplitN(k, "\x00", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

// Init loads the pods.
func (v *PodsView) Init() tea.Cmd { return v.refresh() }

// Selected returns the pod under the cursor.
func (v *PodsView) Selected() (engines.Pod, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return engines.Pod{}, false
	}
	return v.visible[i], true
}

// Update folds the refresh message in.
func (v *PodsView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(PodsRefreshMsg); ok {
		v.loading = false
		v.inFlight = false
		v.err = m.Err
		v.all = m.Pods
		v.rebuildRows()
	}
	return nil
}

// UpdateTable forwards navigation keys.
func (v *PodsView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *PodsView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.columns(fitColumns(podColumns(), width)))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *PodsView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first load is outstanding.
func (v *PodsView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *PodsView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey maps a keystroke to an app action.
func (v *PodsView) HandleKey(key string) (string, string) {
	p, ok := v.Selected()
	if !ok {
		return "", ""
	}
	k := PodKey(p)
	switch key {
	case KeyEnter, "o":
		return "pod_inspect", k
	case "u":
		return "pod_start", k
	case "x":
		return "pod_stop", k
	case "R":
		return "pod_restart", k
	case KeyCtrlD:
		return "confirm_pod_rm", k
	}
	return "", ""
}

// View renders the table.
func (v *PodsView) View() string {
	if v.podman == nil {
		return style.Muted.Render("  podman is not installed — pods are a podman feature")
	}
	var b strings.Builder
	if v.err != nil {
		b.WriteString(style.Error.Render(fmt.Sprintf("  %v", v.err)) + "\n")
	}
	if len(v.visible) == 0 && !v.loading {
		if len(v.all) == 0 {
			b.WriteString(style.Muted.Render("  no pods in any running podman machine"))
		} else {
			b.WriteString(style.Muted.Render("  no pods match"))
		}
		return b.String()
	}
	b.WriteString(fixSelectedRow(v.table.View()))
	return b.String()
}

// Refresh reloads the pods.
func (v *PodsView) Refresh() tea.Cmd { return v.refresh() }

func (v *PodsView) refresh() tea.Cmd {
	if v.podman == nil || v.inFlight {
		return nil
	}
	v.inFlight = true
	p := *v.podman
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pods, err := p.Pods(ctx)
		return PodsRefreshMsg{Pods: pods, Err: err}
	}
}

func (v *PodsView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, p := range v.all {
		names := make([]string, 0, len(p.Containers))
		for _, c := range p.Containers {
			names = append(names, c.Name)
		}
		members := strings.Join(names, ",")
		if !f.Empty() && !f.MatchesAny(p.Machine, p.Name, p.Status, members) {
			continue
		}
		rows = append(rows, table.Row{
			truncate(p.Machine, 18),
			truncate(p.Name, 28),
			podStatusCell(p.Status),
			fmt.Sprintf("%d/%d", p.Running(), len(p.Containers)),
			truncate(members, 40),
			docker.Container{Created: p.Created}.Age(),
		})
		v.visible = append(v.visible, p)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

func podStatusCell(s string) string {
	switch strings.ToLower(s) {
	case "running":
		return style.StateRunning.Render(s)
	case "degraded":
		return style.StateRestarting.Render(s)
	case "exited", "stopped", "dead":
		return style.StateExited.Render(s)
	}
	return style.StateCreated.Render(s)
}

// Table is the view's table, for the keys every table shares.
func (v *PodsView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *PodsView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }
