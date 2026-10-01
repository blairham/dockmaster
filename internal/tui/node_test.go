package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
	"github.com/blairham/dockyard/internal/tui/views"
)

func kindNodes() []docker.Container {
	now := time.Now().Add(-18 * time.Hour)
	return []docker.Container{
		{
			ID: "node0000worker", Name: "k8s-worker", Image: "kindest/node:v1.35.0", State: "running",
			Created: now, Labels: map[string]string{"io.x-k8s.kind.role": "worker", "io.x-k8s.kind.cluster": "k8s"},
		},
		{ID: "reg000000000000", Name: "kind-registry", Image: "registry:2", State: "running", Created: now},
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
// containerd, so docker ps shows only the node. enter on the node lists
// what is inside it; on any other container enter is still logs.
func TestKindNodeDrillIn(t *testing.T) {
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: kindNodes()})
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	for c, _ := cv.Selected(); c.Name != "k8s-worker"; c, _ = cv.Selected() {
		step(a, key("j"))
	}

	step(a, key("enter"))
	if a.view != style.ViewNode {
		t.Fatalf("enter on a kind node opened %v, want the node view", a.view)
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

	for c, _ := cv.Selected(); c.Name != "kind-registry"; c, _ = cv.Selected() {
		step(a, key("j"))
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
