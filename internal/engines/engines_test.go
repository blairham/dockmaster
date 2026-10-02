// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package engines

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/colima"
)

// fake answers commands from a table keyed by the joined argv and records
// every call, so no test here starts a VM or an app.
type fake struct {
	out   map[string]string
	fail  map[string]bool
	calls []string
}

func (f *fake) run(_ context.Context, bin string, args ...string) ([]byte, error) {
	key := strings.Join(append([]string{bin}, args...), " ")
	f.calls = append(f.calls, key)
	if f.fail[key] {
		return nil, errors.New(bin + ": failed")
	}
	return []byte(f.out[key]), nil
}

func TestPodmanList(t *testing.T) {
	f := &fake{out: map[string]string{
		"podman machine list --format json": `[
			{"Name":"podman-machine-default","Running":true,"VMType":"applehv","CPUs":4,"Memory":"4294967296","DiskSize":"107374182400"},
			{"Name":"old","Running":false,"Starting":false,"VMType":"qemu","CPUs":2,"Memory":2147483648,"DiskSize":10737418240}
		]`,
		"podman machine inspect podman-machine-default": `[{"ConnectionInfo":{"PodmanSocket":{"Path":"/var/folders/x/podman/podman-machine-default-api.sock"}}}]`,
		"podman machine inspect old":                    `[{"Rootful":true,"ConnectionInfo":{"PodmanSocket":{"Path":"/var/folders/x/podman/old-api.sock"}}}]`,
	}}
	got, err := Podman{Run: f.run}.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Machine{
		{
			Provider: PodmanName, Name: "podman-machine-default", Status: "Running", Running: true, Arch: "applehv",
			Runtime: "podman", CPUs: 4, Memory: 4294967296, Disk: 107374182400,
			Host: "unix:///var/folders/x/podman/podman-machine-default-api.sock",
		},
		{
			Provider: PodmanName, Name: "old", Status: "Stopped", Arch: "qemu", Runtime: "podman",
			CPUs: 2, Memory: 2147483648, Disk: 10737418240, Rootful: true,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List =\n%+v\nwant\n%+v", got, want)
	}
	// A stopped machine is inspected for its modes but serves no socket.
	if got[1].Host != "" {
		t.Errorf("stopped machine got a socket: %q", got[1].Host)
	}
}

// TestPodmanModes reads rootful and user-mode networking from inspect.
func TestPodmanModes(t *testing.T) {
	f := &fake{out: map[string]string{
		"podman machine list --format json": `[{"Name":"r","Running":false}]`,
		"podman machine inspect r":          `[{"Rootful":true,"UserModeNetworking":true}]`,
	}}
	ms, err := Podman{Run: f.run}.List(context.Background())
	if err != nil || len(ms) != 1 || !ms[0].Rootful || !ms[0].UserNet {
		t.Errorf("modes = %+v %v", ms, err)
	}
}

// TestPodmanRestart uses the native verb, and falls back to stop+start on
// a podman that does not have it.
func TestPodmanRestart(t *testing.T) {
	f := &fake{}
	_ = Podman{Run: f.run}.Restart(context.Background(), "m")
	if !reflect.DeepEqual(f.calls, []string{"podman machine restart m"}) {
		t.Errorf("native = %q", f.calls)
	}
	old := &fake{fail: map[string]bool{"podman machine restart m": true}}
	oldRun := func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		out, err := old.run(ctx, bin, args...)
		if err != nil {
			return nil, errors.New(`podman: Error: unrecognized command "podman machine restart"`)
		}
		return out, nil
	}
	_ = Podman{Run: oldRun}.Restart(context.Background(), "m")
	if !reflect.DeepEqual(
		old.calls,
		[]string{"podman machine restart m", "podman machine stop m", "podman machine start m"},
	) {
		t.Errorf("fallback = %q", old.calls)
	}
}

func TestPodmanModeFlags(t *testing.T) {
	yes, no := true, false
	got := podmanResourceArgs(Config{Rootful: &yes, UserNet: &no})
	if !reflect.DeepEqual(got, []string{"--rootful=true", "--user-mode-networking=false"}) {
		t.Errorf("flags = %q", got)
	}
}

// TestPodmanVerbs pins argv, including podman's units: memory in MiB.
func TestPodmanVerbs(t *testing.T) {
	f := &fake{out: map[string]string{
		"podman machine list --format json": `[{"Name":"m","Running":true,"CPUs":2,"Memory":"2147483648","DiskSize":"53687091200"}]`,
	}}
	p := Podman{Run: f.run}
	ctx := context.Background()
	cfg := Config{CPUs: 4, MemoryGiB: 6, DiskGiB: 80}
	_ = p.Start(ctx, "m")
	_ = p.Stop(ctx, "m")
	_ = p.Delete(ctx, "m")
	_ = p.Create(ctx, "new", cfg)
	f.calls = append(f.calls, "--")
	_ = p.Edit(ctx, "m", cfg, true)
	f.calls = append(f.calls, "--")
	_ = p.Edit(ctx, "m", Config{CPUs: 2, MemoryGiB: 3, DiskGiB: 50}, false) // disk unchanged
	var got []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "podman machine list") && !strings.HasPrefix(c, "podman machine inspect") {
			got = append(got, c)
		}
	}
	f.calls = got
	want := []string{
		"podman machine start m", "podman machine stop m", "podman machine rm -f m",
		"podman machine init --cpus 4 --memory 6144 --disk-size 80 --now new", "--",
		"podman machine stop m", "podman machine set --cpus 4 --memory 6144 --disk-size 80 m", "podman machine start m", "--",
		// podman rejects a disk size that does not grow, so an unchanged one is left out
		"podman machine set --cpus 2 --memory 3072 m", "podman machine start m",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls =\n%q\nwant\n%q", f.calls, want)
	}
	if got := (Podman{}).ShellCommand("m"); !reflect.DeepEqual(got, []string{"podman", "machine", "ssh", "m"}) {
		t.Errorf("ShellCommand = %q", got)
	}
}

// TestSingleStatusIsThePing: a single-engine runtime is running exactly
// when its daemon answers on its endpoint.
func TestSingleStatusIsThePing(t *testing.T) {
	for _, up := range []bool{true, false} {
		s := Single{
			Engine: OrbStackName, Context: "orbstack", Host: "unix:///o.sock",
			Ping: func(_ context.Context, host string) bool { return up && host == "unix:///o.sock" },
		}
		ms, err := s.List(context.Background())
		if err != nil || len(ms) != 1 || ms[0].Running != up || ms[0].Name != OrbStackName || ms[0].Context != "orbstack" {
			t.Errorf("up=%v: %+v %v", up, ms, err)
		}
	}
	s := Single{}
	if err := s.Delete(context.Background(), "x"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Delete = %v", err)
	}
}

func names(ps []Provider) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name())
	}
	return out
}

// TestDetect: what is installed decides what is offered, a leftover context
// alone offers nothing, and each app is driven by its CLI when it has one.
func TestDetect(t *testing.T) {
	ctx := context.Background()
	base := func(bins, apps []string, f *fake) Env {
		return Env{
			Run: f.run, Home: "/h",
			LookPath: func(b string) (string, error) {
				for _, x := range bins {
					if x == b {
						return "/bin/" + b, nil
					}
				}
				return "", errors.New("absent")
			},
			AppExists: func(n string) bool {
				for _, a := range apps {
					if a == n {
						return true
					}
				}
				return false
			},
			ContextHost: func(n string) (string, bool) {
				if n == "desktop-linux" {
					return "unix:///h/.docker/run/docker.sock", true
				}
				return "", false
			},
		}
	}

	// This machine: colima and the docker CLI, plus a desktop-linux context
	// Docker Desktop left behind — but no Docker Desktop.
	f := &fake{fail: map[string]bool{"docker desktop version": true}}
	env := base([]string{"docker", "colima"}, nil, f)
	env.Colima = colima.NewWithRunner(nil, "/h/.colima")
	if got := names(Detect(ctx, env)); !reflect.DeepEqual(got, []string{ColimaName}) {
		t.Errorf("colima only = %q", got)
	}

	// Everything, Docker Desktop through its plugin, Rancher through rdctl,
	// OrbStack through its app.
	f = &fake{}
	env = base([]string{"docker", "podman", "rdctl"}, []string{"OrbStack"}, f)
	ps := Detect(ctx, env)
	if got := names(
		ps,
	); !reflect.DeepEqual(
		got,
		[]string{PodmanName, DockerDesktopName, RancherDesktopName, OrbStackName},
	) {
		t.Errorf("all = %q", got)
	}
	dd := asSingle(t, ps[1])
	if !reflect.DeepEqual(dd.StartC, []string{"docker", "desktop", "start"}) ||
		dd.Host != "unix:///h/.docker/run/docker.sock" {
		t.Errorf("docker desktop = %+v", dd)
	}
	if rd := asSingle(t, ps[2]); !reflect.DeepEqual(rd.StopC, []string{"rdctl", "shutdown"}) ||
		rd.Host != "unix:///h/.rd/docker.sock" {
		t.Errorf("rancher = %+v", rd)
	}
	if orb := asSingle(t, ps[3]); !reflect.DeepEqual(orb.StartC, []string{"open", "-a", "OrbStack"}) {
		t.Errorf("orbstack = %+v", orb)
	}

	// This machine again, with Docker Desktop installed: a Homebrew docker
	// CLI does not find the plugin inside Docker.app, so it is run directly.
	const plugin = "/Applications/Docker.app/Contents/Resources/cli-plugins/docker-desktop"
	f = &fake{fail: map[string]bool{"docker desktop version": true}}
	env = base([]string{"docker"}, []string{"Docker"}, f)
	env.FileExists = func(p string) bool { return p == plugin }
	ps = Detect(ctx, env)
	if len(ps) != 1 || ps[0].Name() != DockerDesktopName {
		t.Fatalf("docker desktop app = %q", names(ps))
	}
	if dd := asSingle(t, ps[0]); !reflect.DeepEqual(dd.StopC, []string{plugin, "desktop", "stop"}) {
		t.Errorf("docker desktop via bundled plugin = %+v", dd)
	}

	// Rancher Desktop installed but never launched: rdctl is not on PATH
	// until first-run setup adds ~/.rd/bin, but it ships inside the app.
	const rdctl = "/Applications/Rancher Desktop.app/Contents/Resources/resources/darwin/bin/rdctl"
	f = &fake{}
	env = base(nil, []string{"Rancher Desktop"}, f)
	env.FileExists = func(p string) bool { return p == rdctl }
	ps = Detect(ctx, env)
	if len(ps) != 1 || ps[0].Name() != RancherDesktopName {
		t.Fatalf("rancher app = %q", names(ps))
	}
	if rd := asSingle(t, ps[0]); !reflect.DeepEqual(rd.StartC, []string{rdctl, "start"}) ||
		!reflect.DeepEqual(rd.StopC, []string{rdctl, "shutdown"}) || rd.K8sStatus == nil || rd.Resources == nil {
		t.Errorf("rancher via bundled rdctl = %+v", rd)
	}
}

// TestSingleOwnStatusWinsOverPing: Docker Desktop paused by Resource Saver
// is running, even when the ping into the paused VM would time out.
func TestSingleOwnStatusWinsOverPing(t *testing.T) {
	for _, tc := range []struct {
		out     string
		status  string
		fail    bool
		running bool
	}{
		{out: `{"Status":"running"}`, running: true, status: "Running"},
		{out: `{"Status":"paused"}`, running: true, status: "Paused"},
		{out: `{"Status":"stopped"}`, status: "Stopped"},
		{fail: true, status: "Stopped"},
	} {
		f := &fake{
			out:  map[string]string{"docker desktop status --format json": tc.out},
			fail: map[string]bool{"docker desktop status --format json": tc.fail},
		}
		s := Single{
			Run: f.run, Engine: DockerDesktopName, Host: "unix:///d.sock",
			StatusC: []string{"docker", "desktop", "status", "--format", "json"}, ParseStatus: DockerDesktopStatus,
			Ping: func(context.Context, string) bool { return false }, // a ping that timed out
		}
		ms, _ := s.List(context.Background())
		if ms[0].Running != tc.running || ms[0].Status != tc.status {
			t.Errorf("%q fail=%v: %+v", tc.out, tc.fail, ms[0])
		}
	}
	if st, up, _ := OrbStackStatus([]byte("Running\n")); !up || st != "Running" {
		t.Errorf("orb status = %q %v", st, up)
	}
}

func TestPodmanPods(t *testing.T) {
	f := &fake{out: map[string]string{
		"podman machine list --format json": `[{"Name":"default","Running":true},{"Name":"off","Running":false},{"Name":"r","Running":true}]`,
		"podman machine inspect r":          `[{"Rootful":true}]`,
		"podman --connection default pod ps --format json": `[{"Id":"p1","Name":"web","Status":"Running","Created":"2026-10-01T12:00:00.123Z",
			"Containers":[{"Names":"p1-infra","Status":"running"},{"Names":"nginx","Status":"running"},{"Names":"side","Status":"exited"}]}]`,
		"podman --connection r-root pod ps --format json": `[]`,
	}}
	p := Podman{Run: f.run}
	pods, err := p.Pods(context.Background())
	if err != nil || len(pods) != 1 {
		t.Fatalf("pods = %+v %v", pods, err)
	}
	if pod := pods[0]; pod.Machine != "default" || pod.Name != "web" || pod.Running() != 2 || len(pod.Containers) != 3 ||
		pod.Created.IsZero() {
		t.Errorf("pod = %+v", pod)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "connection off") {
			t.Error("asked a stopped machine for pods")
		}
	}
	f.calls = nil
	_ = p.PodVerb(context.Background(), "r", "web", "rm")
	if f.calls[len(f.calls)-1] != "podman --connection r-root pod rm -f web" {
		t.Errorf("rootful rm = %q", f.calls)
	}
}

// TestSingleSocketGoneSkipsStatus: with its socket off disk the engine is
// stopped, and neither the status command nor the ping runs — Docker
// Desktop's status retries for 16 seconds before saying it is not up.
func TestSingleSocketGoneSkipsStatus(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	for _, present := range []bool{false, true} {
		if present {
			if err := os.WriteFile(sock, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		ran, pinged := false, false
		s := Single{
			Engine: DockerDesktopName, Host: "unix://" + sock, SocketExists: socketExists,
			Run: func(context.Context, string, ...string) ([]byte, error) {
				ran = true
				return []byte(`{"Status":"running"}`), nil
			},
			StatusC: []string{"docker", "desktop", "status"}, ParseStatus: DockerDesktopStatus,
			Ping: func(context.Context, string) bool { pinged = true; return false },
		}
		ms, err := s.List(context.Background())
		if err != nil || len(ms) != 1 {
			t.Fatalf("present=%v: %+v %v", present, ms, err)
		}
		if !present && (ran || pinged || ms[0].Running || ms[0].Status != "Stopped") {
			t.Errorf("socket gone: ran=%v pinged=%v machine=%+v", ran, pinged, ms[0])
		}
		if present && (!ran || !ms[0].Running) {
			t.Errorf("socket present: status not asked (ran=%v) or not running: %+v", ran, ms[0])
		}
	}
}

func asSingle(t *testing.T, p Provider) Single {
	t.Helper()
	s, ok := p.(Single)
	if !ok {
		t.Fatalf("%s is a %T, want Single", p.Name(), p)
	}
	return s
}

func TestKubernetesParsers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parse  func([]byte) (bool, bool)
		out    string
		on, ok bool
	}{
		{name: "orb true", parse: OrbStackKubeStatus, out: "true\n", on: true, ok: true},
		{name: "orb false", parse: OrbStackKubeStatus, out: "false\n", ok: true},
		{name: "orb junk", parse: OrbStackKubeStatus, out: "error: not running"},
		{name: "rancher on", parse: RancherKubeStatus, out: `{"kubernetes":{"enabled":true,"version":"1.30"}}`, on: true, ok: true},
		{name: "rancher off", parse: RancherKubeStatus, out: `{"kubernetes":{"enabled":false}}`, ok: true},
		{name: "rancher missing", parse: RancherKubeStatus, out: `{"version":1}`},
		{name: "dd running", parse: DockerDesktopKubeStatus, out: `{"status":"running"}`, on: true, ok: true},
		{name: "dd stopped", parse: DockerDesktopKubeStatus, out: `{"Status":"stopped"}`, ok: true},
		{name: "dd unknown shape", parse: DockerDesktopKubeStatus, out: `{"state":{"x":1}}`},
	} {
		if on, ok := tc.parse([]byte(tc.out)); on != tc.on || ok != tc.ok {
			t.Errorf("%s: (%v, %v), want (%v, %v)", tc.name, on, ok, tc.on, tc.ok)
		}
	}
}

// TestRuntimeKubernetesStatus: a single-engine runtime reports its own
// built-in cluster when it is up — read only; dockmaster does not switch it.
func TestRuntimeKubernetesStatus(t *testing.T) {
	f := &fake{out: map[string]string{"orb config get k8s.enable": "true\n"}}
	orb := Single{
		Engine: OrbStackName, Run: f.run, Host: "tcp://orb",
		Ping:      func(context.Context, string) bool { return true },
		K8sStatus: kubeStatus(f.run, []string{"orb", "config", "get", "k8s.enable"}, OrbStackKubeStatus),
	}
	ms, _ := orb.List(context.Background())
	if ms[0].K8s != KubeOn {
		t.Errorf("orbstack K8s = %q", ms[0].K8s)
	}
	down := orb
	down.Ping = func(context.Context, string) bool { return false }
	if ms, _ := down.List(context.Background()); ms[0].K8s != "" {
		t.Errorf("a stopped engine reports K8s %q", ms[0].K8s)
	}
}

// TestRancherResources reads the VM's size from `rdctl list-settings` as
// Rancher Desktop 1.24 writes it.
func TestRancherResources(t *testing.T) {
	out := []byte(`{"virtualMachine":{"memoryInGB":6,"numberCPUs":2,"type":"vz"},` +
		`"experimental":{"virtualMachine":{"diskSize":"100GiB"}}}`)
	r, ok := RancherResources(out)
	if !ok || r.CPUs != 2 || r.Memory != 6<<30 || r.Disk != 100<<30 {
		t.Errorf("resources = %+v, %v", r, ok)
	}
	if _, ok := RancherResources([]byte(`{"kubernetes":{"enabled":true}}`)); ok {
		t.Error("settings with no VM size read as a size")
	}
}

// TestSingleShowsResourcesWhenUp: a running single-engine runtime's row
// carries its VM's size; a stopped one asks nothing — rdctl cannot answer
// while the app is down.
func TestSingleShowsResourcesWhenUp(t *testing.T) {
	asked := 0
	s := Single{
		Engine: RancherDesktopName, Host: "unix:///h/.rd/docker.sock",
		Ping: func(context.Context, string) bool { return true },
		Resources: func(context.Context) (Resources, bool) {
			asked++
			return Resources{CPUs: 2, Memory: 6 << 30, Disk: 100 << 30}, true
		},
	}
	ms, _ := s.List(context.Background())
	if m := ms[0]; m.CPUs != 2 || m.Memory != 6<<30 || m.Disk != 100<<30 {
		t.Errorf("running row = %+v", m)
	}
	s.Ping = func(context.Context, string) bool { return false }
	asked = 0
	ms, _ = s.List(context.Background())
	if asked != 0 || ms[0].CPUs != 0 {
		t.Errorf("stopped: asked %d times, row %+v", asked, ms[0])
	}
}
