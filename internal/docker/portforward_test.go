// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/network"
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
	cfg, host, nets, err := forwardConfig(target, netip.Addr{}, 18080, 80)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != ForwardImage || !reflect.DeepEqual([]string(cfg.Cmd),
		[]string{"tcp-listen:18080,fork,reuseaddr", "tcp-connect:172.20.0.5:80"}) {
		t.Errorf("image %q cmd %q", cfg.Image, cfg.Cmd)
	}
	b := host.PortBindings[network.MustParsePort("18080/tcp")]
	if len(b) != 1 || b[0].HostIP.String() != "127.0.0.1" || b[0].HostPort != "18080" {
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
	hostNet := Container{Name: "h", Network: "host", Endpoints: []Endpoint{{Network: "host"}}}
	_, _, _, err := forwardConfig(hostNet, netip.Addr{}, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "host networking") {
		t.Errorf("host networking: %v", err)
	}
	_, _, _, err = forwardConfig(Container{Name: "s", State: "exited"}, netip.Addr{}, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "no network address") {
		t.Errorf("stopped container: %v", err)
	}
}

// TestForwardConfigAddress: portForwardAddress is where the helper
// publishes, and the helper records it so :pf shows it.
func TestForwardConfigAddress(t *testing.T) {
	target := Container{ID: "abc", Name: "web", Endpoints: []Endpoint{{Network: "n", IP: "172.20.0.5"}}}
	for _, tc := range []struct {
		addr netip.Addr
		want string
	}{
		{addr: netip.Addr{}, want: "127.0.0.1"},
		{addr: netip.MustParseAddr("0.0.0.0"), want: "0.0.0.0"},
		{addr: netip.MustParseAddr("::1"), want: "::1"},
		{addr: netip.MustParseAddr("192.168.1.20"), want: "192.168.1.20"},
	} {
		cfg, host, _, err := forwardConfig(target, tc.addr, 18080, 80)
		if err != nil {
			t.Fatal(err)
		}
		b := host.PortBindings[network.MustParsePort("18080/tcp")]
		if len(b) != 1 || b[0].HostIP.String() != tc.want {
			t.Errorf("%v: binding %+v, want %s", tc.addr, b, tc.want)
		}
		if cfg.Labels[LabelForwardAddress] != tc.want {
			t.Errorf("%v: address label %q, want %s", tc.addr, cfg.Labels[LabelForwardAddress], tc.want)
		}
	}
}

// TestForwardListenAndURL: a forward on loopback (or one from before the
// address label) reads as localhost; on any other address it names it, and
// its URL is that address unless it is every address, where localhost
// answers too.
func TestForwardListenAndURL(t *testing.T) {
	for _, tc := range []struct {
		addr        string
		listen, url string
		local       bool
	}{
		{addr: "", listen: "localhost:15000", url: "http://localhost:15000", local: true},
		{addr: "127.0.0.1", listen: "localhost:15000", url: "http://localhost:15000", local: true},
		{addr: "::1", listen: "localhost:15000", url: "http://localhost:15000", local: true},
		{addr: "0.0.0.0", listen: "0.0.0.0:15000", url: "http://localhost:15000"},
		{addr: "::", listen: "[::]:15000", url: "http://localhost:15000"},
		{addr: "192.168.1.20", listen: "192.168.1.20:15000", url: "http://192.168.1.20:15000"},
		{addr: "fd00::5", listen: "[fd00::5]:15000", url: "http://[fd00::5]:15000"},
	} {
		addr, _ := netip.ParseAddr(tc.addr)
		f := PortForward{Address: addr, Local: 15000}
		if f.Listen() != tc.listen || f.URL() != tc.url || ForwardIsLocal(addr) != tc.local {
			t.Errorf("%q: listen %q url %q local %v; want %q %q %v",
				tc.addr, f.Listen(), f.URL(), ForwardIsLocal(addr), tc.listen, tc.url, tc.local)
		}
	}
}

func TestParseForwardSpecs(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		bad  string
	}{
		{in: "8080:80", want: "8080:80"},
		{in: "8080:80, 8443:443", want: "8080:80,8443:443"},
		{in: "5000", want: "5000:5000"},
		{in: "8080:80,8080:81", bad: "forwarded twice"},
		{in: "8080:80,", bad: "not a port"},
		{in: ",", bad: "not a port"},
		{in: "1,2,3,4,5,6,7,8,9", bad: "too many"},
		{in: strings.Repeat("1", 300), bad: "too long"},
	} {
		specs, err := ParseForwardSpecs(tc.in)
		if tc.bad != "" {
			if err == nil || !strings.Contains(err.Error(), tc.bad) {
				t.Errorf("ParseForwardSpecs(%q) = %v, %v; want an error with %q", tc.in, specs, err, tc.bad)
			}
			continue
		}
		if err != nil || JoinForwardSpecs(specs) != tc.want {
			t.Errorf("ParseForwardSpecs(%q) = %v, %v; want %s", tc.in, specs, err, tc.want)
		}
	}
}

// TestLabelForwards: the labels, good, bad and hostile. A bad label is an
// error naming the label and never a partial list; the auto label wins.
func TestLabelForwards(t *testing.T) {
	web := func(labels map[string]string) Container {
		return Container{Name: "shop-web-1", Service: "web", Labels: labels}
	}
	for _, tc := range []struct {
		labels map[string]string
		name   string
		want   string
		bad    string
		auto   bool
	}{
		{name: "none", labels: nil},
		{name: "other labels", labels: map[string]string{"com.example": "8080:80"}},
		{name: "preset", labels: map[string]string{LabelPortForwards: "8080:80"}, want: "8080:80"},
		{name: "preset one port", labels: map[string]string{LabelPortForwards: "5432"}, want: "5432:5432"},
		{
			name: "auto", labels: map[string]string{LabelAutoPortForwards: "8080:80,8443:443"},
			want: "8080:80,8443:443", auto: true,
		},
		{name: "auto wins", auto: true, want: "9000:90", labels: map[string]string{
			LabelPortForwards: "8080:80", LabelAutoPortForwards: "9000:90",
		}},
		{name: "k9s service prefix", labels: map[string]string{LabelPortForwards: "web::8080:80"}, want: "8080:80"},
		{name: "k9s name prefix", labels: map[string]string{LabelPortForwards: "shop-web-1::80"}, want: "80:80"},
		{
			name: "prefix per spec", labels: map[string]string{LabelPortForwards: "web::8080:80,8443:443"},
			want: "8080:80,8443:443",
		},
		{
			name: "another container", labels: map[string]string{LabelPortForwards: "db::5432"},
			bad: "names another container",
		},
		{name: "empty prefix", labels: map[string]string{LabelPortForwards: "::5432"}, bad: "names another"},
		{name: "empty", labels: map[string]string{LabelAutoPortForwards: ""}, bad: "is empty", auto: true},
		{name: "blank", labels: map[string]string{LabelPortForwards: "  "}, bad: "is empty"},
		{name: "port name", labels: map[string]string{LabelPortForwards: "web::9090:http"}, bad: "not a port"},
		{
			name: "out of range", labels: map[string]string{LabelAutoPortForwards: "70000:80"}, bad: "not a port",
			auto: true,
		},
		{name: "zero", labels: map[string]string{LabelPortForwards: "0"}, bad: "not a port"},
		{name: "negative", labels: map[string]string{LabelPortForwards: "-1:80"}, bad: "not a port"},
		{name: "three parts", labels: map[string]string{LabelPortForwards: "1:2:3"}, bad: "not a port"},
		{name: "double prefix", labels: map[string]string{LabelPortForwards: "web::web::80"}, bad: "not a port"},
		{
			name: "too long", labels: map[string]string{LabelAutoPortForwards: strings.Repeat("8080:80,", 40)},
			bad: "too long", auto: true,
		},
		{
			name: "too many", labels: map[string]string{LabelAutoPortForwards: "1,2,3,4,5,6,7,8,9"},
			bad: "too many", auto: true,
		},
		{
			name: "duplicate local", labels: map[string]string{LabelAutoPortForwards: "8080:80,8080:443"},
			bad: "twice", auto: true,
		},
		{
			name: "escape sequence", labels: map[string]string{LabelPortForwards: "\x1b]52;c;aGk=\x07"},
			bad: "not a port",
		},
		{name: "real escape", labels: map[string]string{LabelPortForwards: "\x1b[2J:80"}, bad: "not a port"},
		{name: "unicode digits", labels: map[string]string{LabelPortForwards: "٨٠"}, bad: "not a port"},
	} {
		specs, auto, err := LabelForwards(web(tc.labels))
		if auto != tc.auto {
			t.Errorf("%s: auto %v, want %v", tc.name, auto, tc.auto)
		}
		if tc.bad != "" {
			if err == nil || !strings.Contains(err.Error(), tc.bad) || specs != nil {
				t.Errorf("%s: %v, %v; want no forwards and an error with %q", tc.name, specs, err, tc.bad)
			}
			continue
		}
		if err != nil || JoinForwardSpecs(specs) != tc.want {
			t.Errorf("%s: %v, %v; want %q", tc.name, specs, err, tc.want)
		}
	}
}

// TestLabelForwardsErrorIsInert: a hostile label's text reaches the screen
// only quoted, its controls escaped, and the error names the label.
func TestLabelForwardsErrorIsInert(t *testing.T) {
	for _, v := range []string{"\x1b[2J", "a\u202eb::80", "web::\x1b]52;c;aGk=\x07"} {
		_, _, err := LabelForwards(Container{Name: "web", Labels: map[string]string{LabelPortForwards: v}})
		if err == nil {
			t.Fatalf("%q: no error", v)
		}
		if strings.ContainsAny(err.Error(), "\x1b\x07\u202e") {
			t.Errorf("%q: error carries a raw control: %q", v, err)
		}
		if !strings.Contains(err.Error(), LabelPortForwards) {
			t.Errorf("%q: error does not name the label: %v", v, err)
		}
	}
}

// TestPortForwardAddressOverTheAPI: against a fake daemon, the address
// reaches the create request as the binding's host IP and the address label,
// and the listing reads it back — loopback for a helper without the label
// or with garbage in it.
func TestPortForwardAddressOverTheAPI(t *testing.T) {
	version := regexp.MustCompile(`^/v[0-9.]+`)
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		path := version.ReplaceAllString(r.URL.Path, "")
		switch {
		case path == "/_ping":
			_, _ = w.Write([]byte("OK"))
		case strings.HasPrefix(path, "/images/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:socat"})
		case path == "/containers/create":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &created)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "helper1"})
		case strings.HasSuffix(path, "/start"):
			w.WriteHeader(http.StatusNoContent)
		case path == "/containers/json":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"Id": "h1", "State": "running", "Labels": map[string]string{
					LabelForward: "true", LabelForwardLocal: "15000", LabelForwardAddress: "0.0.0.0",
				}},
				{"Id": "h2", "State": "running", "Labels": map[string]string{
					LabelForward: "true", LabelForwardLocal: "15001",
				}},
				{"Id": "h3", "State": "running", "Labels": map[string]string{
					LabelForward: "true", LabelForwardLocal: "15002", LabelForwardAddress: "\x1b[2J",
				}},
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
	target := Container{ID: "abc", Name: "web", Endpoints: []Endpoint{{Network: "n", IP: "172.20.0.5"}}}
	pf, err := c.StartPortForward(context.Background(), target, netip.MustParseAddr("0.0.0.0"), 15000, 80)
	if err != nil {
		t.Fatal(err)
	}
	if pf.Listen() != "0.0.0.0:15000" {
		t.Errorf("started forward listens on %q", pf.Listen())
	}
	host, _ := created["HostConfig"].(map[string]any)
	bindings, _ := host["PortBindings"].(map[string]any)
	b, _ := bindings["15000/tcp"].([]any)
	var first map[string]any
	if len(b) > 0 {
		first, _ = b[0].(map[string]any)
	}
	if len(b) != 1 || first["HostIp"] != "0.0.0.0" {
		t.Errorf("create bound %v, want 0.0.0.0", bindings)
	}
	if labels, _ := created["Labels"].(map[string]any); labels[LabelForwardAddress] != "0.0.0.0" {
		t.Errorf("create labels %v", labels)
	}

	list, err := c.PortForwards(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(list))
	for _, f := range list {
		got = append(got, f.Listen())
	}
	if want := "0.0.0.0:15000 localhost:15001 localhost:15002"; strings.Join(got, " ") != want {
		t.Errorf("listed %q, want %q", strings.Join(got, " "), want)
	}
}
