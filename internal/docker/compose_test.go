// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func composeLabels(project, files, dir, env string) map[string]string {
	return map[string]string{
		LabelProject:     project,
		LabelService:     "web",
		LabelConfigFiles: files,
		LabelWorkingDir:  dir,
		LabelEnvFiles:    env,
	}
}

func TestProjectsCarryComposeLabels(t *testing.T) {
	cs := []Container{
		{
			Name:    "shop-web-1",
			State:   "running",
			Project: "shop",
			Service: "web",
			Labels: composeLabels(
				"shop",
				"/src/shop/compose.yaml, /src/shop/compose.override.yaml",
				"/src/shop",
				"/src/shop/.env",
			),
		},
	}
	got := Projects(cs)
	if len(got) != 1 {
		t.Fatalf("projects = %+v", got)
	}
	p := got[0]
	if p.WorkingDir != "/src/shop" ||
		!reflect.DeepEqual(p.ConfigFiles, []string{"/src/shop/compose.yaml", "/src/shop/compose.override.yaml"}) ||
		!reflect.DeepEqual(p.EnvFiles, []string{"/src/shop/.env"}) {
		t.Errorf("labels not folded: %+v", p)
	}
}

func TestComposeArgs(t *testing.T) {
	p := Project{
		Name: "shop", WorkingDir: "/src/shop",
		ConfigFiles: []string{"/src/shop/a.yaml", "/src/shop/b.yaml"},
		EnvFiles:    []string{"/src/shop/.env"},
	}
	want := []string{
		"-p", "shop", "--project-directory", "/src/shop",
		"-f", "/src/shop/a.yaml", "-f", "/src/shop/b.yaml", "--env-file", "/src/shop/.env",
	}
	if got := p.ComposeArgs(); !reflect.DeepEqual(got, want) {
		t.Errorf("ComposeArgs =\n%q\nwant\n%q", got, want)
	}
}

func TestMissingFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(f, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := (Project{ConfigFiles: []string{f}}).MissingFile(); got != "" {
		t.Errorf("present file reported missing: %q", got)
	}
	gone := filepath.Join(dir, "gone.yaml")
	if got := (Project{ConfigFiles: []string{f, gone}}).MissingFile(); !strings.Contains(got, gone) {
		t.Errorf("missing compose file not reported: %q", got)
	}
	if got := (Project{ConfigFiles: []string{f}, EnvFiles: []string{gone}}).MissingFile(); !strings.Contains(got, gone) {
		t.Errorf("missing env file not reported: %q", got)
	}
	if got := (Project{}).MissingFile(); got == "" {
		t.Error("a project with no recorded files reported runnable")
	}
}

// TestComposeVerbs pins each verb's full argv, --host first so compose acts
// on the daemon dockmaster is showing.
func TestComposeVerbs(t *testing.T) {
	var calls []string
	run := func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return nil, nil
	}
	c := NewComposeWithRunner(run, "unix:///h/docker.sock")
	p := Project{Name: "shop", WorkingDir: "/s", ConfigFiles: []string{"/s/c.yaml"}}
	ctx := context.Background()
	for _, fn := range []func(context.Context, Project) error{c.Up, c.Down, c.Restart, c.Pull} {
		if err := fn(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	base := "--host unix:///h/docker.sock compose -p shop --project-directory /s -f /s/c.yaml "
	want := []string{base + "up -d", base + "down", base + "restart", base + "pull"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls =\n%q\nwant\n%q", calls, want)
	}
}

func TestComposeErrorIsTheLastLine(t *testing.T) {
	err := &ComposeError{
		Stderr: " Container shop-web-1  Creating\n\nError response from daemon: port is already allocated\n",
		Err:    errors.New("exit status 1"),
	}
	if got := err.Error(); got != "compose: Error response from daemon: port is already allocated" {
		t.Errorf("Error() = %q", got)
	}
}
