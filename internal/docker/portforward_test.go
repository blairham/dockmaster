package docker

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseForwardSpec(t *testing.T) {
	for _, tc := range []struct {
		in            string
		local, remote int
		bad           bool
	}{
		{in: "8080:80", local: 8080, remote: 80},
		{in: " 5000 ", local: 5000, remote: 5000},
		{in: "15000:5000", local: 15000, remote: 5000},
		{in: "", bad: true},
		{in: "abc", bad: true},
		{in: "0:80", bad: true},
		{in: "8080:70000", bad: true},
		{in: "8080:", bad: true},
	} {
		l, r, err := ParseForwardSpec(tc.in)
		if (err != nil) != tc.bad || (!tc.bad && (l != tc.local || r != tc.remote)) {
			t.Errorf("ParseForwardSpec(%q) = %d, %d, %v", tc.in, l, r, err)
		}
	}
}

func TestDefaultForwardSpec(t *testing.T) {
	c := Container{
		PortList: []PortMapping{{Type: "udp", Private: 53}, {Type: "tcp", Private: 8080}, {Type: "tcp", Private: 443}},
	}
	if got := DefaultForwardSpec(c); got != "443:443" {
		t.Errorf("DefaultForwardSpec = %q, want the lowest TCP port", got)
	}
	if got := DefaultForwardSpec(Container{}); got != "" {
		t.Errorf("no ports = %q", got)
	}
}

// TestForwardConfig pins the helper: socat relays the local port to the
// target's address on the target's network, published on loopback only,
// labeled so dockmaster can list it, and removed when stopped.
func TestForwardConfig(t *testing.T) {
	target := Container{
		ID: "abc123", Name: "web",
		Endpoints: []Endpoint{{Network: "shop_default", IP: "172.20.0.5"}, {Network: "zz", IP: "10.0.0.2"}},
	}
	cfg, host, nets, err := forwardConfig(target, 18080, 80)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != ForwardImage || !reflect.DeepEqual([]string(cfg.Cmd),
		[]string{"tcp-listen:18080,fork,reuseaddr", "tcp-connect:172.20.0.5:80"}) {
		t.Errorf("image %q cmd %q", cfg.Image, cfg.Cmd)
	}
	if b := host.PortBindings["18080/tcp"]; len(b) != 1 || b[0].HostIP != "127.0.0.1" || b[0].HostPort != "18080" {
		t.Errorf("binding = %+v, want 127.0.0.1:18080 only", b)
	}
	if !host.AutoRemove {
		t.Error("helper is not AutoRemove")
	}
	if _, ok := nets.EndpointsConfig["shop_default"]; !ok || len(nets.EndpointsConfig) != 1 {
		t.Errorf("networks = %v, want the target's first network", nets.EndpointsConfig)
	}
	for k, want := range map[string]string{
		LabelForward: "true", LabelForwardTarget: "abc123", LabelForwardTargetName: "web",
		LabelForwardLocal: "18080", LabelForwardRemote: "80",
	} {
		if cfg.Labels[k] != want {
			t.Errorf("label %s = %q, want %q", k, cfg.Labels[k], want)
		}
	}
	if forwardName("web", 18080) != "dockmaster-pf-web-18080" {
		t.Error("helper name changed")
	}
}

func TestForwardEndpointRefusals(t *testing.T) {
	_, _, _, err := forwardConfig(Container{Name: "h", Network: "host", Endpoints: []Endpoint{{Network: "host"}}}, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "host networking") {
		t.Errorf("host networking: %v", err)
	}
	_, _, _, err = forwardConfig(Container{Name: "s", State: "exited"}, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "no network address") {
		t.Errorf("stopped container: %v", err)
	}
}
