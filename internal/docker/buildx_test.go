// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestParseBuilders reads real `docker buildx ls --format json` output
// (buildx v0.37.2 on a Colima host, paths and names scrubbed): a
// docker-container builder in error, a stopped kubernetes builder, the
// current context's docker builder, and contexts whose daemons are down.
func TestParseBuilders(t *testing.T) {
	out, err := os.ReadFile("testdata/buildx-ls.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	bs, err := ParseBuilders(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 7 {
		t.Fatalf("parsed %d builders, want 7", len(bs))
	}
	type want struct {
		name, driver, status, endpoint, buildkit string
		current, contextBuilder, hasErr          bool
	}
	wants := []want{
		{
			name: "ci-builder", driver: "docker-container", status: "error",
			endpoint: "unix:///var/run/docker.sock", hasErr: true,
		},
		{
			name: "k8s-remote", driver: "kubernetes", status: "stopped",
			endpoint: "kubernetes:///k8s-remote?deployment=k8s-remote&kubeconfig=%2FUsers%2Fme%2F.kube%2Fdev.kubeconfig",
		},
		{
			name: "colima", driver: "docker", status: "running", endpoint: "colima", buildkit: "v0.30.0",
			current: true, contextBuilder: true,
		},
		{name: "default", status: "error", contextBuilder: true, hasErr: true},
	}
	for i, w := range wants {
		b := bs[i]
		got := want{
			name: b.Name, driver: b.Driver, status: b.Status(), endpoint: b.Endpoint(), buildkit: b.BuildKit(),
			current: b.Current, contextBuilder: b.ContextBuilder(), hasErr: b.Reason() != "",
		}
		if got != w {
			t.Errorf("builder %d:\n got %+v\nwant %+v", i, got, w)
		}
	}
	if p := bs[2].Platforms(); !slices.Equal(p, []string{"linux/arm64", "linux/amd64", "linux/amd64/v2", "linux/386"}) {
		t.Errorf("colima platforms %v", p)
	}
	if bs[2].LastActivity.IsZero() {
		t.Error("LastActivity not parsed")
	}
}

// TestBuilderNodesSummarized: a builder with two nodes shows the first's
// endpoint and version with how many more, every platform once, and the
// worst status.
func TestBuilderNodesSummarized(t *testing.T) {
	b := BuildxBuilder{Name: "multi", Driver: "docker-container", Nodes: []BuildxNode{
		{Name: "a", Endpoint: "ctx-a", Status: "running", Version: "v0.30.0", Platforms: []string{"linux/amd64"}},
		{Name: "b", Endpoint: "ctx-b", Status: "inactive", Platforms: []string{"linux/arm64", "linux/amd64"}},
	}}
	if got := b.Endpoint(); got != "ctx-a (+1)" {
		t.Errorf("Endpoint %q", got)
	}
	if got := b.BuildKit(); got != "v0.30.0 (+1)" {
		t.Errorf("BuildKit %q", got)
	}
	if got := b.Status(); got != "inactive" {
		t.Errorf("Status %q", got)
	}
	if got := b.Platforms(); !slices.Equal(got, []string{"linux/amd64", "linux/arm64"}) {
		t.Errorf("Platforms %v", got)
	}
	b.Nodes[1].Err = "boom"
	if b.Status() != "error" || b.Reason() != "boom" {
		t.Errorf("a failing node: status %q, err %q", b.Status(), b.Reason())
	}
}

// recordRunner records each argv and answers from out.
func recordRunner(out string, err error) (CLIRunner, *[][]string) {
	var calls [][]string
	return func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return []byte(out), err
	}, &calls
}

// TestBuildxArgv: every verb goes to the daemon on screen (the endpoint
// flags first) and puts the builder's name after --, so a name is never
// read as a flag.
func TestBuildxArgv(t *testing.T) {
	ep := []string{"--host", "unix:///tmp/d.sock"}
	var calls [][]string
	b := NewBuildxWithRunner(func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if slices.Contains(args, "prune") {
			return []byte("ID\tRECLAIMABLE\nTotal:\t1.2GB\n"), nil
		}
		return nil, nil
	}, ep)
	ctx := context.Background()
	if _, err := b.Builders(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Inspect(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []func(context.Context, string) error{b.Start, b.Stop, b.Remove, b.Use} {
		if err := f(ctx, "x"); err != nil {
			t.Fatal(err)
		}
	}
	total, err := b.Prune(ctx, "x")
	if err != nil || total != "Total:\t1.2GB" {
		t.Errorf("prune %q, %v", total, err)
	}
	want := []string{
		"ls --format=json",
		"inspect -- x",
		"inspect --bootstrap -- x",
		"stop -- x",
		"rm -- x",
		"use -- x",
		"prune --builder=x --force",
	}
	if len(calls) != len(want) {
		t.Fatalf("%d calls, want %d", len(calls), len(want))
	}
	for i, c := range calls {
		got := strings.Join(c, " ")
		if w := "--host unix:///tmp/d.sock buildx " + want[i]; got != w {
			t.Errorf("call %d:\n got %s\nwant %s", i, got, w)
		}
	}
}

// TestBuildxErrorNamesTheVerb: a failure says which buildx verb failed.
func TestBuildxErrorNamesTheVerb(t *testing.T) {
	run, _ := recordRunner("", errors.New("no builder \"x\" found"))
	err := NewBuildxWithRunner(run, nil).Stop(context.Background(), "x")
	if err == nil || err.Error() != `buildx stop: no builder "x" found` {
		t.Errorf("err %v", err)
	}
}

// TestBuilderSpecCheck: what buildx would refuse, or read as a flag, is
// refused before anything runs; the argv keeps each value after =.
func TestBuilderSpecCheck(t *testing.T) {
	cases := []struct {
		spec BuilderSpec
		want string // "" is accepted
	}{
		{spec: BuilderSpec{Driver: "docker-container"}},
		{spec: BuilderSpec{Name: "ci.1_x-y", Driver: "kubernetes", Platform: "linux/amd64, linux/arm64"}},
		{spec: BuilderSpec{Name: "r", Driver: "remote", Endpoint: "tcp://buildkitd:1234"}},
		{spec: BuilderSpec{Name: "-rm", Driver: "docker-container"}, want: "name:"},
		{spec: BuilderSpec{Name: "a b", Driver: "docker-container"}, want: "name:"},
		{spec: BuilderSpec{Driver: "docker"}, want: "driver:"},
		{spec: BuilderSpec{Driver: "docker-container", Platform: "--all"}, want: "platform:"},
		{spec: BuilderSpec{Driver: "remote"}, want: "endpoint: the remote driver"},
		{spec: BuilderSpec{Driver: "docker-container", Endpoint: "tcp://x:1"}, want: "endpoint: only the remote"},
		{spec: BuilderSpec{Driver: "remote", Endpoint: "-x"}, want: "endpoint:"},
	}
	for _, c := range cases {
		err := c.spec.Check()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%+v refused: %v", c.spec, err)
		case c.want != "" && (err == nil || !strings.HasPrefix(err.Error(), c.want)):
			t.Errorf("%+v: err %v, want %q", c.spec, err, c.want)
		}
	}
	got := strings.Join(BuilderSpec{
		Name: "ci", Driver: "remote", Platform: "linux/amd64, linux/arm64", Endpoint: "tcp://b:1234",
	}.CreateArgs(), " ")
	if want := "--driver=remote --name=ci --platform=linux/amd64,linux/arm64 -- tcp://b:1234"; got != want {
		t.Errorf("CreateArgs %q, want %q", got, want)
	}
}

// TestBuildxCreate returns the name buildx printed, and never runs a spec
// it would refuse.
func TestBuildxCreate(t *testing.T) {
	run, calls := recordRunner("eloquent_turing\n", nil)
	b := NewBuildxWithRunner(run, []string{"--context", "tls"})
	name, err := b.Create(context.Background(), BuilderSpec{Driver: "docker-container"})
	if err != nil || name != "eloquent_turing" {
		t.Errorf("Create %q, %v", name, err)
	}
	if _, err := b.Create(context.Background(), BuilderSpec{Driver: "docker"}); err == nil {
		t.Error("a docker-driver builder was not refused")
	}
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != "--context tls buildx create --driver=docker-container" {
		t.Errorf("calls %q", *calls)
	}
}
