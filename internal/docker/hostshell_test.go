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
	"testing"
)

// TestHostShellArgs pins the helper's argv (#59): every flag before the
// image, the image one argument, nsenter's namespaces after it.
func TestHostShellArgs(t *testing.T) {
	got, err := HostShellArgs("alpine:3")
	want := []string{
		"run", "--rm", "-it", "--privileged", "--pid=host", "--net=host",
		"--label", "dockmaster.helper=hostshell", "alpine:3",
		"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", "sh",
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("HostShellArgs = %q, %v\nwant %q", got, err, want)
	}
	for _, bad := range []string{"", " \t", "-v/:/h", "--privileged"} {
		if args, err := HostShellArgs(bad); err == nil || args != nil {
			t.Errorf("HostShellArgs(%q) = %q, %v; want refused", bad, args, err)
		}
	}
	if err := ValidHostShellImage("x;-y $(z)"); err != nil {
		t.Errorf("an odd but non-flag image was refused: %v", err)
	}
	if err := ValidHostShellImage("-x"); err == nil || !strings.Contains(err.Error(), "flag") {
		t.Errorf("a flag image: %v", err)
	}
}

// TestRootless reads rootless docker's and rootless podman's shared
// security option, and nothing else.
func TestRootless(t *testing.T) {
	for opts, want := range map[string]bool{
		"name=seccomp,profile=builtin|name=rootless|name=cgroupns": true,
		"name=rootless": true,
		"name=seccomp,profile=builtin|name=cgroupns": false,
		"name=rootlessish":                           false,
		"":                                           false,
	} {
		var list []string
		if opts != "" {
			list = strings.Split(opts, "|")
		}
		if got := rootless(list); got != want {
			t.Errorf("rootless(%q) = %v", list, got)
		}
	}
}

// TestNegotiateReadsRootless: Negotiate sets Rootless from the daemon's
// own /info, as a rootless podman socket reports it.
func TestNegotiateReadsRootless(t *testing.T) {
	version := regexp.MustCompile(`^/v[0-9.]+`)
	for _, want := range []bool{true, false} {
		opts := []string{"name=seccomp,profile=default"}
		if want {
			opts = append(opts, "name=rootless")
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Api-Version", "1.47")
			switch version.ReplaceAllString(r.URL.Path, "") {
			case "/_ping":
				_, _ = w.Write([]byte("OK"))
			case "/info":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"Name": "pm", "ServerVersion": "5.6.1", "OSType": "linux", "Architecture": "aarch64",
					"SecurityOptions": opts,
				})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		c, err := New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Negotiate(context.Background()); err != nil {
			t.Fatal(err)
		}
		srv.Close()
		if c.Rootless != want || c.Name != "pm" {
			t.Errorf("security options %q: Rootless %v, name %q", opts, c.Rootless, c.Name)
		}
	}
}
