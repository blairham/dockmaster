// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeDaemon is a docker API that answers every call with success and
// records it as "METHOD /path", the API version prefix dropped.
func fakeDaemon(t *testing.T) (*docker.Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	version := regexp.MustCompile(`^/v[0-9.]+`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		if r.URL.Path == "/_ping" {
			_, _ = w.Write([]byte("OK"))
			return
		}
		mu.Lock()
		calls = append(calls, r.Method+" "+version.ReplaceAllString(r.URL.Path, ""))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	c, err := docker.New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

// markedApp is the containers view on the sample containers, against a
// fake daemon, with api and migrate marked.
func markedApp(t *testing.T, opts Options) (*App, func() []string) {
	t.Helper()
	a := NewApp(nil, opts)
	a.splashActive = false
	a.loading = false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 44})
	c, calls := fakeDaemon(t)
	a.client = c
	loadContainers(a)
	step(a, key("down"))
	step(a, key("space"))
	step(a, key("down"))
	step(a, key("space"))
	if a.flash != "2 marked" {
		t.Fatalf("marking: flash %q", a.flash)
	}
	return a, calls
}

func TestMarkedRowsStopTogether(t *testing.T) {
	a, calls := markedApp(t, Options{Version: "test"})
	v := typedView[*views.ContainersView](a, style.ViewContainers)

	runOnce(a, step(a, key("x")))
	want := "POST /containers/bbbbbbbbbbbb2222/stop|POST /containers/cccccccccccc3333/stop"
	if got := strings.Join(calls(), "|"); got != want {
		t.Errorf("x with two marked: %s\nwant %s", got, want)
	}
	if v.MarkCount() != 0 {
		t.Errorf("marks left after the action: %d", v.MarkCount())
	}
}

func TestMarkedRowsRemoveAfterOneQuestion(t *testing.T) {
	a, calls := markedApp(t, Options{Version: "test"})
	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(render(a), "remove 2 containers: api, migrate?") {
		t.Fatalf("ctrl-d with two marked did not ask once about both:\n%s", render(a))
	}
	if len(calls()) != 0 {
		t.Fatalf("removed before the answer: %q", calls())
	}
	runOnce(a, step(a, key("y")))
	got := strings.Join(calls(), "|")
	if !strings.Contains(got, "DELETE /containers/bbbbbbbbbbbb2222") ||
		!strings.Contains(got, "DELETE /containers/cccccccccccc3333") ||
		strings.Count(got, "DELETE") != 2 {
		t.Errorf("after yes: %s", got)
	}
}

func TestMarksFollowRowsAndSpareOtherKeys(t *testing.T) {
	a, _ := markedApp(t, Options{Version: "test"})
	v := typedView[*views.ContainersView](a, style.ViewContainers)

	// A sort moves the rows; the marks go with them.
	step(a, key("shift+right"))
	acts := v.BulkKey("x")
	names := make([]string, 0, len(acts))
	for _, act := range acts {
		names = append(names, act.Label)
	}
	if strings.Join(names, ",") != "api,migrate" {
		t.Errorf("after a sort the marks are on %v", names)
	}

	// A marked row is drawn in the mark color — off the cursor, which
	// repaints its own row.
	if !strings.Contains(renderStyled(a), views.Theme().MarkStyle.Render("api")) {
		t.Error("a marked row is not drawn as marked")
	}

	// enter is not a bulk key: it opens the cursor row's logs.
	step(a, key("enter"))
	if a.view != style.ViewLogs {
		t.Errorf("enter with rows marked went to %v", a.view)
	}

	// ctrl+\ clears.
	step(a, key("esc"))
	step(a, key("ctrl+\\"))
	if v.MarkCount() != 0 {
		t.Errorf("ctrl-\\ left %d marked", v.MarkCount())
	}
}

func TestMarkedRowsReadonly(t *testing.T) {
	for _, k := range []string{"x", "ctrl+d"} {
		a, calls := markedApp(t, Options{Version: "test", ReadOnly: true})
		v := typedView[*views.ContainersView](a, style.ViewContainers)
		runOnce(a, step(a, key(k)))
		if !strings.Contains(a.errFlash, "readonly") || a.confirm.Active() {
			t.Errorf("%s with marks in readonly: err %q", k, a.errFlash)
		}
		if len(calls()) != 0 || v.MarkCount() != 2 {
			t.Errorf("%s in readonly: called %q, %d still marked", k, calls(), v.MarkCount())
		}
	}
}
