package docker

import (
	"strings"
	"testing"
	"time"
)

// crictlPS is the shape of `crictl ps -a -o json` from a kind v1.35 node,
// trimmed to three containers and the fields dockmaster reads.
const crictlPS = `{"containers":[
 {"id":"432f5e11b60d","metadata":{"name":"sxbet-rest-oegw","attempt":0},
  "image":{"image":"sha256:86fc","userSpecifiedImage":"legate-sxbet-rest-oegw:a286feffbc32"},
  "state":"CONTAINER_RUNNING","createdAt":"1790883447360266905",
  "labels":{"io.kubernetes.pod.name":"sxbet-rest-oegw-79fdbc5c9d-4bnrj-x-trading-x-rig","io.kubernetes.pod.namespace":"k5s-legate"}},
 {"id":"47871414167f","metadata":{"name":"operator","attempt":632},
  "image":{"image":"quay.io/prometheus-operator/prometheus-operator:v0.92.0"},
  "state":"CONTAINER_EXITED","createdAt":"1790818294364780042",
  "labels":{"io.kubernetes.pod.name":"prom-op","io.kubernetes.pod.namespace":"monitoring"}},
 {"id":"aaaa","metadata":{"name":"coredns","attempt":1},
  "image":{"image":"registry.k8s.io/coredns/coredns:v1.12.0"},
  "state":"CONTAINER_RUNNING","createdAt":"1790818000000000000",
  "labels":{"io.kubernetes.pod.name":"coredns-1","io.kubernetes.pod.namespace":"kube-system"}}
]}`

func TestParseCrictlPS(t *testing.T) {
	list, err := parseCrictlPS([]byte(crictlPS))
	if err != nil {
		t.Fatal(err)
	}
	order := make([]string, 0, len(list))
	for _, n := range list {
		order = append(order, n.Namespace+"/"+n.Name)
	}
	if got := strings.Join(order, " "); got != "k5s-legate/sxbet-rest-oegw kube-system/coredns monitoring/operator" {
		t.Errorf("order = %s, want sorted by namespace then pod", got)
	}
	rig := list[0]
	if rig.Pod != "sxbet-rest-oegw-79fdbc5c9d-4bnrj-x-trading-x-rig" || rig.State != "running" ||
		rig.Image != "legate-sxbet-rest-oegw:a286feffbc32" || rig.ID != "432f5e11b60d" {
		t.Errorf("rig = %+v", rig)
	}
	if want := time.Unix(0, 1790883447360266905); !rig.Created.Equal(want) {
		t.Errorf("created = %v, want %v", rig.Created, want)
	}
	op := list[2]
	if op.State != "exited" || op.Attempt != 632 || op.Image != "quay.io/prometheus-operator/prometheus-operator:v0.92.0" {
		t.Errorf("operator = %+v (state, restarts, image fallback)", op)
	}
	if _, err := parseCrictlPS([]byte("crictl: command not found")); err == nil {
		t.Error("garbage parsed without error")
	}
}

func TestNodeRole(t *testing.T) {
	for _, tc := range []struct {
		labels map[string]string
		role   string
		ok     bool
	}{
		{labels: map[string]string{"io.x-k8s.kind.role": "worker", "io.x-k8s.kind.cluster": "k8s"}, role: "worker", ok: true},
		{labels: map[string]string{"io.x-k8s.kind.role": "control-plane"}, role: "control-plane", ok: true},
		{labels: map[string]string{"k3d.role": "server"}, role: "server", ok: true},
		{labels: map[string]string{"k3d.role": "agent"}, role: "agent", ok: true},
		{labels: map[string]string{"k3d.role": "loadbalancer"}}, // no runtime inside
		{labels: map[string]string{"k3d.role": "registry"}},
		{labels: map[string]string{}}, // kind-registry: a plain registry:2
	} {
		role, ok := NodeRole(Container{Labels: tc.labels})
		if role != tc.role || ok != tc.ok {
			t.Errorf("%v: %q %v, want %q %v", tc.labels, role, ok, tc.role, tc.ok)
		}
	}
}

func TestLogRequestRange(t *testing.T) {
	since := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	o := logOptions(500, false, time.Time{})
	if o.Tail != "500" || o.Since != "" || !o.Follow {
		t.Errorf("tail only: %+v", o)
	}
	o = logOptions(500, true, since)
	if o.Tail != "all" || o.Since != "1790856000" || !o.Timestamps {
		t.Errorf("since: %+v, want every line since the Unix time", o)
	}

	if got := strings.Join(nodeLogArgs(500, false, time.Time{}), " "); got != "-f --tail=500" {
		t.Errorf("node tail: %q", got)
	}
	if got := strings.Join(nodeLogArgs(500, true, since), " "); got != "-f --since=2026-10-01T12:00:00Z --timestamps" {
		t.Errorf("node since: %q", got)
	}
}
