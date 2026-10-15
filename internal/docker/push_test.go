// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/jsonstream"
)

func TestParseTag(t *testing.T) {
	cases := []struct {
		in, full, familiar string
		bad                bool
	}{
		{in: "app", full: "docker.io/library/app:latest", familiar: "app:latest"},
		{in: "me/app:v1", full: "docker.io/me/app:v1", familiar: "me/app:v1"},
		{in: "ghcr.io/me/app:RC1", full: "ghcr.io/me/app:RC1", familiar: "ghcr.io/me/app:RC1"},
		{in: "localhost:5001/app", full: "localhost:5001/app:latest", familiar: "localhost:5001/app:latest"},
		{in: "", bad: true},
		{in: "App:v1", bad: true},
		{in: "app::v1", bad: true},
		{in: "app@sha256:" + strings.Repeat("a", 64), bad: true},
	}
	for _, c := range cases {
		full, fam, err := ParseTag(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("ParseTag(%q) = %q, want an error", c.in, full)
			}
			continue
		}
		if err != nil || full != c.full || fam != c.familiar {
			t.Errorf("ParseTag(%q) = %q %q %v, want %q %q", c.in, full, fam, err, c.full, c.familiar)
		}
	}
}

func TestPushProgress(t *testing.T) {
	var p PushProgress
	msgs := []jsonstream.Message{
		{Status: "The push refers to repository [ghcr.io/me/app]"},
		{ID: "a1", Status: "Preparing"},
		{ID: "b2", Status: "Preparing"},
		{ID: "a1", Status: "Pushing", Progress: &jsonstream.Progress{Current: 512 << 10, Total: 2 << 20}},
		{ID: "b2", Status: "Layer already exists"},
	}
	changed := 0
	for _, m := range msgs {
		if p.Add(m) {
			changed++
		}
	}
	if changed != 4 {
		t.Errorf("%d changes, want 4 (the repository line is not a layer)", changed)
	}
	if got := p.String(); !strings.HasPrefix(got, "1/2 layers · ") || !strings.Contains(got, "/") {
		t.Errorf("summary %q", got)
	}
	p.Add(jsonstream.Message{ID: "a1", Status: "Pushed"})
	if got := p.String(); got != "2/2 layers" {
		t.Errorf("summary %q, want 2/2 layers", got)
	}
	// A tag's digest line carries an ID that is not a layer.
	if p.Add(jsonstream.Message{ID: "v1", Status: "v1: digest: sha256:abc size: 1234"}) || p.String() != "2/2 layers" {
		t.Errorf("the digest line counted as a layer: %q", p.String())
	}
}

// pushDaemon is a daemon answering /images/<name>/push with lines, and
// /images/<src>/tag, recording what it was sent.
func pushDaemon(t *testing.T, lines []string) (*Client, func() (path, auth string)) {
	t.Helper()
	version := regexp.MustCompile(`^/v[0-9.]+`)
	var mu sync.Mutex
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		p := version.ReplaceAllString(r.URL.Path, "")
		if p == "/_ping" {
			_, _ = w.Write([]byte("OK"))
			return
		}
		mu.Lock()
		gotPath, gotAuth = p+"?"+r.URL.RawQuery, r.Header.Get("X-Registry-Auth")
		mu.Unlock()
		switch {
		case strings.HasSuffix(p, "/push"):
			for _, l := range lines {
				_, _ = w.Write([]byte(l + "\n"))
			}
		case strings.HasSuffix(p, "/tag"):
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return c, func() (string, string) {
		mu.Lock()
		defer mu.Unlock()
		return gotPath, gotAuth
	}
}

// TestPushImageSendsTheConfigCredential: the credential the docker config
// holds for the image's registry goes to the daemon as X-Registry-Auth,
// progress is reported, and the registry is named.
func TestPushImageSendsTheConfigCredential(t *testing.T) {
	cfg, _ := isolateDockerConfig(t)
	writeConfig(t, cfg, `{"auths": {"ghcr.io": {"auth": "`+basicAuth("octo", "pat")+`"}}}`)
	c, got := pushDaemon(t, []string{
		`{"status":"Preparing","id":"a1"}`,
		`{"status":"Pushing","id":"a1","progressDetail":{"current":10,"total":20}}`,
		`{"status":"Pushed","id":"a1"}`,
	})
	var progress []string
	reg, err := c.PushImage(context.Background(), "ghcr.io/me/app:v1", func(l string) { progress = append(progress, l) })
	if err != nil || reg != "ghcr.io" {
		t.Fatalf("PushImage = %q, %v", reg, err)
	}
	path, auth := got()
	if path != "/images/ghcr.io/me/app/push?tag=v1" {
		t.Errorf("daemon got %s", path)
	}
	b, err := base64.URLEncoding.DecodeString(auth)
	if err != nil {
		t.Fatalf("auth header %q: %v", auth, err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil || m["username"] != "octo" || m["password"] != "pat" {
		t.Errorf("auth header carried %s", b)
	}
	if len(progress) == 0 || progress[len(progress)-1] != "1/1 layers" {
		t.Errorf("progress %q", progress)
	}
}

// TestPushImageAnonymous: with no credential for the registry the push goes
// without one, and the daemon's error line is the push's error.
func TestPushImageAnonymous(t *testing.T) {
	isolateDockerConfig(t)
	c, got := pushDaemon(t, []string{
		`{"status":"Preparing","id":"a1"}`,
		`{"errorDetail":{"message":"denied: requested access to the resource is denied"},"error":"denied"}`,
	})
	_, err := c.PushImage(context.Background(), "me/app", nil)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("err %v, want the daemon's denial", err)
	}
	if path, auth := got(); path != "/images/docker.io/me/app/push?tag=latest" || auth != "" {
		t.Errorf("daemon got %s with auth %q", path, auth)
	}
}

func TestTagImage(t *testing.T) {
	c, got := pushDaemon(t, nil)
	if err := c.TagImage(context.Background(), "sha256:abc", "ghcr.io/me/app:v1"); err != nil {
		t.Fatal(err)
	}
	if path, _ := got(); path != "/images/sha256:abc/tag?repo=ghcr.io%2Fme%2Fapp&tag=v1" {
		t.Errorf("daemon got %s", path)
	}
	if err := c.TagImage(context.Background(), "sha256:abc", "Bad"); err == nil {
		t.Error("a bad tag reached the daemon")
	}
}
