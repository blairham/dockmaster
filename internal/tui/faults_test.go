// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// faultyContainers adds a crashed and an unhealthy container to the
// sample's healthy, running-clean and exited-clean ones.
func faultyContainers() []docker.Container {
	now := time.Now().Add(-time.Hour)
	return append(sampleContainers(),
		docker.Container{
			ID: "eeeeeeeeeeee5555", Name: "crashed", Image: "app", State: "exited",
			Status: "Exited (137) 5 minutes ago", Created: now,
		},
		docker.Container{
			ID: "ffffffffffff6666", Name: "sick", Image: "app", State: "running",
			Health: "unhealthy", Status: "Up 1 hour (unhealthy)", Created: now,
		},
	)
}

// TestFaultsFilter: ctrl+z lists only the containers in trouble, says so
// in the title, and brings everything back on a second press.
func TestFaultsFilter(t *testing.T) {
	a := newTestApp(t)
	step(a, views.ContainersRefreshMsg{Containers: faultyContainers()})
	step(a, key("ctrl+z"))
	out := render(a)
	if !strings.Contains(out, "crashed") || !strings.Contains(out, "sick") ||
		strings.Contains(out, "migrate") || strings.Contains(out, " web ") {
		t.Errorf("faults only:\n%s", out)
	}
	if !strings.Contains(out, "faults") || a.flash != "faults only on" {
		t.Errorf("title or flash (%q):\n%s", a.flash, out)
	}
	step(a, key("ctrl+z"))
	if out := render(a); !strings.Contains(out, "migrate") || a.flash != "faults only off" {
		t.Errorf("faults off:\n%s", out)
	}
}

// TestFaultsWithNone: a daemon with nothing wrong says so — and ctrl+z
// still works on the empty list it leaves.
func TestFaultsWithNone(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	step(a, key("ctrl+z"))
	if out := render(a); !strings.Contains(out, "no faults") {
		t.Errorf("no faults:\n%s", out)
	}
	step(a, key("ctrl+z"))
	if out := render(a); !strings.Contains(out, "web") {
		t.Errorf("ctrl+z on the empty faults list did not restore:\n%s", out)
	}
}

// TestFaultsFetchStoppedContainers: a crashed container has exited, so
// faults asks docker for stopped ones too even with the stopped/all
// toggle off — without changing that toggle.
func TestFaultsFetchStoppedContainers(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			mu.Lock()
			queries = append(queries, r.URL.Query().Get("all"))
			mu.Unlock()
		}
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)
	c, err := docker.New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(c, Options{Version: "test"})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 40})
	step(a, views.ContainersRefreshMsg{})
	runOnce(a, step(a, key("ctrl+z")))
	mu.Lock()
	defer mu.Unlock()
	if len(queries) == 0 || queries[len(queries)-1] != "1" {
		t.Errorf("faults listed with all=%q, want stopped containers too (all=1)", queries)
	}
	if a.showAll {
		t.Error("faults switched the stopped/all toggle on")
	}
}
