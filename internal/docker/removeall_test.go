// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeDaemon serves the list calls from fixed JSON and records each DELETE,
// answering it with the status refuse names (200 when absent).
func fakeDaemon(t *testing.T, lists map[string]any, refuse map[string]int) (*Client, func() []string) {
	t.Helper()
	version := regexp.MustCompile(`^/v[0-9.]+`)
	var (
		mu      sync.Mutex
		deleted []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		path := version.ReplaceAllString(r.URL.Path, "")
		if r.Method == http.MethodDelete {
			if f := r.URL.Query().Get("force"); f == "1" || f == "true" {
				t.Errorf("DELETE %s was forced", path)
			}
			mu.Lock()
			deleted = append(deleted, path)
			mu.Unlock()
			if code, ok := refuse[path]; ok {
				w.WriteHeader(code)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "refused: " + path})
				return
			}
			_, _ = w.Write([]byte("[]"))
			return
		}
		if path == "/_ping" {
			_, _ = w.Write([]byte("OK"))
			return
		}
		body, ok := lists[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	c, err := New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(deleted)
	}
}

// TestRemoveAllImages: an image a container uses is never touched — not
// even untagged; a multi-tagged one goes tag by tag, unforced; an untagged
// one goes by ID; newest goes first; a conflict is a skip and anything
// else is a skip that is also reported.
func TestRemoveAllImages(t *testing.T) {
	c, deleted := fakeDaemon(t, map[string]any{
		"/images/json": []map[string]any{
			{"Id": "sha256:old", "Created": 1, "RepoTags": []string{"base:1"}},
			{"Id": "sha256:used", "Created": 5, "RepoTags": []string{"app:1", "app:latest"}},
			{"Id": "sha256:multi", "Created": 4, "RepoTags": []string{"x:1", "x:2"}},
			{"Id": "sha256:none", "Created": 3, "RepoTags": []string{"<none>:<none>"}},
			{"Id": "sha256:broken", "Created": 2, "RepoTags": []string{"broken:1"}},
		},
		"/containers/json": []map[string]any{
			{"Id": "c1", "Names": []string{"/c1"}, "ImageID": "sha256:used", "State": "exited"},
		},
	}, map[string]int{
		"/images/base:1":   http.StatusConflict,
		"/images/broken:1": http.StatusInternalServerError,
	})

	res, err := c.RemoveAllImages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 2 || res.Skipped != 3 {
		t.Errorf("removed %d skipped %d, want 2 and 3", res.Removed, res.Skipped)
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "broken:1") || strings.Contains(res.Err.Error(), "base:1") {
		t.Errorf("Err = %v, want the 500 reported and the conflict not", res.Err)
	}
	want := []string{"/images/x:1", "/images/x:2", "/images/sha256:none", "/images/broken:1", "/images/base:1"}
	if got := deleted(); !slices.Equal(got, want) {
		t.Errorf("deleted %q\nwant    %q", got, want)
	}
}

// TestRemoveAllVolumes: every volume is asked for, unforced; the daemon's
// in-use refusal is a skip.
func TestRemoveAllVolumes(t *testing.T) {
	c, deleted := fakeDaemon(t, map[string]any{
		"/volumes": map[string]any{"Volumes": []map[string]any{
			{"Name": "db"}, {"Name": "cache"}, {"Name": "tmp"},
		}},
	}, map[string]int{"/volumes/db": http.StatusConflict})

	res, err := c.RemoveAllVolumes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 2 || res.Skipped != 1 || res.Err != nil {
		t.Errorf("result %+v, want 2 removed, 1 skipped, no error", res)
	}
	if got := deleted(); !slices.Equal(got, []string{"/volumes/db", "/volumes/cache", "/volumes/tmp"}) {
		t.Errorf("deleted %q", got)
	}
}
