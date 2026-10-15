// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blairham/dockmaster/internal/docker"
)

// fakeV2 is a registry's v2 API in memory: repositories with tags, each
// tag a manifest (or an index over two), and the delete endpoint.
type fakeV2 struct {
	manifests map[string]string // repo/digest -> body
	tags      map[string]map[string]string
	configs   map[string]string // digest -> body
	deleted   []string
	mu        sync.Mutex
	noDelete  bool
}

const (
	cfgDigest   = "sha256:c0nf19"
	imgDigest   = "sha256:1111"
	indexDigest = "sha256:2222"
)

func newFakeV2() *fakeV2 {
	img := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"config":{"digest":"` + cfgDigest + `","size":100},"layers":[{"size":1000},{"size":24}]}`
	index := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[` +
		`{"digest":"sha256:att","platform":{"os":"unknown","architecture":"unknown"}},` +
		`{"digest":"` + imgDigest + `","platform":{"os":"linux","architecture":"arm64","variant":"v8"}},` +
		`{"digest":"` + imgDigest + `","platform":{"os":"linux","architecture":"amd64"}}]}`
	return &fakeV2{
		manifests: map[string]string{"app/" + imgDigest: img, "app/" + indexDigest: index},
		tags:      map[string]map[string]string{"app": {"v1": imgDigest, "multi": indexDigest}, "lib/base": {}},
		configs:   map[string]string{cfgDigest: `{"created":"2026-01-02T03:04:05Z"}`},
	}
}

func (f *fakeV2) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v2/")
	switch {
	case path == "_catalog":
		repos := make([]string, 0, len(f.tags))
		for k := range f.tags {
			repos = append(repos, k)
		}
		slices.Sort(repos)
		// Two pages: the first names the second in a Link header.
		if r.URL.Query().Get("last") == "" {
			w.Header().Set("Link", `</v2/_catalog?last=`+repos[0]+`&n=1>; rel="next"`)
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos[:1]})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos[1:]})
	case strings.HasSuffix(path, "/tags/list"):
		repo := strings.TrimSuffix(path, "/tags/list")
		tags := []string{}
		for t := range f.tags[repo] {
			tags = append(tags, t)
		}
		slices.Sort(tags)
		_ = json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags})
	case strings.Contains(path, "/manifests/"):
		repo, ref, _ := strings.Cut(path, "/manifests/")
		digest := ref
		if d, ok := f.tags[repo][ref]; ok {
			digest = d
		}
		if r.Method == http.MethodDelete {
			if f.noDelete {
				w.WriteHeader(http.StatusMethodNotAllowed)
				_, _ = w.Write([]byte(`{"errors":[{"code":"UNSUPPORTED","message":"The operation is unsupported."}]}`))
				return
			}
			f.deleted = append(f.deleted, repo+"@"+ref)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		body, ok := f.manifests[repo+"/"+digest]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"code":"MANIFEST_UNKNOWN","message":"manifest unknown"}]}`))
			return
		}
		var m struct {
			MediaType string `json:"mediaType"`
		}
		_ = json.Unmarshal([]byte(body), &m)
		w.Header().Set("Content-Type", m.MediaType)
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write([]byte(body))
	case strings.Contains(path, "/blobs/"):
		_, digest, _ := strings.Cut(path, "/blobs/")
		_, _ = w.Write([]byte(f.configs[digest]))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func anonymous(context.Context, string) (docker.Credential, bool, error) {
	return docker.Credential{}, false, nil
}

func fakeClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(strings.TrimPrefix(srv.URL, "http://")).WithCredentials(anonymous)
}

// TestCatalogFollowsPages: every page of the catalog, by its Link header.
func TestCatalogFollowsPages(t *testing.T) {
	c := fakeClient(t, newFakeV2())
	repos, err := c.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"app", "lib/base"}; !slices.Equal(repos, want) {
		t.Errorf("catalog %v, want %v", repos, want)
	}
}

// TestImageReadsManifestAndConfig: a single image's digest, compressed
// size (config + layers) and created time; an index's own digest, its
// platforms without the attestation, and its first platform's size.
func TestImageReadsManifestAndConfig(t *testing.T) {
	c := fakeClient(t, newFakeV2())
	tags, err := c.Tags(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	imgs := c.Images(context.Background(), "app", tags)
	if len(imgs) != 2 || imgs[0].Tag != "multi" || imgs[1].Tag != "v1" {
		t.Fatalf("images %+v", imgs)
	}
	single := imgs[1]
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if single.Err != nil || single.Digest != imgDigest || single.Size != 1124 || !single.Created.Equal(created) {
		t.Errorf("v1 = %+v", single)
	}
	multi := imgs[0]
	if multi.Err != nil || multi.Digest != indexDigest || multi.Size != 1124 ||
		!slices.Equal(multi.Platforms, []string{"linux/arm64/v8", "linux/amd64"}) {
		t.Errorf("multi = %+v", multi)
	}
}

// TestDeleteByDigest: a delete goes by digest; a registry without deletes
// enabled answers 405, which is ErrDeleteDisabled; a tag is refused.
func TestDeleteByDigest(t *testing.T) {
	f := newFakeV2()
	c := fakeClient(t, f)
	if err := c.Delete(context.Background(), "app", imgDigest); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.deleted, []string{"app@" + imgDigest}) {
		t.Errorf("deleted %v", f.deleted)
	}
	if err := c.Delete(context.Background(), "app", "v1"); err == nil {
		t.Error("a delete by tag went through")
	}
	f.noDelete = true
	if err := c.Delete(context.Background(), "app", imgDigest); !errors.Is(err, ErrDeleteDisabled) {
		t.Errorf("delete on a registry without deletes: %v", err)
	}
}

// TestRegistryErrorMessage: the registry's own error code and message.
func TestRegistryErrorMessage(t *testing.T) {
	c := fakeClient(t, newFakeV2())
	img := c.Image(context.Background(), "app", "missing")
	if img.Err == nil || !strings.Contains(img.Err.Error(), "MANIFEST_UNKNOWN") {
		t.Errorf("missing tag: %v", img.Err)
	}
}

// TestBearerChallenge: a 401 with a Bearer challenge sends the docker
// config's credential to the realm, and the retry carries the token; the
// token is reused for the same scope rather than fetched again.
func TestBearerChallenge(t *testing.T) {
	var (
		mu         sync.Mutex
		tokenCalls int
		gotUser    string
		gotScope   string
	)
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		tokenCalls++
		user, pass, _ := r.BasicAuth()
		gotUser = user + ":" + pass
		gotScope = r.URL.Query().Get("scope") + " " + r.URL.Query().Get("service")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok123"})
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok123" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(
				`Bearer realm="%s/token",service="fake",scope="repository:app:pull,push"`, srv.URL,
			))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tags": []string{"v1"}})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	var asked string
	c := New(strings.TrimPrefix(srv.URL, "http://")).WithCredentials(
		func(_ context.Context, server string) (docker.Credential, bool, error) {
			asked = server
			return docker.Credential{Username: "octo", Password: "s3cret"}, true, nil
		},
	)
	for range 2 {
		tags, err := c.Tags(context.Background(), "app")
		if err != nil || !slices.Equal(tags, []string{"v1"}) {
			t.Fatalf("tags %v, %v", tags, err)
		}
	}
	if tokenCalls != 1 || gotUser != "octo:s3cret" || gotScope != "repository:app:pull,push fake" {
		t.Errorf("token calls %d user %q scope %q", tokenCalls, gotUser, gotScope)
	}
	if asked != strings.TrimPrefix(srv.URL, "http://") {
		t.Errorf("credential asked for %q", asked)
	}
}

// TestBasicChallenge: a Basic challenge is answered with the credential;
// with none, the error says to log in.
func TestBasicChallenge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "octo" || p != "pw" {
			w.Header().Set("WWW-Authenticate", `Basic realm="Registry Realm"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []string{"x"}})
	}))
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	if _, err := New(addr).WithCredentials(anonymous).Catalog(context.Background()); err == nil ||
		!strings.Contains(err.Error(), ":login") {
		t.Errorf("anonymous on a basic registry: %v", err)
	}
	c := New(addr).WithCredentials(func(context.Context, string) (docker.Credential, bool, error) {
		return docker.Credential{Username: "octo", Password: "pw"}, true, nil
	})
	if repos, err := c.Catalog(context.Background()); err != nil || !slices.Equal(repos, []string{"x"}) {
		t.Errorf("catalog %v, %v", repos, err)
	}
}

// TestNewChoosesSchemeAndHost: a loopback registry is plain HTTP, dialed
// at 127.0.0.1 for localhost; anything else is HTTPS; Docker Hub's config
// key is its API host.
func TestNewChoosesSchemeAndHost(t *testing.T) {
	for _, tc := range []struct{ addr, want string }{
		{addr: "localhost:5000", want: "http://127.0.0.1:5000"},
		{addr: "127.0.0.1:5001", want: "http://127.0.0.1:5001"},
		{addr: "[::1]:5000", want: "http://[::1]:5000"},
		{addr: "ghcr.io", want: "https://ghcr.io"},
		{addr: "https://index.docker.io/v1/", want: "https://registry-1.docker.io"},
		{addr: "http://reg.lan:5000", want: "http://reg.lan:5000"},
	} {
		if got := New(tc.addr).Base(); got != tc.want {
			t.Errorf("New(%q).Base() = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

// TestParseChallenge: quoted values keep their commas.
func TestParseChallenge(t *testing.T) {
	kind, p := parseChallenge(`Bearer realm="https://auth.example/token",service="reg",scope="repository:a:pull,push"`)
	if kind != "bearer" || p["realm"] != "https://auth.example/token" || p["service"] != "reg" ||
		p["scope"] != "repository:a:pull,push" {
		t.Errorf("parsed %q %v", kind, p)
	}
}
