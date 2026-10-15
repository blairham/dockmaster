// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// builtinNetworks are the three networks the daemon creates and refuses to
// delete. They are shown, but the delete path refuses them up front rather
// than round-tripping to the daemon for a guaranteed error.
var builtinNetworks = map[string]bool{"bridge": true, "host": true, "none": true}

// Network is one row of the networks view.
type Network struct {
	Created time.Time

	Labels map[string]string

	ID      string
	Name    string
	Driver  string
	Scope   string
	Subnet  string
	Gateway string
	Project string

	// Containers is how many containers are attached, or -1 when the list
	// endpoint did not include the attachment map.
	Containers int

	Internal   bool
	Attachable bool
	IPv6       bool
}

// Short is the 12-char network ID.
func (n Network) Short() string { return shortID(n.ID) }

// Age is the network's creation age, one unit.
func (n Network) Age() string { return since(n.Created) }

// Builtin reports whether this is a daemon-managed network that cannot be
// removed.
func (n Network) Builtin() bool { return builtinNetworks[n.Name] }

// Networks lists networks.
func (c *Client) Networks(ctx context.Context) ([]Network, error) {
	res, err := c.api.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}

	out := make([]Network, 0, len(res.Items))
	for _, n := range res.Items {
		subnets := make([]string, 0, len(n.IPAM.Config))
		gateway := ""
		for _, cfg := range n.IPAM.Config {
			// Subnet and Gateway are netip values now; the zero value
			// is "not set", which must stay "" rather than print "invalid".
			if cfg.Subnet.IsValid() {
				subnets = append(subnets, cfg.Subnet.String())
			}
			if gateway == "" && cfg.Gateway.IsValid() {
				gateway = cfg.Gateway.String()
			}
		}
		sort.Strings(subnets)

		// The list endpoint does not report attachments — the daemon
		// leaves Containers out of /networks, and the API's list type no
		// longer has the field at all. -1 keeps "unknown" distinct from
		// "zero attached", which matters because zero is the
		// delete-is-safe signal.
		count := -1

		out = append(out, Network{
			ID:         n.ID,
			Name:       n.Name,
			Driver:     n.Driver,
			Scope:      n.Scope,
			Created:    n.Created,
			Subnet:     strings.Join(subnets, ","),
			Gateway:    gateway,
			Internal:   n.Internal,
			Attachable: n.Attachable,
			IPv6:       n.EnableIPv6,
			Labels:     n.Labels,
			Project:    n.Labels[LabelProject],
			Containers: count,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// InspectNetwork returns pretty-printed inspect JSON for a network.
func (c *Client) InspectNetwork(ctx context.Context, id string) ([]byte, error) {
	res, err := c.api.NetworkInspect(ctx, id, client.NetworkInspectOptions{Verbose: true})
	if err != nil {
		return nil, fmt.Errorf("inspecting network %s: %w", shortID(id), err)
	}
	return indentJSON(res.Raw)
}

// RemoveNetwork deletes a network.
func (c *Client) RemoveNetwork(ctx context.Context, id, name string) error {
	if builtinNetworks[name] {
		return fmt.Errorf("%s is a built-in network and cannot be removed", name)
	}
	if _, err := c.api.NetworkRemove(ctx, id, client.NetworkRemoveOptions{}); err != nil {
		return fmt.Errorf("removing network %s: %w", name, err)
	}
	return nil
}

// PruneNetworks removes every network no container is attached to.
func (c *Client) PruneNetworks(ctx context.Context) (deleted int, err error) {
	res, err := c.api.NetworkPrune(ctx, client.NetworkPruneOptions{})
	if err != nil {
		return 0, fmt.Errorf("pruning networks: %w", err)
	}
	return len(res.Report.NetworksDeleted), nil
}

// NetworkAttach is one `docker network connect`: the container, and the
// optional DNS aliases and fixed address it takes on the network.
type NetworkAttach struct {
	Container string
	IP        string
	Aliases   []string
}

// ParseEndpointIP reads a fixed address for a connect: "" for one the
// daemon picks, else an IPv4 or IPv6 address — never a CIDR, which
// `docker network connect --ip` refuses too.
func ParseEndpointIP(s string) (*network.EndpointIPAMConfig, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil //nolint:nilnil // no fixed address is not an error
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return nil, fmt.Errorf("%q is not an IP address", s)
	}
	if addr.Is4() || addr.Is4In6() {
		return &network.EndpointIPAMConfig{IPv4Address: addr.Unmap()}, nil
	}
	return &network.EndpointIPAMConfig{IPv6Address: addr}, nil
}

// ConnectNetwork attaches a container to a network, as `docker network
// connect [--alias …] [--ip …]`. The daemon's error is wrapped, not
// replaced, so a caller can still tell "already connected" apart.
func (c *Client) ConnectNetwork(ctx context.Context, networkName string, at NetworkAttach) error {
	ipam, err := ParseEndpointIP(at.IP)
	if err != nil {
		return err
	}
	_, err = c.api.NetworkConnect(ctx, networkName, client.NetworkConnectOptions{
		Container: at.Container,
		EndpointConfig: &network.EndpointSettings{
			Aliases:    at.Aliases,
			IPAMConfig: ipam,
		},
	})
	if err != nil {
		return fmt.Errorf("connecting %s to %s: %w", at.Container, networkName, err)
	}
	return nil
}

// DisconnectNetwork detaches a container from a network, as `docker
// network disconnect` without -f: a daemon that cannot detach it cleanly
// says so rather than having its endpoint state forced.
func (c *Client) DisconnectNetwork(ctx context.Context, networkName, container string) error {
	if container == "" {
		return errors.New("no container to disconnect")
	}
	_, err := c.api.NetworkDisconnect(ctx, networkName, client.NetworkDisconnectOptions{Container: container})
	if err != nil {
		return fmt.Errorf("disconnecting %s from %s: %w", container, networkName, err)
	}
	return nil
}
