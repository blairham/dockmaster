// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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
