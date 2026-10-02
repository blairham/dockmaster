package views

import (
	"fmt"
	"strconv"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// NodeRefreshMsg carries the containers inside one Kubernetes node.
type NodeRefreshMsg struct {
	Err        error
	Node       string
	Containers []docker.NodeContainer
}

// NodeView lists the containers inside a kind or k3d node container — the
// pods' workloads, which run in the node's own containerd and never appear
// in `docker ps`. Read through crictl; see docs/design/kubernetes-nodes.md.
type NodeView struct {
	client *docker.Client
	err    error

	node   string // node container ID, for exec
	name   string // node container name, for the title
	filter string

	all     []docker.NodeContainer
	visible []docker.NodeContainer
	table   table.Model

	// registry is a line about the cluster's local registry, shown above
	// the containers ("" when it has none).
	registry string
	height   int
	loading  bool
	inFlight bool
	// showAll lists exited containers too. Off by default: most exited
	// rows are a running container's previous attempt, kept by the
	// kubelet for `kubectl logs --previous`, and they double the list.
	showAll bool
}

// NewNodeView builds the drill-in for node (ID) named name.
func NewNodeView(client *docker.Client, node, name string) *NodeView {
	t := table.New(
		table.WithColumns(nodeColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &NodeView{client: client, table: t, node: node, name: name, loading: true}
}

func nodeColumns() []table.Column {
	return []table.Column{
		{Title: "NAMESPACE", Width: 16},
		{Title: "POD", Width: 40},
		{Title: "NAME", Width: 24},
		{Title: "STATE", Width: 9},
		{Title: "RESTARTS", Width: 8},
		{Title: "IMAGE", Width: 36},
		{Title: "AGE", Width: 6},
	}
}

// Title is the node's name, for the border title.
func (v *NodeView) Title() string { return v.name }

// SetRegistry gives the node view a line about its cluster's registry.
func (v *NodeView) SetRegistry(line string) {
	v.registry = line
	v.Resize(v.table.Width(), v.height)
}

// ShowAll reports whether exited containers are listed.
func (v *NodeView) ShowAll() bool { return v.showAll }

// ToggleAll shows or hides exited containers.
func (v *NodeView) ToggleAll() {
	v.showAll = !v.showAll
	v.rebuildRows()
}

// Node is the node container's ID.
func (v *NodeView) Node() string { return v.node }

// Selected is the container under the cursor.
func (v *NodeView) Selected() (docker.NodeContainer, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return docker.NodeContainer{}, false
	}
	return v.visible[i], true
}

// Init kicks off the first listing.
func (v *NodeView) Init() tea.Cmd { return v.Refresh() }

// Update folds a listing in.
func (v *NodeView) Update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(NodeRefreshMsg)
	if !ok || m.Node != v.node {
		return nil
	}
	v.loading, v.inFlight = false, false
	if m.Err != nil {
		v.err = docker.FormatUserError(m.Err)
		return nil
	}
	v.err = nil
	v.all = m.Containers
	v.rebuildRows()
	return nil
}

// UpdateTable forwards navigation keys.
func (v *NodeView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *NodeView) Resize(width, height int) {
	v.height = height
	if v.registry != "" {
		height = max(height-1, 1)
	}
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(fitColumns(nodeColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *NodeView) Count() int { return len(v.table.Rows()) }

// Loading reports whether the first listing is outstanding.
func (v *NodeView) Loading() bool { return v.loading }

// SetFilter applies a row filter over namespace, pod, name, state and image.
func (v *NodeView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// NodeParam packs what a node action needs — node, container, display
// name — into the one string an action carries.
func NodeParam(node, id, name string) string { return node + "\x00" + id + "\x00" + name }

// HandleKey — enter/l logs, o inspect, s shell, ctrl-d remove (exited
// only), a show/hide exited.
func (v *NodeView) HandleKey(key string) (string, string) {
	if key == "a" {
		return "toggle_all", ""
	}
	c, ok := v.Selected()
	if !ok {
		return "", ""
	}
	p := NodeParam(v.node, c.ID, c.Pod+"/"+c.Name)
	switch key {
	case KeyCtrlD, KeyDelete:
		if c.State == "running" {
			return "node_remove_running", p
		}
		return "confirm_node_remove", p
	case KeyEnter, "l":
		return "node_logs", p
	case "o":
		return "node_inspect", p
	case "s":
		return "node_shell", p
	}
	return "", ""
}

// View renders the table.
func (v *NodeView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if v.registry != "" {
		return v.registry + "\n" + fixSelectedRow(v.table.View())
	}
	return fixSelectedRow(v.table.View())
}

// Refresh relists the node's containers, single-flighted like every list:
// each listing is an exec into the node.
func (v *NodeView) Refresh() tea.Cmd {
	if v.inFlight || v.client == nil {
		return nil
	}
	v.inFlight = true
	node, client := v.node, v.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(20 * time.Second)
		defer cancel()
		list, err := client.NodeContainers(ctx, node)
		return NodeRefreshMsg{Node: node, Containers: list, Err: err}
	}
}

func (v *NodeView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, c := range v.all {
		if !v.showAll && c.State == "exited" {
			continue
		}
		if !f.Empty() && !f.MatchesAny(c.Namespace, c.Pod, c.Name, c.State, c.Image) {
			continue
		}
		rows = append(rows, table.Row{
			c.Namespace,
			c.Pod,
			c.Name,
			style.StateStyle(c.State).Render(c.State),
			strconv.Itoa(c.Attempt),
			c.Image,
			since(c.Created),
		})
		v.visible = append(v.visible, c)
	}
	setTableRows(&v.table, rows)
}
