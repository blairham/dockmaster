// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeRegistryAPI is a registry's v2 API with one repository, app, tagged
// v1, recording each DELETE.
func fakeRegistryAPI(t *testing.T) (port string, deleted func() []string) {
	t.Helper()
	var (
		mu  sync.Mutex
		del []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/_catalog":
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []string{"app"}})
		case r.URL.Path == "/v2/app/tags/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"tags": []string{"v1"}})
		case r.URL.Path == "/v2/app/manifests/v1" && r.Method == http.MethodGet:
			w.Header().Set("Docker-Content-Digest", "sha256:0123456789abcdef0123")
			_, _ = w.Write([]byte(`{"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
				`"config":{"digest":"sha256:cfg","size":24},"layers":[{"size":2048}]}`))
		case r.URL.Path == "/v2/app/blobs/sha256:cfg":
			_, _ = w.Write([]byte(`{"created":"2026-01-01T00:00:00Z"}`))
		case r.Method == http.MethodDelete:
			mu.Lock()
			del = append(del, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	_, port, _ = strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	return port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), del...)
	}
}

// fakeLocalRegistries stands in for the daemon's registry containers and
// the calls on them; it returns the calls made.
func fakeLocalRegistries(t *testing.T, regs []docker.LocalRegistry) *[]string {
	t.Helper()
	fakeRegistries(t, nil)
	calls := &[]string{}
	oldList, oldUp, oldStop, oldRm, oldGC := views.ListLocalRegistries, localRegistryUpFn, registryStopFn,
		registryRemoveFn, registryGCFn
	views.ListLocalRegistries = func(context.Context, *docker.Client) ([]docker.LocalRegistry, error) {
		return regs, nil
	}
	localRegistryUpFn = func(_ context.Context, _ *docker.Client, spec docker.Registry) (
		docker.LocalRegistry, bool, error,
	) {
		*calls = append(*calls, "up "+spec.Name+" "+spec.Port+" "+spec.Volume)
		return docker.LocalRegistry{Name: spec.Name, Port: spec.Port, Kind: docker.RegistryKindDockmaster}, false, nil
	}
	registryStopFn = func(_ context.Context, _ *docker.Client, name string) error {
		*calls = append(*calls, "stop "+name)
		return nil
	}
	registryRemoveFn = func(_ context.Context, _ *docker.Client, name string) error {
		*calls = append(*calls, "rm "+name)
		return nil
	}
	registryGCFn = func(_ context.Context, _ *docker.Client, name string) (int, error) {
		*calls = append(*calls, "gc "+name)
		return 3, nil
	}
	t.Cleanup(func() {
		views.ListLocalRegistries, localRegistryUpFn, registryStopFn, registryRemoveFn, registryGCFn =
			oldList, oldUp, oldStop, oldRm, oldGC
	})
	return calls
}

// TestRegistriesListLocal: the registry containers on the daemon lead the
// list, with their address, state and kind; a config entry for the same
// address is folded into its row.
func TestRegistriesListLocal(t *testing.T) {
	fakeLocalRegistries(t, []docker.LocalRegistry{
		{Name: "dockmaster-registry", Kind: docker.RegistryKindDockmaster, Port: "5000", Running: true},
		{Name: "kind-registry", Kind: docker.RegistryKindKind, Port: "5001"},
	})
	a := newTestApp(t)
	_, cmd := a.dispatchCommand("registries")
	runCmd(a, cmd)
	out := render(a)
	for _, want := range []string{
		"localhost:5000", "dockmaster-registry", "running", "dockmaster registry",
		"localhost:5001", "kind-registry", "stopped", "kind registry", "ghcr.io",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("registries view is missing %q:\n%s", want, out)
		}
	}
	rv := typedView[*views.RegistriesView](a, style.ViewRegistries)
	if r, ok := rv.Selected(); !ok || r.Container() != "dockmaster-registry" {
		t.Errorf("first row %+v", r)
	}
}

// TestRegistryUp: :registry up runs config's registry — its name, port and
// volume — and says where it is; --readonly refuses it.
func TestRegistryUp(t *testing.T) {
	calls := fakeLocalRegistries(t, nil)
	a := optsApp(t, Options{LocalRegistryName: "reg", LocalRegistryPort: 5055})
	_, cmd := a.dispatchCommand("registries")
	runCmd(a, cmd)
	_, cmd = a.dispatchCommand("registry up")
	runCmd(a, cmd)
	if len(*calls) != 1 || (*calls)[0] != "up reg 5055 reg-data" {
		t.Fatalf("calls %v (err %q)", *calls, a.errFlash)
	}
	if !strings.Contains(a.flash, "local registry up at localhost:5055") || !strings.Contains(a.flash, "created") {
		t.Errorf("flash %q", a.flash)
	}
	a.readonly = true
	_, cmd = a.dispatchCommand("registry up")
	runCmd(a, cmd)
	if len(*calls) != 1 || !strings.Contains(a.errFlash, "readonly mode") {
		t.Errorf("readonly: calls %v err %q", *calls, a.errFlash)
	}
}

// TestRegistryContainerKeys: on a registry container x stops it, ctrl-d
// removes dockmaster's own after a confirm and refuses a cluster's, and P
// garbage-collects after a confirm.
func TestRegistryContainerKeys(t *testing.T) {
	calls := fakeLocalRegistries(t, []docker.LocalRegistry{
		{Name: "dockmaster-registry", Kind: docker.RegistryKindDockmaster, Port: "5000", Running: true},
		{Name: "kind-registry", Kind: docker.RegistryKindKind, Port: "5001", Running: true},
	})
	a := newTestApp(t)
	_, cmd := a.dispatchCommand("registries")
	runCmd(a, cmd)
	runCmd(a, step(a, key("x")))
	step(a, key("ctrl+d"))
	if !a.confirm.Active() ||
		!strings.Contains(a.confirm.Prompt(), "remove the registry container dockmaster-registry?") ||
		!strings.Contains(a.confirm.Prompt(), "dockmaster-registry-data") {
		t.Fatalf("ctrl-d asked %q", a.confirm.Prompt())
	}
	runCmd(a, step(a, key("y")))
	step(a, key("P"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "garbage-collect dockmaster-registry?") {
		t.Fatalf("P asked %q", a.confirm.Prompt())
	}
	runCmd(a, step(a, key("y")))
	if want := "stop dockmaster-registry,rm dockmaster-registry,gc dockmaster-registry"; strings.Join(
		*calls,
		",",
	) != want {
		t.Errorf("calls %v, want %s", *calls, want)
	}
	if !strings.Contains(a.flash, "3 blobs deleted") {
		t.Errorf("gc flash %q", a.flash)
	}
	step(a, key("down"))
	step(a, key("ctrl+d"))
	if a.confirm.Active() || !strings.Contains(a.errFlash, "kind's registry, not dockmaster's") {
		t.Errorf("ctrl-d on kind's registry: confirm %v err %q", a.confirm.Active(), a.errFlash)
	}
}

// TestRegistryBrowse: enter on a registry lists its repositories from the
// v2 API, enter on one lists its tags with digest and size, and ctrl-d on
// a tag deletes its manifest by digest after a confirm.
func TestRegistryBrowse(t *testing.T) {
	port, deleted := fakeRegistryAPI(t)
	fakeLocalRegistries(t, []docker.LocalRegistry{
		{Name: "dockmaster-registry", Kind: docker.RegistryKindDockmaster, Port: port, Running: true},
	})
	a := newTestApp(t)
	_, cmd := a.dispatchCommand("registries")
	runCmd(a, cmd)
	runCmd(a, step(a, key("enter")))
	if a.view != style.ViewRegistryCatalog || !strings.Contains(render(a), "app") {
		t.Fatalf("enter opened %s:\n%s", style.ViewName(a.view), render(a))
	}
	runCmd(a, step(a, key("enter")))
	out := render(a)
	if a.view != style.ViewRegistryTags {
		t.Fatalf("enter on a repository opened %s:\n%s", style.ViewName(a.view), out)
	}
	for _, want := range []string{"v1", "sha256:0123456789ab", "2.07kB"} {
		if !strings.Contains(out, want) {
			t.Errorf("tags view is missing %q:\n%s", want, out)
		}
	}
	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "delete localhost:"+port+"/app:v1") {
		t.Fatalf("ctrl-d asked %q (err %q)", a.confirm.Prompt(), a.errFlash)
	}
	if len(deleted()) != 0 {
		t.Fatal("deleted before the confirm was answered")
	}
	runCmd(a, step(a, key("y")))
	if d := deleted(); len(d) != 1 || d[0] != "/v2/app/manifests/sha256:0123456789abcdef0123" {
		t.Errorf("deleted %v (err %q)", d, a.errFlash)
	}
}

// TestCatalogCommand: :catalog opens the running local registry, and says
// how to start one when there is none.
func TestCatalogCommand(t *testing.T) {
	fakeLocalRegistries(t, nil)
	a := newTestApp(t)
	_, cmd := a.dispatchCommand("catalog")
	runCmd(a, cmd)
	if a.view == style.ViewRegistryCatalog || !strings.Contains(a.errFlash, ":registry up") {
		t.Errorf("no registry: view %s err %q", style.ViewName(a.view), a.errFlash)
	}
	port, _ := fakeRegistryAPI(t)
	views.ListLocalRegistries = func(context.Context, *docker.Client) ([]docker.LocalRegistry, error) {
		return []docker.LocalRegistry{{Name: "r", Port: port, Running: true}}, nil
	}
	_, cmd = a.dispatchCommand("catalog")
	runCmd(a, cmd)
	if a.view != style.ViewRegistryCatalog || !strings.Contains(render(a), "app") {
		t.Errorf(":catalog opened %s:\n%s", style.ViewName(a.view), render(a))
	}
}
