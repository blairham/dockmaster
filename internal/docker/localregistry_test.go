// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"slices"
	"testing"

	"github.com/moby/moby/api/types/mount"
)

// registryDaemon is a daemon holding dockmaster's registry (stopped, so
// its port is only in HostConfig), kind's (running) and an unrelated
// container.
func registryDaemon(t *testing.T) *Client {
	t.Helper()
	c, _ := fakeDaemon(t, map[string]any{
		"/containers/json": []map[string]any{
			{"Id": "web", "Names": []string{"/web"}, "Image": "nginx", "State": "running"},
			{"Id": "kr", "Names": []string{"/kind-registry"}, "Image": "registry:2", "State": "running"},
			{
				"Id": "dm", "Names": []string{"/dockmaster-registry"}, "Image": "registry:2", "State": "exited",
				"Labels": map[string]string{LocalRegistryLabel: "local"},
			},
		},
		"/containers/kr/json": map[string]any{
			"Id": "kr", "Config": map[string]any{"Env": []string{"PATH=/bin"}},
			"NetworkSettings": map[string]any{"Ports": map[string]any{
				"5000/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "5001"}},
			}},
		},
		"/containers/dm/json": map[string]any{
			"Id": "dm", "Config": map[string]any{"Env": []string{deleteEnabledEnv}},
			"HostConfig": map[string]any{"PortBindings": map[string]any{
				"5000/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "5000"}},
			}},
		},
		"/containers/dm/start": map[string]any{},
	}, nil)
	return c
}

// TestLocalRegistries: every registry container, dockmaster's first, with
// the port it publishes — from HostConfig when it is stopped — and
// whether it allows deletes; other containers are not registries.
func TestLocalRegistries(t *testing.T) {
	regs, err := registryDaemon(t).LocalRegistries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []LocalRegistry{
		{Name: "dockmaster-registry", ID: "dm", Kind: RegistryKindDockmaster, Port: "5000", DeleteEnabled: true},
		{Name: "kind-registry", ID: "kr", Kind: RegistryKindKind, Port: "5001", Running: true},
	}
	if !slices.Equal(regs, want) {
		t.Errorf("registries\n got %+v\nwant %+v", regs, want)
	}
	if regs[0].Address() != "localhost:5000" || !regs[0].Managed() || regs[1].Managed() {
		t.Errorf("address %q managed %v %v", regs[0].Address(), regs[0].Managed(), regs[1].Managed())
	}
}

// TestUpLocalRegistryReuses: an existing registry is started, never a
// second one created — and with no dockmaster registry, a cluster's that
// publishes a port is the one reused.
func TestUpLocalRegistryReuses(t *testing.T) {
	r, reused, err := registryDaemon(t).UpLocalRegistry(context.Background(),
		LocalRegistrySpec("dockmaster-registry", "5000"))
	if err != nil || !reused || r.Name != "dockmaster-registry" || !r.Running {
		t.Errorf("up = %+v reused %v err %v", r, reused, err)
	}
	regs := []LocalRegistry{
		{Name: "k3d-reg", Kind: RegistryKindK3d},
		{Name: "kind-registry", Kind: RegistryKindKind, Port: "5001"},
	}
	if got, ok := pickLocalRegistry(regs, "mine"); !ok || got.Name != "kind-registry" {
		t.Errorf("picked %+v %v, want the one with a port", got, ok)
	}
	if _, ok := pickLocalRegistry(regs[:1], "mine"); ok {
		t.Error("picked a registry that publishes no port")
	}
}

// TestLocalRegistrySpecCreate: dockmaster's registry keeps its storage in
// a named volume, carries its label and allows deletes, on loopback only;
// kind's keeps the recipe's bare shape.
func TestLocalRegistrySpecCreate(t *testing.T) {
	opts := registryCreateOptions(LocalRegistrySpec("reg", "5050"))
	if opts.Name != "reg" || opts.Config.Labels[LocalRegistryLabel] != "local" ||
		!slices.Contains(opts.Config.Env, deleteEnabledEnv) {
		t.Errorf("config %+v", opts.Config)
	}
	want := []mount.Mount{{Type: mount.TypeVolume, Source: "reg-data", Target: "/var/lib/registry"}}
	if !slices.Equal(opts.HostConfig.Mounts, want) {
		t.Errorf("mounts %+v", opts.HostConfig.Mounts)
	}
	b := opts.HostConfig.PortBindings[registryPort]
	if len(b) != 1 || b[0].HostPort != "5050" || b[0].HostIP.String() != "127.0.0.1" {
		t.Errorf("bindings %+v", b)
	}
	if opts.HostConfig.RestartPolicy.Name != "unless-stopped" {
		t.Errorf("restart %q", opts.HostConfig.RestartPolicy.Name)
	}
	kind := registryCreateOptions(DefaultRegistry)
	if kind.HostConfig.Mounts != nil || kind.Config.Labels != nil || kind.Config.Env != nil {
		t.Errorf("kind's registry grew extras: %+v %+v", kind.Config, kind.HostConfig.Mounts)
	}
}

// TestCountCollected: the blobs a garbage-collect run deleted.
func TestCountCollected(t *testing.T) {
	out := "app\napp: marking manifest sha256:1\nblob eligible for deletion: sha256:a\n" +
		"blob eligible for deletion: sha256:b\nfailed to garbage collect: nothing"
	if n := CountCollected(out); n != 2 {
		t.Errorf("counted %d", n)
	}
}
