// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/tree"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// XrayRefreshMsg carries the containers the tree is built from.
type XrayRefreshMsg struct {
	Err        error
	Containers []docker.Container
}

// xrayTarget is what enter and o do on one node.
type xrayTarget struct {
	enter, enterParam     string
	inspect, inspectParam string
}

// XrayView is k9s's xray for Docker (#9): every compose project, its
// services, their containers, and what each container uses — its image,
// volumes and networks — as one tree; standalone containers under their own
// root. It is built from one `docker ps -a`, so it refreshes on the poll,
// and the tree keeps what is open and where the cursor is across refreshes.
type XrayView struct {
	tree     *tree.Model
	client   *docker.Client
	err      error
	targets  map[string]xrayTarget
	inFlight bool
	loading  bool
}

// NewXrayView builds the tree, opened all the way down as k9s's xray is.
func NewXrayView(client *docker.Client) *XrayView {
	t := tree.New(Theme())
	t.SetDefaultDepth(-1)
	return &XrayView{client: client, tree: t, loading: true, targets: map[string]xrayTarget{}}
}

// Title names the view in the border.
func (v *XrayView) Title() string { return "xray" }

// Init loads the tree.
func (v *XrayView) Init() tea.Cmd { return v.Refresh() }

// Refresh re-lists the containers, single-flight like every list view.
func (v *XrayView) Refresh() tea.Cmd {
	if v.inFlight || v.client == nil {
		return nil
	}
	v.inFlight = true
	c := v.client
	return func() tea.Msg {
		ctx, cancel := c.RequestContext(20 * time.Second)
		defer cancel()
		cs, err := c.Containers(ctx, true)
		return XrayRefreshMsg{Containers: cs, Err: err}
	}
}

// Update rebuilds the tree from a listing.
func (v *XrayView) Update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(XrayRefreshMsg)
	if !ok {
		return nil
	}
	v.inFlight, v.loading, v.err = false, false, m.Err
	if m.Err == nil {
		v.targets = map[string]xrayTarget{}
		v.tree.SetRoots(v.build(m.Containers))
	}
	return nil
}

// build is the tree: projects, then services, then containers and what each
// uses, everything in name order so a refresh does not reshuffle it.
func (v *XrayView) build(cs []docker.Container) []*tree.Node {
	projects := map[string]map[string][]docker.Container{}
	var standalone []docker.Container
	for _, c := range cs {
		if c.Project == "" {
			standalone = append(standalone, c)
			continue
		}
		if projects[c.Project] == nil {
			projects[c.Project] = map[string][]docker.Container{}
		}
		projects[c.Project][c.Service] = append(projects[c.Project][c.Service], c)
	}

	var roots []*tree.Node
	for _, p := range sortedKeys(projects) {
		pid := "p:" + p
		v.targets[pid] = xrayTarget{enter: "project_containers", enterParam: p}
		pn := &tree.Node{ID: pid, Label: style.Title.Render("⎔ " + p)}
		for _, s := range sortedKeys(projects[p]) {
			sid := pid + "/s:" + s
			v.targets[sid] = xrayTarget{enter: "project_containers", enterParam: p}
			sn := &tree.Node{ID: sid, Label: "◇ " + s}
			for _, c := range sortedContainers(projects[p][s]) {
				sn.Children = append(sn.Children, v.containerNode(c))
			}
			pn.Children = append(pn.Children, sn)
		}
		roots = append(roots, pn)
	}
	if len(standalone) > 0 {
		root := &tree.Node{ID: "standalone", Label: style.Muted.Render("standalone containers")}
		for _, c := range sortedContainers(standalone) {
			root.Children = append(root.Children, v.containerNode(c))
		}
		roots = append(roots, root)
	}
	return roots
}

// containerNode is a container and what it uses.
func (v *XrayView) containerNode(c docker.Container) *tree.Node {
	cid := "c:" + c.ID
	v.targets[cid] = xrayTarget{enter: "logs", enterParam: c.ID, inspect: "inspect_container", inspectParam: c.ID}
	n := &tree.Node{ID: cid, Label: fmt.Sprintf("▣ %s %s", c.Name, stateStyle(c.State).Render(c.State))}

	if c.Image != "" {
		iid := cid + "/i"
		image := c.ImageID
		if image == "" {
			image = c.Image
		}
		v.targets[iid] = xrayTarget{enter: "layers", enterParam: image, inspect: "inspect_image", inspectParam: image}
		n.Children = append(n.Children, &tree.Node{ID: iid, Label: style.Muted.Render("image ") + c.Image})
	}
	for _, vol := range c.Volumes {
		vid := cid + "/v:" + vol
		v.targets[vid] = xrayTarget{enter: "browse_volume", enterParam: vol, inspect: "inspect_volume", inspectParam: vol}
		n.Children = append(n.Children, &tree.Node{ID: vid, Label: style.Muted.Render("volume ") + vol})
	}
	for _, e := range c.Endpoints {
		nid := cid + "/n:" + e.Network
		label := style.Muted.Render("network ") + e.Network
		if e.IP != "" {
			label += style.Muted.Render(" " + e.IP)
		}
		v.targets[nid] = xrayTarget{enter: "used_by", enterParam: UsedByParam("network", e.Network, e.Network)}
		n.Children = append(n.Children, &tree.Node{ID: nid, Label: label})
	}
	return n
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedContainers(cs []docker.Container) []docker.Container {
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
	return cs
}

// HandleKey maps a key: the tree's own (space, h/l, ←/→) open and close nodes;
// enter acts on the node — a container's logs, an image's layers, a
// volume's files, the containers on a network, a project's containers — and
// o inspects it.
func (v *XrayView) HandleKey(key string) (string, string) {
	switch key {
	case tree.KeyToggle, "h", "l", "left", "right":
		v.tree.HandleKey(key)
		return "xray_nav", ""
	}
	n, ok := v.tree.Selected()
	if !ok {
		return "", ""
	}
	t := v.targets[n.ID]
	switch key {
	case KeyEnter:
		return t.enter, t.enterParam
	case "o":
		return t.inspect, t.inspectParam
	}
	return "", ""
}

// Selected is the node under the cursor.
func (v *XrayView) Selected() (*tree.Node, bool) { return v.tree.Selected() }

// UpdateTable moves the cursor: the app has already turned j/k, g/G and
// the page keys into arrows and pages.
func (v *XrayView) UpdateTable(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		v.tree.HandleKey(k.String())
	}
	return nil
}

// Resize re-lays the tree.
func (v *XrayView) Resize(width, height int) { v.tree.Resize(width, height) }

// Count is the rows on screen.
func (v *XrayView) Count() int { return v.tree.VisibleCount() }

// Loading reports whether the first listing is outstanding.
func (v *XrayView) Loading() bool { return v.loading }

// SetFilter filters the tree: a node shows when it or anything under it
// matches, with the path to it open.
func (v *XrayView) SetFilter(f string) { v.tree.SetFilter(f) }

// View renders the tree.
func (v *XrayView) View() string {
	if v.err != nil {
		return style.Error.Render("  " + docker.FormatUserError(v.err).Error())
	}
	if v.tree.Count() == 0 && !v.loading {
		return style.Muted.Render("  no containers")
	}
	return v.tree.View()
}

// stateStyle colors a container state as the containers view does.
func stateStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return style.StateRunning
	case "paused":
		return style.StatePaused
	case "restarting":
		return style.StateRestarting
	case "created":
		return style.StateCreated
	case "dead":
		return style.StateDead
	default:
		return style.StateExited
	}
}
