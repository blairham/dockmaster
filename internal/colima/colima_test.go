package colima

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeRunner records every invocation and answers from a table keyed by
// the joined argv, so no test here ever starts a VM.
type fakeRunner struct {
	answers map[string]fakeAnswer
	calls   []string
}

type fakeAnswer struct {
	err error
	out string
}

func (f *fakeRunner) run(_ context.Context, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	a, ok := f.answers[key]
	if !ok {
		return nil, errors.New("unexpected colima " + key)
	}
	return []byte(a.out), a.err
}

// TestListParsesOneObjectPerLine pins the shape colima 0.10 prints: NDJSON,
// not an array. A decoder expecting an array parses nothing and reports no
// profiles, which reads exactly like a machine with none.
func TestListParsesOneObjectPerLine(t *testing.T) {
	f := &fakeRunner{answers: map[string]fakeAnswer{
		"list --json": {
			out: `{"name":"default","status":"Running","arch":"aarch64","cpus":8,"memory":17179869184,"disk":107374182400,"runtime":"docker"}
{"name":"work","status":"Stopped","arch":"x86_64","cpus":2,"memory":2147483648,"disk":64424509440,"runtime":"containerd"}
`,
		},
	}}
	c := NewWithRunner(f.run, "/h/.colima")
	got, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Profile{
		{
			Name: "default", Status: "Running", Arch: "aarch64", Runtime: "docker",
			CPUs: 8, Memory: 17179869184, Disk: 107374182400,
		},
		{
			Name: "work", Status: "Stopped", Arch: "x86_64", Runtime: "containerd",
			CPUs: 2, Memory: 2147483648, Disk: 64424509440,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List =\n%+v\nwant\n%+v", got, want)
	}
	if !got[0].Running() || got[1].Running() {
		t.Errorf("Running: default %v, work %v", got[0].Running(), got[1].Running())
	}
}

func TestListEmptyAndMalformed(t *testing.T) {
	f := &fakeRunner{answers: map[string]fakeAnswer{"list --json": {out: "\n"}}}
	got, err := NewWithRunner(f.run, "/h").List(context.Background())
	if err != nil || len(got) != 0 {
		t.Errorf("empty list: %v, %v", got, err)
	}

	f.answers["list --json"] = fakeAnswer{out: "not json\n"}
	if _, err := NewWithRunner(f.run, "/h").List(context.Background()); err == nil {
		t.Error("malformed list parsed without error")
	}
}

// TestVerbsPassTheProfile pins each verb's argv. Delete must carry --force:
// colima's own y/n prompt has no terminal to ask on and would hang the
// command until the timeout.
func TestVerbsPassTheProfile(t *testing.T) {
	f := &fakeRunner{answers: map[string]fakeAnswer{
		"start work": {}, "stop work": {}, "restart work": {}, "delete --force work": {},
	}}
	c := NewWithRunner(f.run, "/h")
	ctx := context.Background()
	for _, fn := range []func(context.Context, string) error{c.Start, c.Stop, c.Restart, c.Delete} {
		if err := fn(ctx, "work"); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"start work", "stop work", "restart work", "delete --force work"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %q, want %q", f.calls, want)
	}
}

func TestFatalMessage(t *testing.T) {
	for _, tc := range []struct {
		name, stderr, want string
	}{
		{
			name:   "logrus fatal",
			stderr: `time="2026-09-30T21:02:30-04:00" level=fatal msg="colima [profile=x] is not running"` + "\n",
			want:   "colima [profile=x] is not running",
		},
		{
			name: "last msg wins",
			stderr: `time="t" level=info msg="starting colima"` + "\n" +
				`time="t" level=fatal msg="error starting vm: exit status 1"` + "\n",
			want: "error starting vm: exit status 1",
		},
		{
			name:   "escaped quotes",
			stderr: `time="t" level=fatal msg="profile \"x\" not found"`,
			want:   `profile "x" not found`,
		},
		{name: "plain text", stderr: "something\nwent wrong\n", want: "went wrong"},
	} {
		if got := FatalMessage(tc.stderr); got != tc.want {
			t.Errorf("%s: FatalMessage = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestErrorUsesTheFatalMessage(t *testing.T) {
	err := &Error{
		Args:   []string{"stop", "x"},
		Stderr: `time="t" level=fatal msg="colima [profile=x] is not running"`,
		Err:    errors.New("exit status 1"),
	}
	if got := err.Error(); got != "colima: colima [profile=x] is not running" {
		t.Errorf("Error() = %q", got)
	}
}

func TestDockerHostAndContextName(t *testing.T) {
	c := NewWithRunner(nil, "/Users/u/.colima")
	if got := c.DockerHost("default"); got != "unix:///Users/u/.colima/default/docker.sock" {
		t.Errorf("DockerHost(default) = %q", got)
	}
	if got := ContextName("default"); got != "colima" {
		t.Errorf("ContextName(default) = %q", got)
	}
	if got := ContextName("work"); got != "colima-work" {
		t.Errorf("ContextName(work) = %q", got)
	}
}

// TestProfileForHost is the inverse of DockerHost, and the check that lets
// dockmaster offer to start a stopped VM rather than just failing to dial.
func TestProfileForHost(t *testing.T) {
	const home = "/Users/u/.colima"
	for _, tc := range []struct {
		host, want string
		ok         bool
	}{
		{host: "unix:///Users/u/.colima/default/docker.sock", want: "default", ok: true},
		{host: "unix:///Users/u/.colima/work/docker.sock", want: "work", ok: true},
		{host: "unix:///Users/u/.colima/work/containerd.sock"},
		{host: "unix:///Users/u/.colima/docker.sock"},
		{host: "unix:///Users/u/.colima/_lima/docker.sock"},
		{host: "unix:///var/run/docker.sock"},
		{host: "tcp://10.0.0.1:2376"},
		{host: "unix:///Users/u/.colima-other/default/docker.sock"},
	} {
		got, ok := ProfileForHost(home, tc.host)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ProfileForHost(%q) = %q, %v; want %q, %v", tc.host, got, ok, tc.want, tc.ok)
		}
	}
	c := NewWithRunner(nil, home)
	if p, ok := c.ProfileForHost(c.DockerHost("work")); !ok || p != "work" {
		t.Errorf("round trip: %q, %v", p, ok)
	}
}

func TestConfigArgs(t *testing.T) {
	got := Config{CPUs: 4, MemoryGiB: 8, DiskGiB: 60, Runtime: "containerd"}.Args()
	want := []string{"--cpus", "4", "--memory", "8", "--disk", "60", "--runtime", "containerd"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args = %q, want %q", got, want)
	}
	if got := (Config{MemoryGiB: 1.5}).Args(); !reflect.DeepEqual(got, []string{"--memory", "1.5"}) {
		t.Errorf("fractional memory: %q", got)
	}
	if got := (Config{}).Args(); len(got) != 0 {
		t.Errorf("zero config renders flags: %q", got)
	}
}

func TestStartWithPassesFlags(t *testing.T) {
	f := &fakeRunner{answers: map[string]fakeAnswer{"start work --cpus 2 --memory 4 --disk 30": {}}}
	err := NewWithRunner(f.run, "/h").StartWith(context.Background(), "work",
		Config{CPUs: 2, MemoryGiB: 4, DiskGiB: 30})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidName(t *testing.T) {
	existing := []Profile{{Name: "default"}, {Name: "Work"}}
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{name: "dev", ok: true},
		{name: "dev-2_x", ok: true},
		{name: ""},
		{name: "-dev"},
		{name: "dev box"},
		{name: "a/b"},
		{name: "default"},
		{name: "work"},
	} {
		if msg := ValidName(tc.name, existing); (msg == "") != tc.ok {
			t.Errorf("ValidName(%q) = %q, want ok=%v", tc.name, msg, tc.ok)
		}
	}
}
