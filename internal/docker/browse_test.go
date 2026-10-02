// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import "testing"

func TestWebPorts(t *testing.T) {
	c := Container{PortList: []PortMapping{
		{Type: "tcp", Private: 80, Public: 8080},
		{Type: "tcp", Private: 80, Public: 8080}, // the same port on ::
		{Type: "udp", Private: 53, Public: 5353},
		{Type: "tcp", Private: 9000}, // exposed, not published
		{Type: "tcp", Private: 443, Public: 8443},
	}}
	got := WebPorts(c)
	if len(got) != 2 || got[0].Public != 8080 || got[1].Public != 8443 {
		t.Errorf("WebPorts = %+v, want 8080 then 8443, once each, tcp only", got)
	}
	if s := PortChoices(got); s != "8080→80 8443→443" {
		t.Errorf("PortChoices = %q", s)
	}
}

func TestPortChoicesIsBounded(t *testing.T) {
	var c Container
	for p := uint16(31000); p > 30990; p-- { // listed in descending order
		c.PortList = append(c.PortList, PortMapping{Type: "tcp", Private: p, Public: p})
	}
	ports := WebPorts(c)
	if ports[0].Public != 30991 {
		t.Errorf("not sorted: first is %d", ports[0].Public)
	}
	if got := PortChoices(
		ports,
	); got != "30991→30991 30992→30992 30993→30993 30994→30994 30995→30995 30996→30996 +4 more" {
		t.Errorf("PortChoices = %q", got)
	}
}

func TestBrowseHost(t *testing.T) {
	for in, want := range map[string]string{
		"unix:///Users/me/.colima/default/docker.sock": "localhost",
		"unix:///var/run/docker.sock":                  "localhost",
		"npipe:////./pipe/docker_engine":               "localhost",
		"":                                             "localhost",
		"tcp://10.0.0.5:2376":                          "10.0.0.5",
		"tcp://build-box.lan:2375":                     "build-box.lan",
		"ssh://me@build-box.lan":                       "build-box.lan",
		"ssh://me@build-box.lan:2222":                  "build-box.lan",
		"tcp://0.0.0.0:2375":                           "localhost",
		"tcp://[fd00::5]:2376":                         "fd00::5",
	} {
		if got := BrowseHost(in); got != want {
			t.Errorf("BrowseHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPortURL(t *testing.T) {
	for _, tc := range []struct {
		host string
		want string
		p    PortMapping
	}{
		{host: "localhost", p: PortMapping{Private: 80, Public: 8080}, want: "http://localhost:8080"},
		{host: "localhost", p: PortMapping{Private: 443, Public: 8443}, want: "https://localhost:8443"},
		{host: "localhost", p: PortMapping{Private: 8443, Public: 9443}, want: "https://localhost:9443"},
		{host: "127.0.0.1", p: PortMapping{Private: 6443, Public: 51234}, want: "https://127.0.0.1:51234"},
		{host: "fd00::5", p: PortMapping{Private: 80, Public: 80}, want: "http://[fd00::5]:80"},
	} {
		if got := PortURL(tc.host, tc.p); got != tc.want {
			t.Errorf("PortURL(%q, %+v) = %q, want %q", tc.host, tc.p, got, tc.want)
		}
	}
}
