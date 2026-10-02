package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

func kindNodes() []docker.Container {
	now := time.Now().Add(-18 * time.Hour)
	return []docker.Container{
		{
			ID: "node0000worker", Name: "k8s-worker", Image: "kindest/node:v1.35.0", State: "running",
			Created: now, Labels: map[string]string{"io.x-k8s.kind.role": "worker", "io.x-k8s.kind.cluster": "k8s"},
		},
		{ID: "web000000000000", Name: "nginx", Image: "nginx:1.27", State: "running", Created: now},
	}
}

func rigContainers() []docker.NodeContainer {
	return []docker.NodeContainer{
		{
			ID: "432f5e11b60d", Name: "sxbet-rest-oegw", Pod: "sxbet-rest-oegw-79fdbc5c9d-4bnrj-x-trading-x-rig",
			Namespace: "k5s-legate", State: "running", Image: "legate-sxbet-rest-oegw:a286feffbc32", Created: time.Now(),
		},
		{ID: "aaaa", Name: "coredns", Pod: "coredns-1", Namespace: "kube-system", State: "running", Image: "coredns:v1.12.0"},
	}
}

// TestKindNodeDrillIn: a kind cluster's pods run in the node's own
// containerd, so docker ps shows only the node. c on the node lists what
// is inside it; enter is logs on every row, the node included.
func TestKindNodeDrillIn(t *testing.T) {
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: kindNodes()})
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	for c, _ := cv.Selected(); c.Name != "k8s-worker"; c, _ = cv.Selected() {
		step(a, key("j"))
	}

	step(a, key("enter"))
	if a.view != style.ViewLogs {
		t.Fatalf("enter on a kind node opened %v, want its logs", a.view)
	}
	step(a, key("esc"))
	step(a, key("c"))
	if a.view != style.ViewNode {
		t.Fatalf("c on a kind node opened %v, want the node view", a.view)
	}
	nv := typedView[*views.NodeView](a, style.ViewNode)
	if nv.Node() != "node0000worker" || nv.Title() != "k8s-worker" {
		t.Errorf("node view for %q titled %q", nv.Node(), nv.Title())
	}
	step(a, views.NodeRefreshMsg{Node: "node0000worker", Containers: rigContainers()})
	out := render(a)
	for _, want := range []string{"sxbet-rest-oegw", "k5s-legate", "coredns", "kube-system", "NAMESPACE", "POD"} {
		if !strings.Contains(out, want) {
			t.Errorf("node view is missing %q", want)
		}
	}
	// A listing for another node must not land here.
	step(a, views.NodeRefreshMsg{Node: "someone-else", Containers: nil})
	if nv.Count() != 2 {
		t.Errorf("another node's listing replaced this one: %d rows", nv.Count())
	}

	step(a, key("o"))
	if a.view != style.ViewInspect {
		t.Errorf("o in the node view opened %v, want inspect", a.view)
	}
	step(a, key("esc"))

	step(a, key("enter"))
	lv := typedView[*views.LogsView](a, style.ViewLogs)
	if a.view != style.ViewLogs || lv.Node() != "node0000worker" || lv.ContainerID() != "432f5e11b60d" {
		t.Fatalf("enter in the node view: view %v, logs of %q in node %q", a.view, lv.ContainerID(), lv.Node())
	}
	// Stop and restart would hand the daemon a containerd ID: not offered.
	for _, k := range []string{"x", "R"} {
		if act, _ := lv.HandleKey(k); act != "" {
			t.Errorf("%s in a node container's logs = %q, want nothing", k, act)
		}
	}
	step(a, key("esc"))
	step(a, key("esc"))
	if a.view != style.ViewContainers {
		t.Fatalf("esc did not unwind to containers: %v", a.view)
	}

	for c, _ := cv.Selected(); c.Name != "nginx"; c, _ = cv.Selected() {
		step(a, key("j"))
	}
	step(a, key("c"))
	if a.view != style.ViewContainers || !strings.Contains(a.errFlash, "not a kind/k3d node") {
		t.Errorf("c on a plain container: view %v flash %q, want an explanation", a.view, a.errFlash)
	}
	step(a, key("enter"))
	if a.view != style.ViewLogs {
		t.Errorf("enter on a plain container opened %v, want logs", a.view)
	}
}

// TestNodeShellIsGatedByReadonly: a shell in a node's container is an exec
// like any other.
func TestNodeShellIsGatedByReadonly(t *testing.T) {
	a := NewApp(nil, Options{ReadOnly: true})
	a.handleAction("node_shell", views.NodeParam("node", "id", "pod/c"))
	if !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("node_shell under --readonly: flash %q", a.errFlash)
	}
}

func nodeWithExited() []docker.NodeContainer {
	return []docker.NodeContainer{
		{ID: "run1", Name: "kube-proxy", Pod: "kube-proxy-72cjf", Namespace: "kube-system", State: "running", Attempt: 8},
		{ID: "old1", Name: "kube-proxy", Pod: "kube-proxy-72cjf", Namespace: "kube-system", State: "exited", Attempt: 7},
		{ID: "oms1", Name: "oms", Pod: "oms-76f57894b8-qj95m", Namespace: "k5s-legate", State: "exited"},
	}
}

func openNode(t *testing.T, a *App) *views.NodeView {
	t.Helper()
	a.handleAction("node_containers", views.NodeParam("node0000worker", "", "k8s-worker"))
	step(a, views.NodeRefreshMsg{Node: "node0000worker", Containers: nodeWithExited()})
	return typedView[*views.NodeView](a, style.ViewNode)
}

// TestNodeViewHidesExitedUntilA: exited containers are mostly a running
// one's previous attempt; they are hidden until a, as docker ps hides
// stopped containers until -a.
func TestNodeViewHidesExitedUntilA(t *testing.T) {
	a := newTestApp(t)
	nv := openNode(t, a)
	if nv.Count() != 1 || strings.Contains(render(a), "oms") {
		t.Fatalf("exited containers shown by default: %d rows", nv.Count())
	}
	step(a, key("a"))
	if nv.Count() != 3 || !strings.Contains(render(a), "oms") {
		t.Errorf("a did not show the exited containers: %d rows", nv.Count())
	}
	step(a, key("a"))
	if nv.Count() != 1 {
		t.Errorf("a again did not hide them: %d rows", nv.Count())
	}
}

// TestNodeRemoveOnlyExited: ctrl-d removes an exited container after a
// confirm; on a running one it refuses, because the kubelet owns it.
func TestNodeRemoveOnlyExited(t *testing.T) {
	a := newTestApp(t)
	nv := openNode(t, a)

	step(a, key("ctrl+d")) // the running kube-proxy
	if a.confirm.Active() || !strings.Contains(a.errFlash, "kubelet") {
		t.Fatalf("ctrl-d on a running container: confirm=%v flash=%q", a.confirm.Active(), a.errFlash)
	}

	step(a, key("a"))
	selectNodeRow(t, a, nv, "oms1")
	step(a, key("ctrl+d"))
	if !a.confirm.Active() {
		t.Fatal("ctrl-d on an exited container did not ask first")
	}
	if !strings.Contains(render(a), "remove exited container oms-76f57894b8-qj95m/oms?") {
		t.Errorf("confirm question is missing the container:\n%s", render(a))
	}
	step(a, key("n"))
	if a.confirm.Active() {
		t.Error("n did not close the confirm")
	}

	ro := NewApp(nil, Options{ReadOnly: true})
	ro.handleAction("confirm_node_remove", views.NodeParam("n", "oms1", "p/oms"))
	if ro.confirm.Active() || !strings.Contains(ro.errFlash, "readonly") {
		t.Errorf("--readonly let a node remove through: confirm=%v flash=%q", ro.confirm.Active(), ro.errFlash)
	}
}

// selectNodeRow moves the cursor to the container with id, failing rather
// than looping when the row is not listed.
func selectNodeRow(t *testing.T, a *App, nv *views.NodeView, id string) {
	t.Helper()
	for range nv.Count() {
		if c, _ := nv.Selected(); c.ID == id {
			return
		}
		step(a, key("j"))
	}
	t.Fatalf("container %s is not in the node view", id)
}

// TestHealthKeyOpensTheReport: H on a container opens its health report,
// shown as text rather than run through the JSON colorizer.
func TestHealthKeyOpensTheReport(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	step(a, key("H"))
	if a.view != style.ViewInspect {
		t.Fatalf("H opened %v, want the health report", a.view)
	}
	iv := typedView[*views.InspectView](a, style.ViewInspect)
	if iv.Title() != "web health" {
		t.Errorf("title = %q", iv.Title())
	}
	// Probe output from an HTTP health endpoint is often JSON-ish: a
	// `"key": value` line is exactly what the colorizer would restyle.
	body := `  Status        unhealthy` + "\n" + `      "error": "db down"`
	step(a, views.InspectRefreshMsg{Kind: views.InspectContainer, Body: []byte(body)})
	if !strings.Contains(renderStyled(a), `"error": "db down"`) {
		t.Errorf("probe output was restyled as JSON:\n%s", renderStyled(a))
	}
}

// keyAt finds where key's shortcut is drawn in the header: line and column.
func keyAt(header, key string) (int, int) {
	for i, l := range strings.Split(header, "\n") {
		if j := strings.Index(l, key); j >= 0 {
			return i, j
		}
	}
	return -1, -1
}

// TestShortcutsHoldStillAcrossRows: the bar is the same whichever row is
// selected — enter is logs everywhere, and c (a node's containers) lives in
// help rather than appearing on node rows: the bar is sorted, so any entry
// coming and going would reflow every key after it.
func TestShortcutsHoldStillAcrossRows(t *testing.T) {
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: kindNodes()})
	header := func() string {
		lines := strings.Split(render(a), "\n")
		return strings.Join(lines[:min(8, len(lines))], "\n")
	}
	step(a, key("g"))
	node := header() // k8s-worker
	step(a, key("j"))
	plain := header() // nginx

	if node != plain {
		t.Errorf("the bar changed with the selected row:\nnode:\n%s\nplain:\n%s", node, plain)
	}
	// c is in help, not the bar: the bar holds the common actions, as k9s's.
	if strings.Contains(node, "<c>") {
		t.Errorf("the bar lists c:\n%s", node)
	}
	step(a, key("?"))
	if !strings.Contains(render(a), "Node containers ⎈") {
		t.Error("help does not list c")
	}
	step(a, key("esc"))
	for _, k := range []string{"<enter>", "<o>", "<H>", "<s>", "<shift-f>", "<ctrl-d>"} {
		nl, nc := keyAt(node, k)
		pl, pc := keyAt(plain, k)
		if nl < 0 || nl != pl || nc != pc {
			t.Errorf("%s moved: line %d col %d on a node, line %d col %d otherwise", k, nl, nc, pl, pc)
		}
	}
	if !regexp.MustCompile(`<enter> +Logs`).MatchString(node) {
		t.Errorf("enter is not Logs on a node row:\n%s", node)
	}
}

// TestNodeRowsAreMarked: a node is marked in the NAME column, so the
// different enter is visible before the row is selected.
func TestNodeRowsAreMarked(t *testing.T) {
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: kindNodes()})
	out := render(a)
	if !strings.Contains(out, "⎈ k8s-worker") {
		t.Errorf("node row is not marked:\n%s", out)
	}
	if strings.Contains(out, "⎈ nginx") || !strings.Contains(out, "nginx") {
		t.Errorf("plain container is marked, or missing:\n%s", out)
	}
	assertFrameFits(t, a, "containers with a marked node")
}
