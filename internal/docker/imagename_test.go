package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// TestContainersNameTheirImageIDs: a container created from an image ID —
// as cri-dockerd creates Kubernetes's — lists with the image's tag, or its
// repository by digest; one with neither keeps its ID. Each ID is
// inspected once across refreshes, except one the daemon failed to answer.
func TestContainersNameTheirImageIDs(t *testing.T) {
	images := map[string]map[string]any{
		"sha256:aaa": {"RepoTags": []string{"rancher/mirrored-coredns-coredns:1.12.3"}},
		"sha256:bbb": {"RepoTags": []string{}, "RepoDigests": []string{"rancher/klipper-lb@sha256:0123456789abcdef0123"}},
		"sha256:ccc": {"RepoTags": []string{"<none>:<none>"}},
	}
	var mu sync.Mutex
	inspected := map[string]int{}
	version := regexp.MustCompile(`^/v[0-9.]+`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		path := version.ReplaceAllString(r.URL.Path, "")
		switch {
		case path == "/_ping":
			_, _ = w.Write([]byte("OK"))
		case path == "/containers/json":
			imgs := []string{"sha256:aaa", "sha256:bbb", "sha256:ccc", "sha256:ddd", "nginx:1.27"}
			list := make([]map[string]any, 0, len(imgs))
			for i, img := range imgs {
				list = append(list, map[string]any{
					"Id": strings.Repeat(string(rune('a'+i)), 12), "Names": []string{"/c" + string(rune('0'+i))},
					"Image": img, "State": "running",
				})
			}
			_ = json.NewEncoder(w).Encode(list)
		case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/images/"), "/json")
			mu.Lock()
			inspected[id]++
			mu.Unlock()
			img, ok := images[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "No such image: " + id})
				return
			}
			img["Id"] = id
			_ = json.NewEncoder(w).Encode(img)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		cs, err := c.Containers(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, ct := range cs {
			got[ct.Name] = ct.Image
		}
		want := map[string]string{
			"c0": "rancher/mirrored-coredns-coredns:1.12.3",
			"c1": "rancher/klipper-lb@sha256:0123456789ab",
			"c2": "sha256:ccc",
			"c3": "sha256:ddd",
			"c4": "nginx:1.27",
		}
		for name, img := range want {
			if got[name] != img {
				t.Errorf("%s image = %q, want %q", name, got[name], img)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for id, want := range map[string]int{"sha256:aaa": 1, "sha256:bbb": 1, "sha256:ccc": 1, "sha256:ddd": 2} {
		if inspected[id] != want {
			t.Errorf("%s inspected %d times, want %d", id, inspected[id], want)
		}
	}
	if inspected["nginx:1.27"] != 0 {
		t.Error("a named image was inspected")
	}
}
