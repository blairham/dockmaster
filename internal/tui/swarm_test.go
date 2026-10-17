// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// swarmFixture is what the fake manager lists: a replicated web at 2/3, a
// global agent, two nodes, a secret and a config.
func swarmFixture() map[string]any {
	now := time.Now()
	return map[string]any{
		"/services": []map[string]any{
			{
				"ID": "svcweb", "Version": map[string]any{"Index": 4},
				"Spec": map[string]any{
					"Name": "web", "Mode": map[string]any{"Replicated": map[string]any{"Replicas": 3}},
					"TaskTemplate": map[string]any{"ContainerSpec": map[string]any{"Image": "nginx:1.27"}},
				},
				"ServiceStatus": map[string]any{"RunningTasks": 2, "DesiredTasks": 3},
			},
			{
				"ID": "svcagent", "Spec": map[string]any{
					"Name": "agent", "Mode": map[string]any{"Global": map[string]any{}},
					"TaskTemplate": map[string]any{"ContainerSpec": map[string]any{"Image": "agent:7"}},
				},
				"ServiceStatus": map[string]any{"RunningTasks": 2, "DesiredTasks": 2},
			},
		},
		"/services/svcweb": map[string]any{
			"ID": "svcweb", "Version": map[string]any{"Index": 4},
			"Spec": map[string]any{"Name": "web", "Mode": map[string]any{"Replicated": map[string]any{"Replicas": 3}}},
		},
		"/tasks": []map[string]any{
			{
				"ID": "t1", "ServiceID": "svcweb", "Slot": 1, "NodeID": "n1", "DesiredState": "running",
				"Status": map[string]any{"State": "running", "Timestamp": now},
			},
			{
				"ID": "t0", "ServiceID": "svcweb", "Slot": 1, "NodeID": "n1", "DesiredState": "shutdown",
				"Status": map[string]any{"State": "failed", "Err": "task: non-zero exit (137)", "Timestamp": now.Add(-time.Hour)},
			},
		},
		"/nodes": []map[string]any{
			{
				"ID": "n1", "Description": map[string]any{"Hostname": "alpha"},
				"Spec":   map[string]any{"Role": "manager", "Availability": "active"},
				"Status": map[string]any{"State": "ready"}, "ManagerStatus": map[string]any{"Leader": true},
			},
			{
				"ID": "n2", "Description": map[string]any{"Hostname": "beta"},
				"Spec": map[string]any{"Role": "worker", "Availability": "drain"}, "Status": map[string]any{"State": "down"},
			},
		},
		"/nodes/n2": map[string]any{
			"ID": "n2", "Version": map[string]any{"Index": 11},
			"Spec": map[string]any{"Role": "worker", "Availability": "drain"},
		},
		"/secrets": []map[string]any{{"ID": "sec1", "Spec": map[string]any{"Name": "db_pass"}}},
		"/configs": []map[string]any{{"ID": "cfg1", "Spec": map[string]any{"Name": "nginx.conf"}}},
	}
}

// swarmManager is a fake swarm manager answering swarmFixture and
// recording every request as "METHOD /path?query body".
func swarmManager(t *testing.T) (*docker.Client, func() []string) {
	t.Helper()
	answers := swarmFixture()
	var mu sync.Mutex
	var calls []string
	version := regexp.MustCompile(`^/v[0-9.]+`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		path := version.ReplaceAllString(r.URL.Path, "")
		if path == "/_ping" {
			_, _ = w.Write([]byte("OK"))
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, strings.TrimSpace(r.Method+" "+path+"?"+r.URL.RawQuery+" "+string(body)))
		mu.Unlock()
		if a, ok := answers[path]; ok {
			_ = json.NewEncoder(w).Encode(a)
			return
		}
		_, _ = w.Write([]byte("{}"))
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

// swarmApp is the app on a fake manager, showing command's view loaded.
func swarmApp(t *testing.T, opts Options, command string) (*App, func() []string) {
	t.Helper()
	c, calls := swarmManager(t)
	opts.Version = "test"
	a := NewApp(c, opts)
	a.splashActive = false
	a.loading = false
	step(a, tea.WindowSizeMsg{Width: 200, Height: 40})
	msg, cmd := a.dispatchCommand(command)
	if msg != "" {
		t.Fatalf(":%s: %s", command, msg)
	}
	runCmd(a, cmd)
	return a, calls
}

// posts is the non-GET requests the manager saw.
func posts(calls []string) []string {
	var out []string
	for _, c := range calls {
		if !strings.HasPrefix(c, http.MethodGet) {
			out = append(out, c)
		}
	}
	return out
}

// selectRow moves the cursor to the row whose rendered line holds name.
func selectRow(t *testing.T, a *App, name string) {
	t.Helper()
	sv := typedView[*views.SwarmView](a, a.view)
	for range sv.Count() {
		if n, _, _ := sv.CopyFields(); n == name {
			return
		}
		step(a, key("j"))
	}
	t.Fatalf("no row %q:\n%s", name, render(a))
}

// TestSwarmViewsList: each view lists its resource as the docker CLI's ls
// does, from the manager.
func TestSwarmViewsList(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    []string
		view    style.ViewType
	}{
		{
			command: "svc", view: style.ViewServices,
			want: []string{"agent", "global", "web", "replicated", "2/3", "nginx:1.27"},
		},
		{command: "tasks", view: style.ViewTasks, want: []string{"web.1", "alpha", "running"}},
		{command: "nodes", view: style.ViewSwarmNodes, want: []string{"alpha", "Leader", "beta", "down", "drain"}},
		{command: "secrets", view: style.ViewSecrets, want: []string{"db_pass"}},
		{command: "configs", view: style.ViewConfigs, want: []string{"nginx.conf"}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			a, _ := swarmApp(t, Options{}, tc.command)
			if a.view != tc.view {
				t.Fatalf(":%s opened %s", tc.command, style.ViewName(a.view))
			}
			out := render(a)
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf(":%s is missing %q:\n%s", tc.command, w, out)
				}
			}
		})
	}
}

// TestTasksHideHistoryUntilAsked: a shutdown task is history — hidden
// until a, shown after, and the title says so.
func TestTasksHideHistoryUntilAsked(t *testing.T) {
	a, _ := swarmApp(t, Options{}, "tasks")
	if out := render(a); strings.Contains(out, "non-zero exit") {
		t.Fatalf("history shown before a:\n%s", out)
	}
	step(a, key("a"))
	if out := render(a); !strings.Contains(out, "non-zero exit (137)") || !strings.Contains(out, "history") {
		t.Fatalf("a did not show the history:\n%s", out)
	}
}

// TestEnterOnServiceScopesTasks: enter on a service opens its tasks, asked
// of the manager by filter and named in the title; esc goes back and the
// next :tasks lists every task again.
func TestEnterOnServiceScopesTasks(t *testing.T) {
	a, calls := swarmApp(t, Options{}, "services")
	selectRow(t, a, "web")
	runCmd(a, step(a, key("enter")))
	if a.view != style.ViewTasks || !strings.Contains(render(a), "of service web") {
		t.Fatalf("enter did not open web's tasks:\n%s", render(a))
	}
	last := ""
	for _, c := range calls() {
		if strings.HasPrefix(c, "GET /tasks") {
			last = c
		}
	}
	if !strings.Contains(last, "svcweb") {
		t.Errorf("tasks not asked for by service: %q", last)
	}
	step(a, key("esc"))
	if a.view != style.ViewServices {
		t.Fatalf("esc went to %s", style.ViewName(a.view))
	}
	if tv := typedView[*views.SwarmView](a, style.ViewTasks); tv.Status() != "" {
		t.Errorf("the scope outlived the drill-in: %q", tv.Status())
	}
}

// TestScaleServiceDownConfirms: s opens the scale form prefilled with the
// desired count; a lower count confirms, naming the change, then updates
// the service at the version read.
func TestScaleServiceDownConfirms(t *testing.T) {
	a, calls := swarmApp(t, Options{}, "services")
	selectRow(t, a, "web")
	step(a, key("s"))
	if a.view != style.ViewScaleForm || !strings.Contains(render(a), "now 3") {
		t.Fatalf("s did not open the form on 3:\n%s", render(a))
	}
	setReplicas(a, "1")
	step(a, key("enter"))
	if !a.confirm.Active() || !strings.Contains(render(a), "from 3 to 1? 2 tasks are stopped") {
		t.Fatalf("a scale down did not confirm:\n%s", render(a))
	}
	if p := posts(calls()); len(p) != 0 {
		t.Fatalf("updated before the confirm: %q", p)
	}
	runCmd(a, step(a, key("y")))
	p := posts(calls())
	if len(p) != 1 || !strings.Contains(p[0], "/services/svcweb/update?version=4") ||
		!strings.Contains(p[0], `"Replicas":1`) {
		t.Fatalf("update = %q", p)
	}
	if !strings.Contains(a.flash, "scaled to 1 web") {
		t.Errorf("flash = %q", a.flash)
	}
}

// TestScaleGlobalServiceRefused: a global service has no count; s says so
// and opens nothing.
func TestScaleGlobalServiceRefused(t *testing.T) {
	a, _ := swarmApp(t, Options{}, "services")
	selectRow(t, a, "agent")
	step(a, key("s"))
	if a.view != style.ViewServices || !strings.Contains(a.errFlash, "agent is a global service") {
		t.Fatalf("view %s, flash %q", style.ViewName(a.view), a.errFlash)
	}
}

// TestDrainConfirmsActivateDoesNot: x on a node confirms, as its tasks
// move; u runs at once. Both post the node's spec at its version.
func TestDrainConfirmsActivateDoesNot(t *testing.T) {
	a, calls := swarmApp(t, Options{}, "nodes")
	selectRow(t, a, "beta")
	step(a, key("x"))
	if !a.confirm.Active() || !strings.Contains(render(a), "drain node beta?") {
		t.Fatalf("x did not confirm:\n%s", render(a))
	}
	runCmd(a, step(a, key("y")))
	runCmd(a, step(a, key("u")))
	p := posts(calls())
	if len(p) != 2 || !strings.Contains(p[0], `"Availability":"drain"`) ||
		!strings.Contains(p[1], `"Availability":"active"`) {
		t.Fatalf("updates = %q", p)
	}
	if !strings.Contains(p[0], "/nodes/n2/update?version=11") {
		t.Errorf("drain not at the node's version: %q", p[0])
	}
}

// TestRemoveSecretConfirms: ctrl-d on a secret asks, warning the value
// cannot be read back, then deletes it.
func TestRemoveSecretConfirms(t *testing.T) {
	a, calls := swarmApp(t, Options{}, "secrets")
	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(render(a), "remove secret db_pass?") {
		t.Fatalf("ctrl-d did not confirm:\n%s", render(a))
	}
	runCmd(a, step(a, key("y")))
	if p := posts(calls()); len(p) != 1 || !strings.HasPrefix(p[0], "DELETE /secrets/sec1") {
		t.Fatalf("calls = %q", p)
	}
}

// TestReadonlyRefusesSwarmChanges: every key that changes the swarm is
// refused under --readonly, before any confirm or request; inspect is not.
func TestReadonlyRefusesSwarmChanges(t *testing.T) {
	for _, tc := range []struct {
		command, row, key string
	}{
		{command: "services", row: "web", key: "s"},
		{command: "services", row: "web", key: "R"},
		{command: "services", row: "web", key: "ctrl+d"},
		{command: "nodes", row: "beta", key: "x"},
		{command: "nodes", row: "beta", key: "u"},
		{command: "nodes", row: "beta", key: "ctrl+d"},
		{command: "secrets", row: "db_pass", key: "ctrl+d"},
		{command: "configs", row: "nginx.conf", key: "ctrl+d"},
	} {
		t.Run(tc.command+"/"+tc.key, func(t *testing.T) {
			a, calls := swarmApp(t, Options{ReadOnly: true}, tc.command)
			selectRow(t, a, tc.row)
			view := a.view
			runCmd(a, step(a, key(tc.key)))
			if a.confirm.Active() || a.view != view || !strings.Contains(a.errFlash, "readonly mode") {
				t.Fatalf("not refused: view %s, confirm %v, flash %q", style.ViewName(a.view), a.confirm.Active(), a.errFlash)
			}
			if p := posts(calls()); len(p) != 0 {
				t.Fatalf("reached the manager: %q", p)
			}
		})
	}
	a, _ := swarmApp(t, Options{ReadOnly: true}, "services")
	step(a, key("o"))
	if a.view != style.ViewInspect {
		t.Errorf("readonly refused inspect: %s, %q", style.ViewName(a.view), a.errFlash)
	}
}

// TestSwarmListingReachesItsViewAfterLeaving: a listing that lands after
// its view was left still clears that view's single-flight guard, so it
// polls again when it is back.
func TestSwarmListingReachesItsViewAfterLeaving(t *testing.T) {
	a, calls := swarmApp(t, Options{}, "services")
	sv := typedView[*views.SwarmView](a, style.ViewServices)
	cmd := sv.Refresh()
	if cmd == nil {
		t.Fatal("no refresh to hold")
	}
	msg := cmd()
	step(a, key("0"))
	step(a, msg)
	before := len(calls())
	if sv.Refresh() == nil {
		t.Fatalf("the services view is still in flight; calls %d", before)
	}
}
