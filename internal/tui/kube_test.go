// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeClusterOps is a daemon for K: it holds clusters and records what it
// was asked to do, in order.
type fakeClusterOps struct {
	clusters []docker.Cluster
	calls    []string
}

func (f *fakeClusterOps) Clusters(context.Context) ([]docker.Cluster, error) { return f.clusters, nil }

func (f *fakeClusterOps) SetClusterRunning(_ context.Context, cl docker.Cluster, on bool) error {
	f.calls = append(f.calls, "running "+cl.Name+" "+onOff(on)+" "+strings.Join(cl.Nodes, ","))
	return nil
}

func (f *fakeClusterOps) SetRegistryRunning(_ context.Context, name string, on bool) error {
	f.calls = append(f.calls, "registry-running "+name+" "+onOff(on))
	return nil
}

func (f *fakeClusterOps) EnsureRegistry(_ context.Context, r docker.Registry) error {
	f.calls = append(f.calls, "registry "+r.Name+" :"+r.Port)
	return nil
}

func (f *fakeClusterOps) WireRegistry(_ context.Context, cl docker.Cluster, _ docker.Registry) error {
	f.calls = append(f.calls, "wire "+strings.Join(cl.Nodes, ","))
	return nil
}

func (f *fakeClusterOps) ConnectRegistry(_ context.Context, r docker.Registry, network string) error {
	f.calls = append(f.calls, "connect "+r.Name+" "+network)
	return nil
}

func (f *fakeClusterOps) Close() error { return nil }

// kubeHarness puts the runtimes view on a fake colima whose default VM has
// the given kind clusters, and answers kind and kubeconfig.
func kubeHarness(
	t *testing.T,
	existing []docker.Cluster,
	contexts []string,
) (*App, *fakeColima, *fakeClusterOps, *[]string) {
	t.Helper()
	ops := &fakeClusterOps{clusters: existing}
	var kindCalls []string
	origDial, origKind, origKC, origProbe := dialCluster, kindRun, kubeconfigs, views.ClusterProbe
	dialCluster = func(string) (clusterOps, error) { return ops, nil }
	kindRun = func(_ context.Context, env []string, _ string, args ...string) ([]byte, error) {
		kindCalls = append(kindCalls, strings.Join(env, " ")+" kind "+strings.Join(args, " "))
		ops.clusters = append(ops.clusters, docker.Cluster{
			Tool: "kind", Name: args[3], Nodes: []string{args[3] + "-control-plane", args[3] + "-worker"}, Running: true,
		})
		return nil, nil
	}
	kubeconfigs = func() ([]string, error) { return contexts, nil }
	views.ClusterProbe = func(_ context.Context, host string) []engines.Cluster {
		if !strings.Contains(host, "/default/") {
			return nil
		}
		out := make([]engines.Cluster, 0, len(ops.clusters))
		for _, c := range ops.clusters {
			out = append(out, engines.Cluster{Tool: c.Tool, Name: c.Name, Registry: c.Registry, Running: c.Running})
		}
		return out
	}
	t.Cleanup(func() { dialCluster, kindRun, kubeconfigs, views.ClusterProbe = origDial, origKind, origKC, origProbe })
	a, f := newColimaApp(t, Options{})
	runCmd(a, a.refreshActiveView())
	return a, f, ops, &kindCalls
}

func defaultRow(a *App) string {
	for _, l := range strings.Split(render(a), "\n") {
		if strings.Contains(l, " default ") {
			return strings.Join(strings.Fields(l), " ")
		}
	}
	return ""
}

// TestKubeStopsAndStartsAnExistingCluster: on a VM already running a kind
// cluster, K stops its nodes; on a stopped one, starts them. The runtime's
// own Kubernetes is never touched.
func TestKubeStopsAndStartsAnExistingCluster(t *testing.T) {
	k8s := docker.Cluster{
		Tool: "kind", Name: "k8s", Nodes: []string{"k8s-control-plane", "k8s-worker"}, Running: true,
		Registry: "kind-registry", RegistryRunning: true,
	}
	a, f, ops, kindCalls := kubeHarness(t, []docker.Cluster{k8s}, []string{"kind-k8s"})
	if !strings.Contains(defaultRow(a), "docker kind") {
		t.Fatalf("K8S column: %q", defaultRow(a))
	}
	step(a, key("K"))
	if !strings.Contains(a.confirm.Prompt(), "stop kind k8s in colima default? its pods stop, and kind-registry with it") {
		t.Fatalf("K on a running cluster: %q", a.confirm.Prompt())
	}
	runCmd(a, step(a, key("y")))
	if strings.Join(ops.calls, "|") != "running k8s off k8s-control-plane,k8s-worker|registry-running kind-registry off" {
		t.Errorf("stop did %v", ops.calls)
	}

	// Colors are checked with the cursor on another row: the selected row
	// is drawn in the selection style, without them.
	step(a, key("j"))
	if styled := renderStyled(a); !strings.Contains(styled, style.Success.Render("kind")) {
		t.Error("a running cluster's kind is not drawn bright")
	}
	ops.clusters[0].Running, ops.clusters[0].RegistryRunning = false, false
	ops.calls = nil
	runCmd(a, a.runtimesView().Refresh())
	if styled := renderStyled(a); !strings.Contains(styled, style.Muted.Render("kind")) ||
		strings.Contains(styled, style.Success.Render("kind")) {
		t.Error("a stopped cluster's kind is not drawn dim")
	}
	step(a, key("k"))
	step(a, key("K"))
	if !strings.Contains(render(a), "start kind k8s in colima default, with kind-registry?") {
		t.Fatalf("K on a stopped cluster:\n%s", render(a))
	}
	runCmd(a, step(a, key("y")))
	// The registry first: a node coming up may pull from it.
	if strings.Join(ops.calls, "|") != "registry-running kind-registry on|running k8s on k8s-control-plane,k8s-worker" {
		t.Errorf("start did %v", ops.calls)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "kubernetes ") {
			t.Errorf("colima's own Kubernetes was touched: %q", c)
		}
	}
	if len(*kindCalls) != 0 {
		t.Errorf("kind ran: %v", *kindCalls)
	}
}

// TestKubeCreatesTheUsersSetup: with no cluster, K creates the setup a kind
// user has — registry, then the cluster against this VM's daemon, then the
// registry wired into the nodes and joined to the kind network.
func TestKubeCreatesTheUsersSetup(t *testing.T) {
	a, _, ops, kindCalls := kubeHarness(t, nil, []string{"colima"})
	step(a, key("K"))
	q := render(a)
	if !strings.Contains(q, "create kind cluster k8s in colima default?") ||
		!strings.Contains(q, "kubectl switches to kind-k8s") {
		t.Fatalf("K with no cluster:\n%s", q)
	}
	runCmd(a, step(a, key("y")))
	if len(*kindCalls) != 1 ||
		(*kindCalls)[0] != "DOCKER_HOST=unix:///h/.colima/default/docker.sock kind create cluster --name k8s --config -" {
		t.Fatalf("kind ran %v", *kindCalls)
	}
	want := "registry kind-registry :5001|wire k8s-control-plane,k8s-worker|connect kind-registry kind"
	if strings.Join(ops.calls, "|") != want {
		t.Errorf("create did %v\nwant %s", ops.calls, want)
	}
}

// TestKubeNamesAroundAnotherMachinesContext: kind-k8s already belongs to
// another machine's cluster, so this one is k8s-default.
func TestKubeNamesAroundAnotherMachinesContext(t *testing.T) {
	a, _, _, kindCalls := kubeHarness(t, nil, []string{"kind-k8s"})
	step(a, key("K"))
	runCmd(a, step(a, key("y")))
	if len(*kindCalls) != 1 || !strings.Contains((*kindCalls)[0], "--name k8s-default") {
		t.Errorf("kind ran %v", *kindCalls)
	}
}

// TestKubeRefusals: a machine whose own Kubernetes is on, or one that is
// stopped, gets the reason instead of a confirm.
func TestKubeRefusals(t *testing.T) {
	a, f, ops, _ := kubeHarness(t, nil, nil)
	f.mu.Lock()
	f.k8s = map[string]bool{"default": true}
	f.mu.Unlock()
	runCmd(a, a.runtimesView().Refresh())
	if !strings.Contains(defaultRow(a), "docker own") {
		t.Errorf("own k8s not shown: %q", defaultRow(a))
	}
	step(a, key("K"))
	if a.confirm.Active() || !strings.Contains(a.errFlash, "colima's own Kubernetes is on") {
		t.Errorf("own k8s on: confirm %v flash %q", a.confirm.Active(), a.errFlash)
	}
	step(a, key("j")) // work, stopped
	step(a, key("K"))
	if a.confirm.Active() || !strings.Contains(a.errFlash, "work is stopped") {
		t.Errorf("stopped machine: confirm %v flash %q", a.confirm.Active(), a.errFlash)
	}
	if len(ops.calls) != 0 {
		t.Errorf("a refusal reached the daemon: %v", ops.calls)
	}
}

// TestKubeKeepsASharedRegistry: kind clusters on a daemon share the
// registry, so stopping one while another runs leaves it up, and says so.
func TestKubeKeepsASharedRegistry(t *testing.T) {
	cluster := func(name string) docker.Cluster {
		return docker.Cluster{
			Tool: "kind", Name: name, Nodes: []string{name + "-control-plane"}, Running: true,
			Registry: "kind-registry", RegistryRunning: true,
		}
	}
	a, _, ops, _ := kubeHarness(t, []docker.Cluster{cluster("k8s"), cluster("other")}, nil)
	step(a, key("K"))
	if !strings.Contains(a.confirm.Prompt(), "kind-registry stays — kind other uses it") {
		t.Fatalf("K with a second cluster: %q", a.confirm.Prompt())
	}
	runCmd(a, step(a, key("y")))
	for _, c := range ops.calls {
		if strings.HasPrefix(c, "registry-running") {
			t.Errorf("stopped the registry another cluster uses: %v", ops.calls)
		}
	}
}

// TestRegistryIsPartOfTheCluster: in the containers list kind-registry is
// marked like the nodes, c on it explains what it is, and the node view
// says where the registry is and whether it is up.
func TestRegistryIsPartOfTheCluster(t *testing.T) {
	a := newTestApp(t)
	nodes := kindNodes()
	nodes[1] = docker.Container{
		ID: "reg000000000000", Name: "kind-registry", Image: "registry:2", State: "running",
		PortList:  []docker.PortMapping{{Type: "tcp", Private: 5000, Public: 5001}},
		Endpoints: []docker.Endpoint{{Network: "kind"}},
	}
	step(a, views.ContainersRefreshMsg{Containers: nodes})
	if out := render(a); !strings.Contains(out, "⎈ kind-registry") {
		t.Errorf("the registry is not marked:\n%s", out)
	}
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	for c, _ := cv.Selected(); c.Name != "kind-registry"; c, _ = cv.Selected() {
		step(a, key("j"))
	}
	step(a, key("n"))
	if a.view != style.ViewContainers || !strings.Contains(a.errFlash, "local registry, not a node") {
		t.Errorf("n on the registry: view %v flash %q", a.view, a.errFlash)
	}

	step(a, key("g"))
	step(a, key("n"))
	if a.view != style.ViewNode {
		t.Fatalf("n on the node opened %v", a.view)
	}
	step(a, views.NodeRefreshMsg{Node: "node0000worker", Containers: rigContainers()})
	if out := render(a); !strings.Contains(out, "registry kind-registry · push to localhost:5001 · running") {
		t.Errorf("node view lacks the registry line:\n%s", out)
	}
	assertFrameFits(t, a, "node view with a registry line")
}
