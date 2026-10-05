// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package scan

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCacheRoundTrip: a result written by one run is read by the next,
// from <dir>/<id>.json, counts and all.
func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	want := Result{ScannedAt: at, Scanner: "grype", Counts: TallyOf([]Vuln{
		{Severity: Critical}, {Severity: High}, {Severity: High}, {Severity: Low}, {Severity: Unknown},
	})}
	if want.Counts != (Tally{Critical: 1, High: 2, Low: 1, Unknown: 1}) {
		t.Fatalf("tally %+v", want.Counts)
	}
	if err := NewCache(dir, 0).Put("sha256:abc123", want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sha256-abc123.json")); err != nil {
		t.Fatalf("not written where expected: %v", err)
	}
	got, ok := NewCache(dir, 0).Get("sha256:abc123")
	if !ok || !got.ScannedAt.Equal(at) || got.Scanner != "grype" || got.Counts != want.Counts {
		t.Errorf("read back %+v %v, want %+v", got, ok, want)
	}
}

// TestCacheUnscanned: a missing, corrupt, truncated or hand-edited file is
// no result — never an error, never a panic.
func TestCacheUnscanned(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"sha256-garbage.json":   "not json",
		"sha256-truncated.json": `{"scannedAt": "2026-10-01T12:00:00Z", "scanner": "gr`,
		"sha256-noscanner.json": `{"scannedAt": "2026-10-01T12:00:00Z", "counts": {"high": 2}}`,
		"sha256-notime.json":    `{"scanner": "grype", "counts": {"high": 2}}`,
		"sha256-negative.json":  `{"scannedAt": "2026-10-01T12:00:00Z", "scanner": "grype", "counts": {"high": -2}}`,
		"sha256-empty.json":     "",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := NewCache(dir, 0)
	for _, id := range []string{
		"sha256:missing", "sha256:garbage", "sha256:truncated", "sha256:noscanner",
		"sha256:notime", "sha256:negative", "sha256:empty", "../../etc/passwd", "",
	} {
		if r, ok := c.Get(id); ok {
			t.Errorf("%q read as scanned: %+v", id, r)
		}
		if c.Fresh(id) {
			t.Errorf("%q is fresh", id)
		}
	}
	var nilCache *Cache
	if _, ok := nilCache.Get("sha256:x"); ok || nilCache.Put("sha256:x", Result{}) != nil || nilCache.Fresh("sha256:x") {
		t.Error("a nil cache holds something")
	}
}

// TestCacheTTL: a result older than the TTL is stale, and not fresh; the
// TTL defaults to a week and a reload can change it.
func TestCacheTTL(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	c := NewCache(t.TempDir(), 0)
	c.now = func() time.Time { return now }
	put := func(id string, age time.Duration) {
		if err := c.Put(id, Result{ScannedAt: now.Add(-age), Scanner: "trivy"}); err != nil {
			t.Fatal(err)
		}
	}
	put("sha256:new", time.Hour)
	put("sha256:edge", DefaultTTL)
	put("sha256:old", DefaultTTL+time.Second)
	for id, fresh := range map[string]bool{"sha256:new": true, "sha256:edge": true, "sha256:old": false} {
		if c.Fresh(id) != fresh {
			t.Errorf("%s fresh %v, want %v", id, !fresh, fresh)
		}
		r, _ := c.Get(id)
		if c.Stale(r) == fresh {
			t.Errorf("%s stale %v", id, c.Stale(r))
		}
	}
	c.SetTTL(30 * time.Minute)
	if c.Fresh("sha256:new") {
		t.Error("an hour-old result is fresh under a 30m TTL")
	}
}

// TestCacheFileName: an image ID names a file in the directory and
// nothing that could climb out of it.
func TestCacheFileName(t *testing.T) {
	for id, want := range map[string]string{
		"sha256:0123abcd": "sha256-0123abcd.json",
		"0123abcd":        "0123abcd.json",
		"../x":            "",
		"sha256:a/b":      "",
		".hidden":         "",
		"":                "",
	} {
		got, ok := FileName(id)
		if got != want || ok != (want != "") {
			t.Errorf("FileName(%q) = %q %v, want %q", id, got, ok, want)
		}
	}
}

// TestCachePutKeepsResultWhenWriteFails: a write that fails still leaves
// the result in memory for this run, and says why.
func TestCachePutKeepsResultWhenWriteFails(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCache(filepath.Join(blocker, "scans"), 0)
	r := Result{ScannedAt: time.Now(), Scanner: "grype", Counts: Tally{High: 1}}
	if err := c.Put("sha256:aa", r); err == nil {
		t.Error("a write under a file succeeded")
	}
	if got, ok := c.Get("sha256:aa"); !ok || got.Counts.High != 1 {
		t.Errorf("result lost: %+v %v", got, ok)
	}
	if err := NewCache("", 0).Put("sha256:aa", r); err != nil {
		t.Errorf("a cache with no directory failed to keep a result: %v", err)
	}
}
