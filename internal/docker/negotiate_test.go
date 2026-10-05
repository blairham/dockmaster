// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// TestNegotiateReadsHostSize: the startup Info call fills the host's CPUs
// and memory beside its version, so the header's CPU/MEM line needs no
// call of its own.
func TestNegotiateReadsHostSize(t *testing.T) {
	version := regexp.MustCompile(`^/v[0-9.]+`)
	var infos int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		switch version.ReplaceAllString(r.URL.Path, "") {
		case "/_ping":
			_, _ = w.Write([]byte("OK"))
		case "/info":
			infos++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ServerVersion": "29.5.2", "OSType": "linux", "Architecture": "aarch64",
				"NCPU": 14, "MemTotal": 16 << 30,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Negotiate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.NCPU != 14 || c.MemTotal != 16<<30 || c.Version != "29.5.2" || infos != 1 {
		t.Errorf("NCPU %d MemTotal %d Version %q after %d info calls", c.NCPU, c.MemTotal, c.Version, infos)
	}
}
