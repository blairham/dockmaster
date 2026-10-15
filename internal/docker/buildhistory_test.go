// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures under testdata/buildx are real buildx v0.37.2 output from two
// throwaway builds on a colima builder, the records removed afterwards: a
// FROM scratch / RUN false that fails, and a cache-only build with a
// target and a build arg. Their context paths are rewritten to /src/… and
// the failure's base64 stack trace (the capturing machine's) is emptied.

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "buildx", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseBuildHistory(t *testing.T) {
	recs, err := ParseBuildHistory(readFixture(t, "ls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("%d records, want 2", len(recs))
	}
	ok, failed := recs[0], recs[1]
	if ok.Name != "app/Dockerfile.dev (base)" || ok.Status != BuildCompleted || failed.Status != BuildError {
		t.Errorf("not newest first, or misread: %+v / %+v", ok, failed)
	}
	if ok.ID() != "pzk1kndfayz6pb67ixyla74lz" || ok.Ref != "colima/colima/pzk1kndfayz6pb67ixyla74lz" {
		t.Errorf("ref %q id %q", ok.Ref, ok.ID())
	}
	if ok.CompletedSteps != 4 || ok.TotalSteps != 4 || ok.CachedSteps != 0 {
		t.Errorf("steps %+v", ok)
	}
	if d := ok.Duration(time.Now()); d < 60*time.Millisecond || d > 65*time.Millisecond {
		t.Errorf("duration %v, want ~62ms", d)
	}
}

// TestParseBuildHistoryRunning: a record still building has no
// completed_at (buildx omits it); its duration runs to now. The line is
// hand-made in buildx's shape — MarshalJSON in commands/history/ls.go.
func TestParseBuildHistoryRunning(t *testing.T) {
	line := `{"cached_steps":2,"completed_steps":3,"created_at":"2026-10-15T16:00:00Z",` +
		`"name":"web","ref":"colima/colima/abc","status":"Running","total_steps":9}` + "\n"
	recs, err := ParseBuildHistory([]byte(line))
	if err != nil || len(recs) != 1 {
		t.Fatalf("%v %v", recs, err)
	}
	r := recs[0]
	now := time.Date(2026, 10, 15, 16, 1, 0, 0, time.UTC)
	if r.Status != BuildRunning || !r.Completed.IsZero() || r.Duration(now) != time.Minute || r.CachedPercent() != 66 {
		t.Errorf("%+v duration %v cached %d", r, r.Duration(now), r.CachedPercent())
	}
}

func TestParseBuildHistoryRejectsGarbage(t *testing.T) {
	if _, err := ParseBuildHistory([]byte("ID NAME STATUS\n")); err == nil {
		t.Error("a table parsed as JSON")
	}
	if recs, err := ParseBuildHistory(nil); err != nil || len(recs) != 0 {
		t.Errorf("empty history: %v %v", recs, err)
	}
}

func TestBuildDetailError(t *testing.T) {
	d, err := ParseBuildDetail(readFixture(t, "inspect-error.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := `[1/1] RUN false: process "/bin/sh -c false" did not complete successfully: exit code: 1`
	if got := d.ErrorLine(); got != want {
		t.Errorf("error line %q\nwant %q", got, want)
	}
	ok, err := ParseBuildDetail(readFixture(t, "inspect-ok.json"))
	if err != nil || ok.ErrorLine() != "" {
		t.Errorf("a completed build has an error line %q (%v)", ok.ErrorLine(), err)
	}
}

// TestBuildDetailSpec: a rebuild takes the record's context, Dockerfile,
// target and args — and refuses a context that is not here.
func TestBuildDetailSpec(t *testing.T) {
	d, err := ParseBuildDetail(readFixture(t, "inspect-ok.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, missing := d.Spec(); missing == nil || !strings.Contains(missing.Error(), "/src/app") {
		t.Errorf("a missing context: %v", missing)
	}
	d.Context = t.TempDir()
	spec, err := d.Spec()
	if err != nil {
		t.Fatal(err)
	}
	if spec.Context != d.Context || spec.Dockerfile != "Dockerfile.dev" || spec.Target != "base" ||
		strings.Join(spec.BuildArgs, " ") != "GREETING=hello" || spec.Tag != "" {
		t.Errorf("spec %+v", spec)
	}
	d.Context = "https://github.com/x/y.git"
	if _, err := d.Spec(); err == nil {
		t.Error("a git context was rebuilt from here")
	}
}

// TestBuildHistoryArgs: the CLI is pointed at the daemon on screen and the
// configured builder, the environment agrees with the endpoint, and a ref
// comes after every flag.
func TestBuildHistoryArgs(t *testing.T) {
	var got [][]string
	var env []string
	run := func(_ context.Context, e []string, args ...string) ([]byte, error) {
		got, env = append(got, args), e
		return readFixture(t, "ls.jsonl"), nil
	}
	h := NewBuildHistoryWithRunner(run, []string{"--host", "unix:///x.sock"}, "colima")
	if _, err := h.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.Remove(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--host unix:///x.sock buildx history ls --builder=colima --format=json",
		"--host unix:///x.sock buildx history rm --builder=colima abc",
	}
	for i, w := range want {
		if g := strings.Join(got[i], " "); g != w {
			t.Errorf("ran %q\nwant %q", g, w)
		}
	}
	if strings.Join(env, " ") != "DOCKER_HOST=unix:///x.sock" {
		t.Errorf("env %q", env)
	}
	logs := h.LogsCommand("docker", "abc")
	if g := strings.Join(logs.Args, " "); g != "docker --host unix:///x.sock buildx history logs --builder=colima abc" {
		t.Errorf("logs %q", g)
	}
}

// TestBuildHistoryTooOld: a buildx without `history`, or without
// `ls --format`, is ErrNoBuildxHistory — one clear message, not an empty
// table. Other failures pass through with buildx's own last line.
func TestBuildHistoryTooOld(t *testing.T) {
	for _, stderr := range []string{
		// buildx before v0.20.0, as today's buildx says it of a verb it lacks.
		`ERROR: unknown command: "history"` + "\n",
		// No buildx plugin at all, old and new CLIs.
		`docker: 'buildx' is not a docker command.` + "\n",
		"docker: unknown command: docker buildx\n",
		"unknown flag: --format\n",
	} {
		h := NewBuildHistoryWithRunner(func(context.Context, []string, ...string) ([]byte, error) {
			return nil, &HistoryError{Stderr: stderr, Err: errors.New("exit status 1")}
		}, nil, "")
		if _, err := h.List(context.Background()); !errors.Is(err, ErrNoBuildxHistory) {
			t.Errorf("%q: %v", stderr, err)
		}
	}
	h := NewBuildHistoryWithRunner(func(context.Context, []string, ...string) ([]byte, error) {
		return nil, &HistoryError{Stderr: "ERROR: no builder \"nope\" found\n", Err: errors.New("exit status 1")}
	}, nil, "nope")
	_, err := h.List(context.Background())
	if errors.Is(err, ErrNoBuildxHistory) || err == nil || err.Error() != `buildx: no builder "nope" found` {
		t.Errorf("a missing builder: %v", err)
	}
}
