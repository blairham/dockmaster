package tui

import (
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// kubeContainers is a daemon a runtime's built-in Kubernetes runs on, as
// Rancher Desktop's is: a pod's pause container and its app container,
// labeled as cri-dockerd labels them, and one container of the user's.
func kubeContainers() []docker.Container {
	pod := func(id, name, typ string) docker.Container {
		return docker.Container{
			ID: id, Name: "k8s_" + name + "_coredns-7cfb7bc9c7-bvvz2_kube-system_39a1_1", Image: "sha256:7efd3c63",
			State: "running", Labels: map[string]string{
				"io.kubernetes.pod.name":       "coredns-7cfb7bc9c7-bvvz2",
				"io.kubernetes.pod.namespace":  "kube-system",
				"io.kubernetes.container.name": name,
				"io.kubernetes.docker.type":    typ,
			},
		}
	}
	return []docker.Container{
		pod("aaaa1111aaaa1111", "POD", "podsandbox"),
		pod("bbbb2222bbbb2222", "coredns", "container"),
		{ID: "cccc3333cccc3333", Name: "mine", Image: "nginx:1.27", State: "running"},
	}
}

func TestKubeRef(t *testing.T) {
	cs := kubeContainers()
	if k, ok := cs[0].Kube(); !ok || !k.Sandbox || k.Pod != "coredns-7cfb7bc9c7-bvvz2" || k.Namespace != "kube-system" {
		t.Errorf("pause container: %+v %v", k, ok)
	}
	if k, ok := cs[1].Kube(); !ok || k.Sandbox || k.Container != "coredns" {
		t.Errorf("app container: %+v %v", k, ok)
	}
	if _, ok := cs[2].Kube(); ok {
		t.Error("a plain container read as Kubernetes")
	}
}

// TestKubeContainersHiddenThenShown: Kubernetes's containers stay out of
// the list, the title says how many; ctrl+k lists them by container and
// pod, and a pod's pause container never.
func TestKubeContainersHiddenThenShown(t *testing.T) {
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: kubeContainers()})
	out := render(a)
	if strings.Contains(out, "coredns") || !strings.Contains(out, "mine") || !strings.Contains(out, "+1 kube hidden") {
		t.Fatalf("default list:\n%s", out)
	}

	step(a, key("ctrl+k"))
	out = render(a)
	if !strings.Contains(out, "coredns coredns-7cfb7bc9c7") || strings.Contains(out, "k8s_") || strings.Contains(out, "POD") {
		t.Errorf("shown:\n%s", out)
	}
	if strings.Contains(out, "kube hidden") || a.flash != "Kubernetes containers shown" {
		t.Errorf("title or flash after ctrl+k (flash %q):\n%s", a.flash, out)
	}

	a.filter = "kube-system"
	a.setActiveFilter("kube-system")
	if out := render(a); !strings.Contains(out, "coredns") || strings.Contains(out, "mine") {
		t.Errorf("filtering by namespace:\n%s", out)
	}
}

// TestKubeToggleOnAnEmptyList: a daemon running nothing but Kubernetes
// lists no rows by default, and ctrl+k must still reach them.
func TestKubeToggleOnAnEmptyList(t *testing.T) {
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: kubeContainers()[:2]})
	if out := render(a); !strings.Contains(out, "only Kubernetes's containers here (1) — press <ctrl-k>") {
		t.Errorf("the empty list does not say where the containers went:\n%s", out)
	}
	step(a, key("ctrl+k"))
	if !strings.Contains(render(a), "coredns") {
		t.Errorf("ctrl+k on an empty-looking list:\n%s", render(a))
	}
}
