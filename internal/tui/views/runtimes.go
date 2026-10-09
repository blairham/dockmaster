// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// runtimesListTimeout bounds listing every runtime. Each is a local CLI
// call or a socket ping; anything slower is a wedged runtime.
const runtimesListTimeout = 20 * time.Second

// RuntimesRefreshMsg carries every runtime's machines. Err joins the
// runtimes that failed to list; the others' machines are still shown.
type RuntimesRefreshMsg struct {
	Err      error
	Machines []engines.Machine
}

// RuntimesView lists the container runtimes installed on this machine and
// their VMs or engines — Colima profiles, Podman machines, Docker Desktop,
// Rancher Desktop, OrbStack — and their lifecycle.
//
// It manages what is underneath the daemon rather than anything in it, so
// it is the one view that keeps working while the daemon is down: stopping
// the machine dockmaster is connected to takes every other view's data source
// away, and this is where you start it again.
type RuntimesView struct {
	tableSort
	providers []engines.Provider
	err       error

	// busy names the operation in flight per machine ("starting", ...);
	// a start runs for a minute and most runtimes report "Stopped" for
	// most of it.
	busy map[string]string

	// connectedHost is the docker endpoint dockmaster is talking to, so the
	// machine serving it can be marked.
	connectedHost string

	filter  string
	all     []engines.Machine
	visible []engines.Machine
	table   table.Model

	loading  bool
	inFlight bool
}

// NewRuntimesView builds the view over the detected providers.
func NewRuntimesView(providers []engines.Provider, connectedHost string) *RuntimesView {
	t := table.New(
		table.WithColumns(runtimeColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &RuntimesView{
		tableSort: tableSort{layoutKey: "runtimes"},
		providers: providers, connectedHost: connectedHost,
		busy: map[string]string{}, table: t, loading: len(providers) > 0,
	}
}

func runtimeColumns() []table.Column {
	return []table.Column{
		{Title: "", Width: 2},
		{Title: "PROVIDER", Width: 15},
		{Title: "NAME", Width: 22},
		{Title: "STATUS", Width: 12},
		{Title: "ARCH", Width: 8},
		{Title: "CPUS", Width: 5},
		{Title: "MEMORY", Width: 8},
		{Title: "DISK", Width: 8},
		{Title: "RUNTIME", Width: 11},
		{Title: "K8S", Width: 4},
		{Title: "CONTEXT", Width: 16},
	}
}

// MachineKey identifies a machine across providers.
func MachineKey(provider, name string) string { return provider + "\x00" + name }

// SplitMachineKey is the inverse of MachineKey.
func SplitMachineKey(k string) (provider, name string) {
	provider, name, _ = strings.Cut(k, "\x00")
	return provider, name
}

// Init kicks off the first listing.
func (v *RuntimesView) Init() tea.Cmd { return v.refresh() }

// Providers are the detected runtimes.
func (v *RuntimesView) Providers() []engines.Provider { return v.providers }

// Provider finds a detected runtime by name.
func (v *RuntimesView) Provider(name string) (engines.Provider, bool) {
	for _, p := range v.providers {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// Selected returns the machine under the cursor.
func (v *RuntimesView) Selected() (engines.Machine, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return engines.Machine{}, false
	}
	return v.visible[i], true
}

// Machine returns a machine from the last listing.
func (v *RuntimesView) Machine(provider, name string) (engines.Machine, bool) {
	for _, m := range v.all {
		if m.Provider == provider && m.Name == name {
			return m, true
		}
	}
	return engines.Machine{}, false
}

// Machines is the last listing.
func (v *RuntimesView) Machines() []engines.Machine { return v.all }

// Update folds the refresh message in.
func (v *RuntimesView) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(RuntimesRefreshMsg); ok {
		v.loading = false
		v.inFlight = false
		v.err = m.Err
		v.all = m.Machines
		v.rebuildRows()
	}
	return nil
}

// SetBusy marks a machine as mid-operation; an empty op clears it.
func (v *RuntimesView) SetBusy(provider, name, op string) {
	if op == "" {
		delete(v.busy, MachineKey(provider, name))
	} else {
		v.busy[MachineKey(provider, name)] = op
	}
	v.rebuildRows()
}

// Busy reports the operation in flight on a machine, if any.
func (v *RuntimesView) Busy(provider, name string) string { return v.busy[MachineKey(provider, name)] }

// SetConnectedHost records which endpoint dockmaster is talking to.
func (v *RuntimesView) SetConnectedHost(host string) {
	v.connectedHost = host
	v.rebuildRows()
}

// UpdateTable forwards navigation keys.
func (v *RuntimesView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *RuntimesView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(runtimeColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *RuntimesView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first listing is outstanding.
func (v *RuntimesView) Loading() bool { return v.loading }

// SetFilter applies a row filter.
func (v *RuntimesView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// HandleKey maps a keystroke to an app action; the param is the machine's
// MachineKey. Whether the runtime supports the verb is the app's to check,
// so the refusal can name the runtime.
//
// Stop and restart confirm, unlike their container counterparts: a machine
// is the VM every container on it runs in.
func (v *RuntimesView) HandleKey(key string) (string, string) {
	if key == "n" && len(v.providers) > 0 {
		provider := ""
		if m, ok := v.Selected(); ok {
			provider = m.Provider
		}
		return "runtime_new", provider
	}
	m, ok := v.Selected()
	if !ok {
		return "", ""
	}
	k := MachineKey(m.Provider, m.Name)
	action, ok := runtimeKeys[key]
	if !ok {
		return "", ""
	}
	if op := v.busy[k]; op != "" && key != "s" && key != "o" {
		return "runtime_busy", k + "\x00" + op
	}
	return action, k
}

// runtimeKeys are the runtimes view's per-machine keys. Every one but s and
// o is refused while the machine is busy.
var runtimeKeys = map[string]string{
	KeyEnter: "runtime_connect",
	"s":      "runtime_shell",
	"o":      "runtime_inspect",
	"u":      "runtime_start",
	"e":      "runtime_edit",
	"x":      "confirm_runtime_stop",
	"R":      "confirm_runtime_restart",
	KeyCtrlD: "confirm_runtime_delete",
	"K":      "confirm_runtime_k8s",
}

// View renders the table.
func (v *RuntimesView) View() string {
	if len(v.providers) == 0 {
		return style.Muted.Render(
			"  no container runtime found — install colima, podman, Docker Desktop, Rancher Desktop or OrbStack to manage it from here",
		)
	}
	var b strings.Builder
	if v.err != nil {
		b.WriteString(style.Error.Render(fmt.Sprintf("  %v", v.err)) + "\n")
	}
	if len(v.visible) == 0 && !v.loading {
		if len(v.all) == 0 {
			b.WriteString(style.Muted.Render("  no machines — <n> creates one"))
		} else {
			b.WriteString(style.Muted.Render("  no machines match"))
		}
		return b.String()
	}
	b.WriteString(fixSelectedRow(v.table.View()))
	return b.String()
}

// Refresh relists every runtime.
func (v *RuntimesView) Refresh() tea.Cmd { return v.refresh() }

func (v *RuntimesView) refresh() tea.Cmd {
	if len(v.providers) == 0 || v.inFlight {
		return nil
	}
	v.inFlight = true
	providers := v.providers
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), runtimesListTimeout)
		defer cancel()
		// In parallel: each runtime is its own CLI, and one slow to answer
		// must not hold the others' rows back. Results keep provider order.
		type result struct {
			err error
			ms  []engines.Machine
		}
		results := make([]result, len(providers))
		var wg sync.WaitGroup
		for i, p := range providers {
			wg.Go(func() {
				ms, err := p.List(ctx)
				if err != nil {
					err = fmt.Errorf("%s: %w", p.Name(), err)
				}
				results[i] = result{ms: ms, err: err}
			})
		}
		wg.Wait()
		var (
			all  []engines.Machine
			errs []error
		)
		for _, r := range results {
			if r.err != nil {
				errs = append(errs, r.err)
				continue
			}
			all = append(all, r.ms...)
		}
		// A machine's own Kubernetes is not the only kind: kind and k3d run
		// clusters as containers on its daemon. Starting the runtime's own
		// on top of one adds a second cluster and takes kubectl's context.
		for i := range all {
			if all[i].Running && all[i].Host != "" {
				all[i].Clusters = ClusterProbe(ctx, all[i].Host)
			}
		}
		return RuntimesRefreshMsg{Machines: all, Err: errors.Join(errs...)}
	}
}

func (v *RuntimesView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]

	for _, m := range v.all {
		if !f.Empty() && !f.MatchesAny(m.Provider, m.Name, m.Status, m.Runtime, m.Arch, m.Context) {
			continue
		}
		marker, name := " ", m.Name
		if m.Host != "" && m.Host == v.connectedHost {
			marker = style.Success.Render("▸")
			name = style.Success.Render(m.Name)
		}
		cpus := style.Muted.Render("—")
		if m.CPUs > 0 {
			cpus = strconv.Itoa(m.CPUs)
		}
		ctx := m.Context
		if ctx == "" {
			ctx = style.Muted.Render("—")
		}
		rows = append(rows, table.Row{
			marker,
			m.Provider,
			truncate(name, 22),
			v.statusCell(m),
			m.Arch,
			cpus,
			gib(m.Memory),
			gib(m.Disk),
			m.Runtime,
			kubeCell(m),
			truncate(ctx, 16),
		})
		v.visible = append(v.visible, m)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

func (v *RuntimesView) statusCell(m engines.Machine) string {
	if op := v.busy[MachineKey(m.Provider, m.Name)]; op != "" {
		return style.StateRestarting.Render(op + "…")
	}
	if m.Running {
		return style.StateRunning.Render(m.Status)
	}
	return style.StateExited.Render(m.Status)
}

// gib renders a byte count in GiB, the unit colima's own flags and listing
// use: a 16GiB VM shown as "17.2GB" reads like a different machine.
func gib(n int64) string {
	if n <= 0 {
		return style.Muted.Render("—")
	}
	const g = 1 << 30
	if n%g == 0 {
		return fmt.Sprintf("%dGiB", n/g)
	}
	return fmt.Sprintf("%.1fGiB", float64(n)/g)
}

// ClusterProbe lists the kind/k3d clusters on the daemon at host. A var so
// tests can answer for a daemon that is not there.
var ClusterProbe = func(ctx context.Context, host string) []engines.Cluster {
	cs, err := docker.Clusters(ctx, host)
	if err != nil {
		return nil
	}
	out := make([]engines.Cluster, 0, len(cs))
	for _, c := range cs {
		out = append(out, engines.Cluster{Tool: c.Tool, Name: c.Name, Registry: c.Registry, Running: c.Running})
	}
	return out
}

// kubeCell is the K8S column: the kind/k3d cluster on the machine's daemon
// — bright when running, dim when stopped — else the runtime's own built-in
// cluster when it is on (orange: K manages kind, not that), else a dash.
func kubeCell(m engines.Machine) string {
	if len(m.Clusters) > 0 {
		c := m.Clusters[0]
		if c.Running {
			return style.Success.Render(c.Tool)
		}
		return style.Muted.Render(c.Tool)
	}
	if m.K8s == engines.KubeOn {
		return lipgloss.NewStyle().Foreground(style.ColorOrange).Render("own")
	}
	return style.Muted.Render("—")
}

// Table is the view's table, for the keys every table shares.
func (v *RuntimesView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *RuntimesView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// Relayout lays the table out again after the column layouts changed.
func (v *RuntimesView) Relayout() { v.relayout(&v.table, v.rebuildRows) }
