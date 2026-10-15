// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"strings"
	"testing"
)

func TestVolumeCreateConfig(t *testing.T) {
	opts, err := VolumeCreateConfig(VolumeSpec{
		Name: "data", Opts: []string{"type=nfs", "o=addr=10.0.0.1,rw"}, Labels: []string{"team=web", "tier="},
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Driver != "local" || opts.DriverOpts["o"] != "addr=10.0.0.1,rw" || opts.Labels["team"] != "web" {
		t.Errorf("opts = %+v", opts)
	}
	if v, ok := opts.Labels["tier"]; !ok || v != "" {
		t.Errorf("an empty label value was dropped: %+v", opts.Labels)
	}
	if _, err := VolumeCreateConfig(VolumeSpec{}); err != nil {
		t.Errorf("an unnamed volume is the daemon's to name: %v", err)
	}

	for _, tc := range []struct {
		want string
		spec VolumeSpec
	}{
		{spec: VolumeSpec{Name: "a"}, want: "name"},
		{spec: VolumeSpec{Name: "-data"}, want: "name"},
		{spec: VolumeSpec{Name: "my data"}, want: "name"},
		{spec: VolumeSpec{Opts: []string{"type"}}, want: "driver opt"},
		{spec: VolumeSpec{Labels: []string{"=x"}}, want: "label"},
	} {
		if _, err := VolumeCreateConfig(tc.spec); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err %v, want one naming %q", tc.spec, err, tc.want)
		}
	}
}

func TestNetworkCreateConfig(t *testing.T) {
	opts, err := NetworkCreateConfig(NetworkSpec{
		Name: "backend", Subnet: "172.30.1.7/16", Gateway: "172.30.0.1", Internal: true, Labels: []string{"a=b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Driver != "bridge" || !opts.Internal || opts.Labels["a"] != "b" || opts.EnableIPv6 != nil {
		t.Errorf("opts = %+v", opts)
	}
	if opts.IPAM == nil || len(opts.IPAM.Config) != 1 ||
		opts.IPAM.Config[0].Subnet.String() != "172.30.0.0/16" || opts.IPAM.Config[0].Gateway.String() != "172.30.0.1" {
		t.Errorf("IPAM = %+v", opts.IPAM)
	}

	v6, err := NetworkCreateConfig(NetworkSpec{Name: "six", Subnet: "fd00:1::/64"})
	if err != nil || v6.EnableIPv6 == nil || !*v6.EnableIPv6 {
		t.Errorf("an IPv6 subnet did not enable IPv6: %+v %v", v6, err)
	}
	if plain, err := NetworkCreateConfig(NetworkSpec{Name: "plain"}); err != nil || plain.IPAM != nil {
		t.Errorf("no subnet should leave IPAM to the daemon: %+v %v", plain, err)
	}

	for _, tc := range []struct {
		want string
		spec NetworkSpec
	}{
		{spec: NetworkSpec{}, want: "name"},
		{spec: NetworkSpec{Name: "a b"}, want: "name"},
		{spec: NetworkSpec{Name: "n", Subnet: "172.30.0.0"}, want: "subnet"},
		{spec: NetworkSpec{Name: "n", Gateway: "172.30.0.1"}, want: "gateway"},
		{spec: NetworkSpec{Name: "n", Subnet: "172.30.0.0/16", Gateway: "10.0.0.1"}, want: "not in"},
		{spec: NetworkSpec{Name: "n", Subnet: "172.30.0.0/16", Gateway: "x"}, want: "gateway"},
		{spec: NetworkSpec{Name: "n", Labels: []string{"nokey"}}, want: "label"},
	} {
		if _, err := NetworkCreateConfig(tc.spec); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err %v, want one naming %q", tc.spec, err, tc.want)
		}
	}
}
