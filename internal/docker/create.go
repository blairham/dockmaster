// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// validVolumeName is what the daemon accepts as a volume name.
var validVolumeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]+$`)

// VolumeSpec is `docker volume create` as the volume form fills it in.
type VolumeSpec struct {
	// Name is optional: the daemon names an unnamed volume itself.
	Name   string
	Driver string
	// Opts are "key=value", as -o takes them.
	Opts []string
	// Labels are "key=value", as --label takes them.
	Labels []string
}

// NetworkSpec is `docker network create` as the network form fills it in.
type NetworkSpec struct {
	Name   string
	Driver string
	// Subnet and Gateway are optional CIDR and address; a gateway needs
	// a subnet to sit in.
	Subnet  string
	Gateway string
	// Labels are "key=value", as --label takes them.
	Labels     []string
	Internal   bool
	Attachable bool
}

// VolumeCreateConfig turns a spec into the create request, or explains the
// first part of it that is wrong — before anything reaches the daemon.
func VolumeCreateConfig(s VolumeSpec) (client.VolumeCreateOptions, error) {
	if s.Name != "" && !validVolumeName.MatchString(s.Name) {
		return client.VolumeCreateOptions{}, fmt.Errorf(
			"name: two or more of letters, digits, _ . -, starting with a letter or digit",
		)
	}
	opts, err := keyValues("driver opt", s.Opts)
	if err != nil {
		return client.VolumeCreateOptions{}, err
	}
	labels, err := keyValues("label", s.Labels)
	if err != nil {
		return client.VolumeCreateOptions{}, err
	}
	driver := strings.TrimSpace(s.Driver)
	if driver == "" {
		driver = "local"
	}
	return client.VolumeCreateOptions{Name: s.Name, Driver: driver, DriverOpts: opts, Labels: labels}, nil
}

// NetworkCreateConfig turns a spec into the create request, or explains the
// first part of it that is wrong.
func NetworkCreateConfig(s NetworkSpec) (client.NetworkCreateOptions, error) {
	if strings.TrimSpace(s.Name) == "" {
		return client.NetworkCreateOptions{}, fmt.Errorf("name: a network needs one")
	}
	if strings.ContainsAny(s.Name, " \t/") {
		return client.NetworkCreateOptions{}, fmt.Errorf("name: no spaces or slashes")
	}
	labels, err := keyValues("label", s.Labels)
	if err != nil {
		return client.NetworkCreateOptions{}, err
	}
	driver := strings.TrimSpace(s.Driver)
	if driver == "" {
		driver = "bridge"
	}
	opts := client.NetworkCreateOptions{
		Driver:     driver,
		Internal:   s.Internal,
		Attachable: s.Attachable,
		Labels:     labels,
	}
	ipam, err := networkIPAM(s.Subnet, s.Gateway)
	if err != nil {
		return client.NetworkCreateOptions{}, err
	}
	if ipam != nil {
		opts.IPAM = ipam
		if ipam.Config[0].Subnet.Addr().Is6() {
			v6 := true
			opts.EnableIPv6 = &v6
		}
	}
	return opts, nil
}

// networkIPAM is the IPAM block for a subnet and gateway, or nil when
// neither is given and the daemon picks the subnet.
func networkIPAM(subnet, gateway string) (*network.IPAM, error) {
	if subnet == "" {
		if gateway != "" {
			return nil, fmt.Errorf("gateway: needs a subnet to sit in")
		}
		return nil, nil //nolint:nilnil // no subnet is no IPAM block, not an error
	}
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		return nil, fmt.Errorf("subnet %q is not a CIDR such as 172.30.0.0/16", subnet)
	}
	cfg := network.IPAMConfig{Subnet: prefix.Masked()}
	if gateway != "" {
		gw, err := netip.ParseAddr(gateway)
		if err != nil {
			return nil, fmt.Errorf("gateway %q is not an IP address", gateway)
		}
		if !prefix.Contains(gw) {
			return nil, fmt.Errorf("gateway %s is not in %s", gw, prefix.Masked())
		}
		cfg.Gateway = gw
	}
	return &network.IPAM{Config: []network.IPAMConfig{cfg}}, nil
}

// keyValues reads "key=value" entries into a map. A key given twice keeps
// its last value, as the CLI's flags do.
func keyValues(what string, entries []string) (map[string]string, error) {
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%s %q is not key=value", what, e)
		}
		out[k] = v
	}
	return out, nil
}

// CreateVolume creates the volume the spec describes and returns its name,
// which the daemon chooses when the spec leaves it empty.
func (c *Client) CreateVolume(ctx context.Context, s VolumeSpec) (string, error) {
	opts, err := VolumeCreateConfig(s)
	if err != nil {
		return "", err
	}
	res, err := c.api.VolumeCreate(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("creating volume: %w", err)
	}
	return res.Volume.Name, nil
}

// CreateNetwork creates the network the spec describes and returns its ID.
func (c *Client) CreateNetwork(ctx context.Context, s NetworkSpec) (string, error) {
	opts, err := NetworkCreateConfig(s)
	if err != nil {
		return "", err
	}
	res, err := c.api.NetworkCreate(ctx, s.Name, opts)
	if err != nil {
		return "", fmt.Errorf("creating network %s: %w", s.Name, err)
	}
	return res.ID, nil
}
