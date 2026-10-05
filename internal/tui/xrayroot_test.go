// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// rootContainers: two containers sharing an image, a network and a volume,
// one of them on a second network, and one using nothing but its image.
func rootContainers() []docker.Container {
	return []docker.Container{
		{
			ID: "c1", Name: "shop-web-1", State: "running", Project: "shop", Service: "web",
			Image: "nginx:1.27", ImageID: "sha256:cccc", Volumes: []string{"pgdata"},
			Endpoints: []docker.Endpoint{{Network: "shop_default", IP: "172.18.0.2"}, {Network: "edge"}},
		},
		{
			ID: "c2", Name: "shop-web-2", State: "exited", Project: "shop", Service: "web",
			Image: "nginx:1.27", ImageID: "sha256:cccc", Volumes: []string{"pgdata"},
			Endpoints: []docker.Endpoint{{Network: "shop_default"}},
		},
		{ID: "c3", Name: "registry", State: "running", Image: "registry:2", ImageID: "sha256:bbbb"},
	}
}

// openXrayRoot runs :xray <word> on the fixture.
func openXrayRoot(t *testing.T, word string) (*App, *views.XrayView) {
	t.Helper()
	a := newTestApp(t)
	if msg, _ := a.dispatchCommand("xray " + word); msg != "" {
		t.Fatalf(":xray %s: %s", word, msg)
	}
	if a.view != style.ViewXray {
		t.Fatalf(":xray %s opened %v", word, a.view)
	}
	xv := typedView[*views.XrayView](a, style.ViewXray)
	step(a, views.XrayRefreshMsg{Containers: rootContainers()})
	return a, xv
}

// inOrder fails unless every want is in out, in order.
func inOrder(t *testing.T, out string, want ...string) {
	t.Helper()
	last := -1
	for _, w := range want {
		i := strings.Index(out[last+1:], w)
		if i < 0 {
			t.Fatalf("%q missing or out of order:\n%s", w, out)
		}
		last += 1 + i
	}
}

// TestXrayRoots: :xray net, vol and img grow the tree from networks,
// volumes and images — each root holding the containers using it, and each
// container what it uses — in name order, and the border says which. A
// container using none of the root's kind is not under any root.
func TestXrayRoots(t *testing.T) {
	a, xv := openXrayRoot(t, "net")
	if xv.Root() != views.XrayNetworks {
		t.Fatalf("root %q", xv.Root())
	}
	out := render(a)
	inOrder(t, out, "xray networks",
		"network edge", "shop-web-1",
		"network shop_default", "shop-web-1", "image nginx:1.27", "volume pgdata", "shop-web-2")
	if strings.Contains(out, "registry") {
		t.Errorf("a container on no network is in the networks tree:\n%s", out)
	}

	a, _ = openXrayRoot(t, "volumes")
	out = render(a)
	inOrder(t, out, "xray volumes", "volume pgdata", "shop-web-1", "shop-web-2")
	if strings.Contains(out, "registry") {
		t.Errorf("a container with no volume is in the volumes tree:\n%s", out)
	}

	a, _ = openXrayRoot(t, "img")
	inOrder(t, render(a), "xray images",
		"image nginx:1.27", "shop-web-1", "shop-web-2", "image registry:2", "registry")

	a, _ = openXrayRoot(t, "projects")
	inOrder(t, render(a), "shop", "web", "shop-web-1", "standalone containers", "registry")
}

// TestXrayRootNodesAct: enter and o on a root act on what it is — the
// containers on a network, a volume's files, an image's layers — v scans an
// image root, and the container keys stay off it.
func TestXrayRootNodesAct(t *testing.T) {
	type want struct{ action, param string }
	for _, tc := range []struct {
		keys map[string]want
		word string
		node string
	}{
		{word: "net", node: "N:shop_default", keys: map[string]want{
			"enter": {"used_by", views.UsedByParam("network", "shop_default", "shop_default")},
			"o":     {"inspect_network", "shop_default"}, "v": {}, "x": {}, "ctrl+d": {"xray_nav", ""},
		}},
		{word: "vol", node: "V:pgdata", keys: map[string]want{
			"enter": {"browse_volume", "pgdata"}, "o": {"inspect_volume", "pgdata"}, "x": {},
		}},
		{word: "img", node: "I:sha256:cccc", keys: map[string]want{
			"enter": {"layers", "sha256:cccc"}, "o": {"inspect_image", "sha256:cccc"},
			"v": {"scan_image", "sha256:cccc"}, "u": {}, "s": {},
		}},
	} {
		a, xv := openXrayRoot(t, tc.word)
		gotoNode(t, a, xv, tc.node)
		for k, w := range tc.keys {
			if act, param := xv.HandleKey(k); act != w.action || param != w.param {
				t.Errorf("%s on %s = (%q, %q), want (%q, %q)", k, tc.node, act, param, w.action, w.param)
			}
		}
	}
}

// TestXrayRootContainersKeepTheirKeys: under any root a container node
// takes the container keys (#56), and the same container under two roots
// is two nodes — the cursor can stand on either.
func TestXrayRootContainersKeepTheirKeys(t *testing.T) {
	a, xv := openXrayRoot(t, "net")
	for _, node := range []string{"N:edge/c:c1", "N:shop_default/c:c1"} {
		gotoNode(t, a, xv, node)
		for k, w := range map[string][2]string{
			"x": {"stop", "c1"}, "enter": {"logs", "c1"}, "ctrl+d": {"confirm_remove_container", "c1"},
		} {
			if act, param := xv.HandleKey(k); act != w[0] || param != w[1] {
				t.Errorf("%s on %s = (%q, %q), want %v", k, node, act, param, w)
			}
		}
	}
	gotoNode(t, a, xv, "N:shop_default/c:c2")
	step(a, key("enter"))
	if a.view != style.ViewLogs {
		t.Errorf("enter on a container under a network opened %v", a.view)
	}
}

// TestXrayRootRefreshKeepsIt: the poll's refresh grows the same root, and
// a bad root is refused, naming the roots there are.
func TestXrayRootRefreshKeepsIt(t *testing.T) {
	a, xv := openXrayRoot(t, "vol")
	if cmd := xv.Refresh(); cmd != nil {
		t.Fatal("a nil client listed")
	}
	step(a, views.XrayRefreshMsg{Containers: rootContainers()})
	if !strings.Contains(render(a), "xray volumes") || xv.Root() != views.XrayVolumes {
		t.Errorf("a refresh lost the root:\n%s", render(a))
	}
	b := newTestApp(t)
	if msg, _ := b.dispatchCommand("xray pods"); !strings.Contains(msg, "net, vol or img") || b.view == style.ViewXray {
		t.Errorf(":xray pods: %q, view %v", msg, b.view)
	}
}

// TestXrayRootCommandsValidate: -c and defaultView take :xray with a root
// it can grow from, and refuse any other word after it.
func TestXrayRootCommandsValidate(t *testing.T) {
	for _, ok := range []string{"xray", "xray net", "xray volumes", "xray img @prod", "xray proj"} {
		if err := ValidateCommand(ok, nil); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"xray pods", "xray net vol", "xray nets"} {
		if err := ValidateCommand(bad, nil); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestXrayRootInspectAndCopyKeys: through the app, i on a root inspects it
// as o does (the d/y/i fallback), and c / I copy the root's name and ID.
func TestXrayRootInspectAndCopyKeys(t *testing.T) {
	for _, tc := range []struct{ word, node, copied string }{
		{word: "net", node: "N:shop_default", copied: "copied shop_default"},
		{word: "vol", node: "V:pgdata", copied: "copied ID pgdata"},
		{word: "img", node: "I:sha256:cccc", copied: "copied ID sha256:cccc"},
	} {
		a, xv := openXrayRoot(t, tc.word)
		gotoNode(t, a, xv, tc.node)
		k := "I"
		if tc.word == "net" {
			k = "c" // a network root has a name and no ID
		}
		if cmd := step(a, key(k)); cmd == nil || a.flash != tc.copied {
			t.Errorf("%s on %s: flash %q", k, tc.node, a.flash)
		}
		step(a, key("i"))
		if a.view != style.ViewInspect {
			t.Errorf("i on %s opened %v, want inspect", tc.node, a.view)
		}
	}
}
