// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func buildContext(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"Dockerfile", "api.Dockerfile"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("FROM scratch\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestBuildCommand: buildx streams plain progress and loads the image; the
// classic builder gets neither flag; every value rides after = in its own
// argument; the endpoint flags come first, and DOCKER_HOST agrees with
// them.
func TestBuildCommand(t *testing.T) {
	dir := buildContext(t)
	spec := BuildSpec{
		Context: dir, Dockerfile: "api.Dockerfile", Tag: "app:dev", Target: "final",
		BuildArgs: []string{"VERSION=1.2", "EMPTY="},
	}
	for _, tc := range []struct {
		name     string
		env      string
		endpoint []string
		want     []string
		builder  Builder
	}{
		{
			name:     "buildx on a host",
			builder:  BuilderBuildx,
			endpoint: []string{"--host", "unix:///run/colima.sock"},
			want: []string{
				"docker", "--host", "unix:///run/colima.sock", "buildx", "build", "--progress=plain", "--load",
				"--file=" + filepath.Join(dir, "api.Dockerfile"), "--tag=app:dev", "--target=final",
				"--build-arg=VERSION=1.2", "--build-arg=EMPTY=", dir,
			},
			env: "DOCKER_HOST=unix:///run/colima.sock",
		},
		{
			name:     "classic on a TLS context",
			builder:  BuilderClassic,
			endpoint: []string{"--context", "remote"},
			want: []string{
				"docker", "--context", "remote", "build",
				"--file=" + filepath.Join(dir, "api.Dockerfile"), "--tag=app:dev", "--target=final",
				"--build-arg=VERSION=1.2", "--build-arg=EMPTY=", dir,
			},
			env: "DOCKER_HOST=",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildCommand("docker", tc.endpoint, tc.builder, spec)
			if !slices.Equal(got.Args, tc.want) {
				t.Errorf("args\n got %q\nwant %q", got.Args, tc.want)
			}
			if got.Dir != dir {
				t.Errorf("dir %q", got.Dir)
			}
			if !slices.Contains(got.Env, tc.env) || !slices.Contains(got.Env, "BUILDKIT_PROGRESS=plain") {
				t.Errorf("env %q, want %q and plain progress", got.Env, tc.env)
			}
		})
	}
	// The context's own Dockerfile needs no --file.
	plain := BuildCommand("docker", nil, BuilderBuildx, BuildSpec{Context: dir})
	if strings.Contains(strings.Join(plain.Args, " "), "--file") || plain.Args[len(plain.Args)-1] != dir {
		t.Errorf("plain build: %q", plain.Args)
	}
}

// TestBuildSpecCheck: what could never build is refused before a builder
// starts, with the field it is in.
func TestBuildSpecCheck(t *testing.T) {
	dir := buildContext(t)
	if err := (BuildSpec{Context: dir, Tag: "app:1", BuildArgs: []string{"A_1=x y"}}).Check(); err != nil {
		t.Errorf("a good spec: %v", err)
	}
	for _, tc := range []struct {
		want string
		spec BuildSpec
	}{
		{spec: BuildSpec{}, want: "context"},
		{spec: BuildSpec{Context: filepath.Join(dir, "nope")}, want: "not a directory"},
		{spec: BuildSpec{Context: dir, Dockerfile: "Missing"}, want: "dockerfile"},
		{spec: BuildSpec{Context: t.TempDir()}, want: "dockerfile"},
		{spec: BuildSpec{Context: dir, Tag: "--push"}, want: "starts with -"},
		{spec: BuildSpec{Context: dir, Target: "a b"}, want: "space"},
		{spec: BuildSpec{Context: dir, BuildArgs: []string{"novalue"}}, want: "KEY=value"},
		{spec: BuildSpec{Context: dir, BuildArgs: []string{"=x"}}, want: "KEY=value"},
		{spec: BuildSpec{Context: dir, BuildArgs: []string{"1A=x"}}, want: "KEY=value"},
	} {
		err := tc.spec.Check()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc.spec, err, tc.want)
		}
	}
}

func TestIsDockerfile(t *testing.T) {
	for name, want := range map[string]bool{
		"Dockerfile": true, "dockerfile": true, "Containerfile": true, "Dockerfile.dev": true,
		"api.Dockerfile": true, "build.dockerfile": true,
		"Dockerfiles": false, "README.md": false, "compose.yaml": false, ".dockerignore": false,
	} {
		if got := IsDockerfile(name); got != want {
			t.Errorf("IsDockerfile(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestScanTermLines: \n, \r\n and a lone \r each end a line — a progress
// bar's redraws are lines in a log — and a last line without one is kept.
func TestScanTermLines(t *testing.T) {
	sc := bufio.NewScanner(strings.NewReader("a\nb\r\nc\rd\r\ne"))
	sc.Split(scanTermLines)
	var got []string
	for sc.Scan() {
		got = append(got, sc.Text())
	}
	if want := []string{"a", "b", "c", "d", "e"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestStreamCommand runs a real, harmless command: stdout and stderr both
// arrive, in order, and the exit status is reported once output ends.
func TestStreamCommand(t *testing.T) {
	st, err := StreamCommand(context.Background(), CommandSpec{
		Args: []string{"sh", "-c", "echo one; echo two >&2; echo three; exit 3"},
		Dir:  t.TempDir(),
		Env:  []string{"DOCKMASTER_TEST=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for l := range st.Lines {
		got = append(got, l)
	}
	if want := []string{"one", "two", "three"}; !slices.Equal(got, want) {
		t.Errorf("lines %q, want %q", got, want)
	}
	if err := <-st.Done; err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("done: %v", err)
	}
}

// TestStreamCommandCancel: canceling kills the command and reports the
// cancellation, not the kill's exit status.
func TestStreamCommandCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st, err := StreamCommand(ctx, CommandSpec{Args: []string{"sh", "-c", "echo started; exec sleep 30"}})
	if err != nil {
		t.Fatal(err)
	}
	if l := <-st.Lines; l != "started" {
		t.Fatalf("first line %q", l)
	}
	cancel()
	select {
	case err := <-st.Done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("done: %v, want canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the command outlived its cancel")
	}
}
