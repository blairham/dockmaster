// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// swarmCall is one request a fake manager saw.
type swarmCall struct {
	Method, Path, Query, Body string
}

// swarmDaemon is a fake swarm manager: answers maps a path to the JSON it
// returns (anything else is 200 with {}), and every request but the
// ping is recorded, the API version prefix dropped.
func swarmDaemon(t *testing.T, answers map[string]any) (*Client, func() []swarmCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []swarmCall
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
		calls = append(calls, swarmCall{Method: r.Method, Path: path, Query: r.URL.RawQuery, Body: string(body)})
		mu.Unlock()
		if a, ok := answers[path]; ok {
			_ = json.NewEncoder(w).Encode(a)
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)
	c, err := New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return c, func() []swarmCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]swarmCall(nil), calls...)
	}
}

// TestServicesListAsServiceLs: mode, RUNNING/DESIRED, the image without
// its pinned digest, published ports and the stack, sorted by name — and
// the list asks for the status block that carries the counts.
func TestServicesListAsServiceLs(t *testing.T) {
	c, calls := swarmDaemon(t, map[string]any{"/services": []map[string]any{
		{
			"ID": "svcweb000000000", "Spec": map[string]any{
				"Name":         "shop_web",
				"Labels":       map[string]string{LabelStack: "shop"},
				"Mode":         map[string]any{"Replicated": map[string]any{"Replicas": 3}},
				"TaskTemplate": map[string]any{"ContainerSpec": map[string]any{"Image": "nginx:1.27@sha256:abc"}},
			},
			"Endpoint": map[string]any{"Ports": []map[string]any{
				{"Protocol": "tcp", "TargetPort": 80, "PublishedPort": 8080, "PublishMode": "ingress"},
			}},
			"ServiceStatus": map[string]any{"RunningTasks": 2, "DesiredTasks": 3},
			"UpdateStatus":  map[string]any{"State": "updating"},
		},
		{
			"ID": "svcagent0000000", "Spec": map[string]any{
				"Name": "agent", "Mode": map[string]any{"Global": map[string]any{}},
				"TaskTemplate": map[string]any{"ContainerSpec": map[string]any{"Image": "datadog/agent:7"}},
			},
			"ServiceStatus": map[string]any{"RunningTasks": 1, "DesiredTasks": 1},
		},
	}})
	got, err := c.Services(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "agent" || got[1].Name != "shop_web" {
		t.Fatalf("services not sorted by name: %+v", got)
	}
	agent, web := got[0], got[1]
	if agent.Mode != ModeGlobal || agent.Scalable() {
		t.Errorf("agent: mode %q scalable %v, want global and not scalable", agent.Mode, agent.Scalable())
	}
	if web.Mode != ModeReplicated || !web.Scalable() || web.Replicas() != "2/3" {
		t.Errorf("web: mode %q replicas %q", web.Mode, web.Replicas())
	}
	if web.Image != "nginx:1.27" || web.Ports != "*:8080->80/tcp" || web.Stack != "shop" || web.Update != "updating" {
		t.Errorf("web row: %+v", web)
	}
	if q := calls()[0].Query; !strings.Contains(q, "status=true") {
		t.Errorf("listing did not ask for the status block: %q", q)
	}
}

// TestScaleServiceWritesBackAtItsVersion: a scale reads the spec, sets the
// count and posts it at the version it read — anything else the manager
// refuses as out of date — and keeps the rest of the spec as it was.
func TestScaleServiceWritesBackAtItsVersion(t *testing.T) {
	c, calls := swarmDaemon(t, map[string]any{"/services/svcweb": map[string]any{
		"ID": "svcweb", "Version": map[string]any{"Index": 42},
		"Spec": map[string]any{
			"Name": "web", "Mode": map[string]any{"Replicated": map[string]any{"Replicas": 3}},
			"TaskTemplate": map[string]any{"ContainerSpec": map[string]any{"Image": "nginx:1.27"}},
		},
	}})
	if err := c.ScaleService(context.Background(), "svcweb", 5); err != nil {
		t.Fatal(err)
	}
	cs := calls()
	if len(cs) != 2 || cs[1].Method != http.MethodPost || cs[1].Path != "/services/svcweb/update" {
		t.Fatalf("calls: %+v", cs)
	}
	if !strings.Contains(cs[1].Query, "version=42") {
		t.Errorf("update not at the version read: %q", cs[1].Query)
	}
	var spec struct {
		TaskTemplate struct{ ContainerSpec struct{ Image string } }
		Mode         struct{ Replicated struct{ Replicas int } }
	}
	if err := json.Unmarshal([]byte(cs[1].Body), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Mode.Replicated.Replicas != 5 || spec.TaskTemplate.ContainerSpec.Image != "nginx:1.27" {
		t.Errorf("update body: %s", cs[1].Body)
	}
}

// TestScaleRefusesAGlobalService: a global service has no count, so the
// scale is refused before any update is sent.
func TestScaleRefusesAGlobalService(t *testing.T) {
	c, calls := swarmDaemon(t, map[string]any{"/services/agent": map[string]any{
		"ID": "agent", "Spec": map[string]any{"Name": "agent", "Mode": map[string]any{"Global": map[string]any{}}},
	}})
	err := c.ScaleService(context.Background(), "agent", 2)
	if !errors.Is(err, ErrNotScalable) || !strings.Contains(err.Error(), "global") {
		t.Fatalf("err = %v, want ErrNotScalable naming the mode", err)
	}
	for _, call := range calls() {
		if call.Method == http.MethodPost {
			t.Fatalf("sent an update: %+v", call)
		}
	}
}

// TestRestartServiceBumpsForceUpdate: a restart is the spec unchanged but
// for ForceUpdate, one more than it was — `service update --force`.
func TestRestartServiceBumpsForceUpdate(t *testing.T) {
	c, calls := swarmDaemon(t, map[string]any{"/services/web": map[string]any{
		"ID": "web", "Version": map[string]any{"Index": 7},
		"Spec": map[string]any{"Name": "web", "TaskTemplate": map[string]any{"ForceUpdate": 2}},
	}})
	if err := c.RestartService(context.Background(), "web"); err != nil {
		t.Fatal(err)
	}
	cs := calls()
	if len(cs) != 2 || !strings.Contains(cs[1].Body, `"ForceUpdate":3`) {
		t.Fatalf("update: %+v", cs)
	}
}

// TestTasksNameAsServicePs: service.slot for a replicated task and
// service.node for a global one, the node by hostname, history after the
// live task in each slot, and the scope sent as filters.
func TestTasksNameAsServicePs(t *testing.T) {
	now := time.Now()
	c, calls := swarmDaemon(t, map[string]any{
		"/tasks": []map[string]any{
			{
				"ID": "old", "ServiceID": "s1", "Slot": 1, "NodeID": "n1", "DesiredState": "shutdown",
				"Status": map[string]any{"State": "failed", "Err": "task: non-zero exit (1)", "Timestamp": now.Add(-time.Hour)},
			},
			{
				"ID": "new", "ServiceID": "s1", "Slot": 1, "NodeID": "n1", "DesiredState": "running",
				"Status": map[string]any{
					"State": "running", "Timestamp": now.Add(-time.Minute),
					"ContainerStatus": map[string]any{"ContainerID": "ctr1"},
				},
				"Spec": map[string]any{"ContainerSpec": map[string]any{"Image": "nginx:1.27@sha256:abc"}},
			},
			{
				"ID": "g", "ServiceID": "s2", "NodeID": "n2", "DesiredState": "running",
				"Status": map[string]any{"State": "running", "Timestamp": now},
			},
		},
		"/services": []map[string]any{
			{"ID": "s1", "Spec": map[string]any{"Name": "web"}},
			{"ID": "s2", "Spec": map[string]any{"Name": "agent"}},
		},
		"/nodes": []map[string]any{
			{"ID": "n1", "Description": map[string]any{"Hostname": "alpha"}},
			{"ID": "n2", "Description": map[string]any{"Hostname": "beta"}},
		},
	})
	got, err := c.Tasks(context.Background(), TaskScope{Service: "web"})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(got))
	for _, tk := range got {
		names = append(names, tk.Name+"/"+tk.ID)
	}
	if want := "agent.n2/g web.1/new web.1/old"; strings.Join(names, " ") != want {
		t.Fatalf("tasks = %q, want %q", strings.Join(names, " "), want)
	}
	live, old := got[1], got[2]
	if live.Node != "alpha" || live.Image != "nginx:1.27" || live.ContainerID != "ctr1" || !live.Live() {
		t.Errorf("live task: %+v", live)
	}
	if old.Live() || old.Err == "" {
		t.Errorf("history task: %+v", old)
	}
	if q := calls()[0].Query; !strings.Contains(q, "service") || !strings.Contains(q, "web") {
		t.Errorf("scope not sent as a filter: %q", q)
	}
}

// TestSetNodeAvailability: drain is the node's spec with Availability
// changed, posted at the version read; a word that is not an availability
// is refused before the daemon is asked anything.
func TestSetNodeAvailability(t *testing.T) {
	c, calls := swarmDaemon(t, map[string]any{"/nodes/n1": map[string]any{
		"ID": "n1", "Version": map[string]any{"Index": 9},
		"Spec":        map[string]any{"Role": "worker", "Availability": "active", "Labels": map[string]string{"zone": "a"}},
		"Description": map[string]any{"Hostname": "alpha"},
	}})
	if err := c.SetNodeAvailability(context.Background(), "n1", "drain"); err != nil {
		t.Fatal(err)
	}
	cs := calls()
	if len(cs) != 2 || cs[1].Path != "/nodes/n1/update" || !strings.Contains(cs[1].Query, "version=9") {
		t.Fatalf("calls: %+v", cs)
	}
	for _, want := range []string{`"Availability":"drain"`, `"Role":"worker"`, `"zone":"a"`} {
		if !strings.Contains(cs[1].Body, want) {
			t.Errorf("update body is missing %s: %s", want, cs[1].Body)
		}
	}
	if err := c.SetNodeAvailability(context.Background(), "n1", "cordon"); err == nil {
		t.Error("cordon accepted as an availability")
	}
	if len(calls()) != 2 {
		t.Errorf("a bad availability reached the daemon: %+v", calls()[2:])
	}
}

// TestSwarmNodesMarkSelfAndLeader: the daemon's own node is marked, and
// the manager status reads as node ls prints it.
func TestSwarmNodesMarkSelfAndLeader(t *testing.T) {
	c, _ := swarmDaemon(t, map[string]any{"/nodes": []map[string]any{
		{
			"ID": "n2", "Description": map[string]any{"Hostname": "beta"}, "Spec": map[string]any{"Role": "worker"},
			"Status": map[string]any{"State": "down"},
		},
		{
			"ID": "n1", "Description": map[string]any{"Hostname": "alpha", "Engine": map[string]any{"EngineVersion": "29.5.2"}},
			"Spec":          map[string]any{"Role": "manager", "Availability": "active"},
			"Status":        map[string]any{"State": "ready"},
			"ManagerStatus": map[string]any{"Leader": true, "Reachability": "reachable"},
		},
	}})
	c.SwarmNodeID = "n1"
	got, err := c.SwarmNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Hostname != "alpha" {
		t.Fatalf("nodes: %+v", got)
	}
	if !got[0].Self || got[0].Manager != "Leader" || got[0].Engine != "29.5.2" {
		t.Errorf("alpha: %+v", got[0])
	}
	if got[1].Self || got[1].Manager != "" || got[1].State != "down" {
		t.Errorf("beta: %+v", got[1])
	}
}

// TestSecretsAndConfigsListMetadata: names, stacks and drivers, sorted —
// and nothing of a value, which the list does not carry.
func TestSecretsAndConfigsListMetadata(t *testing.T) {
	c, _ := swarmDaemon(t, map[string]any{
		"/secrets": []map[string]any{
			{"ID": "s2", "Spec": map[string]any{"Name": "tls", "Driver": map[string]any{"Name": "vault"}}},
			{"ID": "s1", "Spec": map[string]any{"Name": "db_pass", "Labels": map[string]string{LabelStack: "shop"}}},
		},
		"/configs": []map[string]any{
			{"ID": "c1", "Spec": map[string]any{"Name": "nginx.conf", "Templating": map[string]any{"Name": "golang"}}},
		},
	})
	secrets, err := c.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 2 || secrets[0].Name != "db_pass" || secrets[0].Stack != "shop" || secrets[1].Driver != "vault" {
		t.Errorf("secrets: %+v", secrets)
	}
	configs, err := c.Configs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || configs[0].Name != "nginx.conf" || configs[0].Driver != "golang" {
		t.Errorf("configs: %+v", configs)
	}
}
