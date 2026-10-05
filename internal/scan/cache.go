// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package scan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultTTL is how long a cached scan stays fresh (imageScans.ttl): a
// week, after which the vulnerability databases have moved enough that an
// old count is a hint rather than an answer.
const DefaultTTL = 7 * 24 * time.Hour

// Tally is the number of findings at each severity.
type Tally struct {
	Critical   int `json:"critical"`
	High       int `json:"high"`
	Medium     int `json:"medium"`
	Low        int `json:"low"`
	Negligible int `json:"negligible"`
	Unknown    int `json:"unknown"`
}

// TallyOf counts findings by severity.
func TallyOf(vs []Vuln) Tally {
	var t Tally
	for _, v := range vs {
		switch v.Severity {
		case Critical:
			t.Critical++
		case High:
			t.High++
		case Medium:
			t.Medium++
		case Low:
			t.Low++
		case Negligible:
			t.Negligible++
		default:
			t.Unknown++
		}
	}
	return t
}

// Result is one image's scan as the cache keeps it (#63): which scanner,
// when, and how many findings at each severity — not the findings, which
// `v` scans for again.
type Result struct {
	ScannedAt time.Time `json:"scannedAt"`
	Scanner   string    `json:"scanner"`
	Counts    Tally     `json:"counts"`
}

// valid is whether a result read from disk is one a scan wrote: a file cut
// short or edited by hand reads as no scan at all.
func (r Result) valid() bool {
	c := r.Counts
	return r.Scanner != "" && !r.ScannedAt.IsZero() &&
		c.Critical >= 0 && c.High >= 0 && c.Medium >= 0 && c.Low >= 0 && c.Negligible >= 0 && c.Unknown >= 0
}

// Cache keeps scan results by image ID, one JSON file each under dir
// (<state>/scans/<id>.json), and in memory once read. An image ID names
// its content exactly, so a result never goes stale by the image changing
// — only by the vulnerability databases moving, which the TTL is for.
//
// It is safe for concurrent use. A nil *Cache holds nothing and writes
// nothing; dir "" keeps results for this run only.
type Cache struct {
	mem map[string]cached
	// now is the clock staleness is judged by; tests replace it.
	now func() time.Time
	dir string
	ttl time.Duration
	mu  sync.Mutex
}

type cached struct {
	r  Result
	ok bool
}

// NewCache is a cache under dir whose results are stale after ttl (zero
// or less is DefaultTTL).
func NewCache(dir string, ttl time.Duration) *Cache {
	c := &Cache{dir: dir, mem: map[string]cached{}, now: time.Now}
	c.SetTTL(ttl)
	return c
}

// SetTTL changes how long a result stays fresh, for a config reload.
func (c *Cache) SetTTL(ttl time.Duration) {
	if c == nil {
		return
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	c.mu.Lock()
	c.ttl = ttl
	c.mu.Unlock()
}

// Dir is where the cache keeps its files; "" for none.
func (c *Cache) Dir() string {
	if c == nil {
		return ""
	}
	return c.dir
}

// fileNameRe is what an image ID may contain once its colon is replaced:
// the digest algorithm and hex, nothing that could climb out of dir.
var fileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// FileName is the cache file for image ID id — sha256:abc… is
// sha256-abc….json — and false for an ID that is not one.
func FileName(id string) (string, bool) {
	name := strings.ReplaceAll(id, ":", "-")
	if len(name) > 200 || !fileNameRe.MatchString(name) {
		return "", false
	}
	return name + ".json", true
}

// Get is image id's cached result. A missing, unreadable or corrupt file
// is no result: it reads as never scanned, and is not read again this run.
func (c *Cache) Get(id string) (Result, bool) {
	if c == nil || id == "" {
		return Result{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.mem[id]; ok {
		return e.r, e.ok
	}
	r, ok := c.read(id)
	c.mem[id] = cached{r: r, ok: ok}
	return r, ok
}

func (c *Cache) read(id string) (Result, bool) {
	name, ok := FileName(id)
	if !ok || c.dir == "" {
		return Result{}, false
	}
	b, err := os.ReadFile(filepath.Join(c.dir, name)) //nolint:gosec // the app's own state, by a checked name
	if err != nil {
		return Result{}, false
	}
	var r Result
	if json.Unmarshal(b, &r) != nil || !r.valid() {
		return Result{}, false
	}
	return r, true
}

// Put records image id's result: in memory at once, so the column shows it
// whatever happens to the file, then on disk. The error is the file's.
func (c *Cache) Put(id string, r Result) error {
	if c == nil || id == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mem[id] = cached{r: r, ok: true}
	if c.dir == "" {
		return nil
	}
	name, ok := FileName(id)
	if !ok {
		return fmt.Errorf("not an image ID: %q", id)
	}
	return writeFile(filepath.Join(c.dir, name), r)
}

// writeFile writes r through a temporary file, so a crash mid-write
// cannot leave half a result behind.
func writeFile(path string, r Result) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Stale is whether r is older than the TTL.
func (c *Cache) Stale(r Result) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now().Sub(r.ScannedAt) > c.ttl
}

// Fresh is whether image id has a result that is not stale — one a
// background scan can skip.
func (c *Cache) Fresh(id string) bool {
	r, ok := c.Get(id)
	return ok && !c.Stale(r)
}
