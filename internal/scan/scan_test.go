// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package scan

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestParseReports: both scanners' real reports (trimmed from scans of
// registry:2) read into the same shape, with each scanner's spelling of
// severity and of the fixed versions normalized.
func TestParseReports(t *testing.T) {
	g, err := Parse("grype", fixture(t, "grype.json"))
	if err != nil || len(g) != 3 {
		t.Fatalf("grype: %d findings, err %v", len(g), err)
	}
	if v := g[0]; v.ID != "GO-2023-2102" || v.Severity != High || v.Package != "stdlib" ||
		v.Installed != "go1.20.8" || v.FixedIn == "" || v.Title == "" {
		t.Errorf("grype finding read as %+v", v)
	}
	if !slices.IsSortedFunc(g, func(a, b Vuln) int { return strings.Compare(a.ID, b.ID) }) {
		t.Errorf("equal severities not ordered by ID: %v", ids(g))
	}

	tr, err := Parse("trivy", fixture(t, "trivy.json"))
	if err != nil || len(tr) != 2 {
		t.Fatalf("trivy: %d findings, err %v", len(tr), err)
	}
	if v := tr[0]; v.ID != "CVE-2024-24790" || v.Severity != Critical || v.FixedIn != "1.21.11, 1.22.4" || v.Title == "" {
		t.Errorf("trivy finding read as %+v", v)
	}
	if _, err := Parse("grype", []byte("not json")); err == nil {
		t.Error("a report that is not JSON parsed")
	}
}

func ids(vs []Vuln) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}

// TestWorstFirst: findings come worst first whatever order the report has.
func TestWorstFirst(t *testing.T) {
	rep := `{"Results":[{"Vulnerabilities":[
		{"VulnerabilityID":"a","Severity":"LOW"},{"VulnerabilityID":"b","Severity":"CRITICAL"},
		{"VulnerabilityID":"c","Severity":"MEDIUM"},{"VulnerabilityID":"d","Severity":"wat"}]}]}`
	vs, err := Parse("trivy", []byte(rep))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(vs), ","); got != "b,c,a,d" {
		t.Errorf("order %s, want b,c,a,d", got)
	}
	if c := Counts(vs); c[Critical] != 1 || c[Unknown] != 1 {
		t.Errorf("counts %v", c)
	}
}

// TestCommand: each scanner reads the image from the daemon dockmaster is
// showing, through DOCKER_HOST, with the arguments that make it emit JSON.
func TestCommand(t *testing.T) {
	g := Command(context.Background(), Scanner{Name: "grype", Path: "/bin/grype"}, "nginx:1.27", "unix:///x.sock")
	if got := strings.Join(g.Args[1:], " "); got != "docker:nginx:1.27 -o json -q" {
		t.Errorf("grype args %q", got)
	}
	if !slices.Contains(g.Env, "DOCKER_HOST=unix:///x.sock") {
		t.Error("grype is not pointed at dockmaster's daemon")
	}
	tr := Command(context.Background(), Scanner{Name: "trivy", Path: "/bin/trivy"}, "nginx:1.27", "")
	if got := strings.Join(tr.Args[1:], " "); got != "image --quiet --format json --scanners vuln nginx:1.27" {
		t.Errorf("trivy args %q", got)
	}
	for _, e := range tr.Env {
		if strings.HasPrefix(e, "DOCKER_HOST=unix:///x") {
			t.Error("an empty host still set DOCKER_HOST")
		}
	}
}

// fakeScanners puts scripts named for the given scanners on an otherwise
// empty PATH; each prints the fixture or, for "fail", fails.
func fakeScanners(t *testing.T, scripts map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(
			filepath.Join(dir, name),
			[]byte("#!/bin/sh\n"+body+"\n"),
			0o700,
		); err != nil { //nolint:gosec // test fake
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// TestDetectAndRun: grype is preferred when both are installed, trivy is
// used alone, none is a message saying how to get one; a run reads the
// scanner's output, and a failing scanner's last stderr line is reported.
func TestDetectAndRun(t *testing.T) {
	report, _ := filepath.Abs(filepath.Join("testdata", "trivy.json"))
	fakeScanners(t, map[string]string{"grype": "exit 0", "trivy": "/bin/cat " + report})
	if s, err := Detect(); err != nil || s.Name != "grype" {
		t.Errorf("both installed: %+v %v, want grype", s, err)
	}

	fakeScanners(t, map[string]string{"trivy": "/bin/cat " + report})
	s, err := Detect()
	if err != nil || s.Name != "trivy" {
		t.Fatalf("only trivy: %+v %v", s, err)
	}
	vs, err := Run(context.Background(), s, "registry:2", "")
	if err != nil || len(vs) != 2 {
		t.Errorf("run: %d findings, err %v", len(vs), err)
	}

	fakeScanners(t, map[string]string{"trivy": "echo progress >&2; echo 'image not found' >&2; exit 1"})
	s, _ = Detect()
	if _, err := Run(
		context.Background(),
		s,
		"nope:1",
		"",
	); err == nil ||
		!strings.Contains(err.Error(), "image not found") {
		t.Errorf("a failing scanner reported %v", err)
	}

	fakeScanners(t, map[string]string{})
	if _, err := Detect(); err == nil || !strings.Contains(err.Error(), "brew install grype") {
		t.Errorf("no scanner: %v", err)
	}
}
