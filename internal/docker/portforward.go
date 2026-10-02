// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// Port-forward helper labels. Every helper carries them, so the forwards
// can be listed from the daemon alone and told apart from user containers.
const (
	LabelForward           = "dockmaster.portforward"
	LabelForwardTarget     = "dockmaster.portforward.target"
	LabelForwardTargetName = "dockmaster.portforward.target-name"
	LabelForwardLocal      = "dockmaster.portforward.local"
	LabelForwardRemote     = "dockmaster.portforward.remote"
)

// ForwardImage relays one TCP port. socat is the whole program; the image
// is a few megabytes.
const ForwardImage = "alpine/socat:latest"

// PortForward is one running forward: localhost:Local → Target:Remote.
type PortForward struct {
	Created    time.Time
	ID         string // the helper container
	Target     string // target container ID
	TargetName string
	State      string
	Local      int
	Remote     int
}

// URL is where the forward answers on this machine.
func (f PortForward) URL() string { return "http://localhost:" + strconv.Itoa(f.Local) }

// ParseForwardSpec reads "local:remote", or a single port for both.
func ParseForwardSpec(s string) (local, remote int, err error) {
	s = strings.TrimSpace(s)
	l, r, ok := strings.Cut(s, ":")
	if !ok {
		r = l
	}
	if local, err = parsePort(l); err != nil {
		return 0, 0, fmt.Errorf("local port: %w", err)
	}
	if remote, err = parsePort(r); err != nil {
		return 0, 0, fmt.Errorf("container port: %w", err)
	}
	return local, remote, nil
}

func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("%q is not a port (1-65535)", s)
	}
	return n, nil
}

// DefaultForwardSpec is the prompt's prefill for a container: its first
// TCP port, forwarded to the same number locally, or "" when it has none.
func DefaultForwardSpec(c Container) string {
	ports := append([]PortMapping(nil), c.PortList...)
	sort.Slice(ports, func(i, j int) bool { return ports[i].Private < ports[j].Private })
	for _, p := range ports {
		if p.Type == "" || p.Type == "tcp" {
			n := strconv.Itoa(int(p.Private))
			return n + ":" + n
		}
	}
	return ""
}

// forwardEndpoint picks the network the helper joins and the address it
// relays to: the target's first network with an address. Host and none
// networking have no address to relay to.
func forwardEndpoint(c Container) (Endpoint, error) {
	for _, e := range c.Endpoints {
		if e.IP != "" && e.Network != "host" && e.Network != "none" {
			return e, nil
		}
	}
	if strings.Contains(c.Network, "host") {
		return Endpoint{}, errors.New(c.Name + " uses host networking — its ports are already on the host")
	}
	return Endpoint{}, errors.New(c.Name + " has no network address to forward to — is it running?")
}

// forwardName is the helper's container name.
func forwardName(target string, local int) string {
	return fmt.Sprintf("dockmaster-pf-%s-%d", target, local)
}

// forwardConfig builds the helper container: socat listening on the local
// port inside the target's network, published on the host's loopback only,
// relaying to the target's address. AutoRemove makes stopping it the whole
// cleanup.
func forwardConfig(
	target Container,
	local, remote int,
) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	ep, err := forwardEndpoint(target)
	if err != nil {
		return nil, nil, nil, err
	}
	port, err := network.ParsePort(strconv.Itoa(local) + "/tcp")
	if err != nil {
		return nil, nil, nil, err
	}
	cfg := &container.Config{
		Image: ForwardImage,
		Cmd: []string{
			fmt.Sprintf("tcp-listen:%d,fork,reuseaddr", local),
			fmt.Sprintf("tcp-connect:%s:%d", ep.IP, remote),
		},
		ExposedPorts: network.PortSet{port: struct{}{}},
		Labels: map[string]string{
			LabelForward:           "true",
			LabelForwardTarget:     target.ID,
			LabelForwardTargetName: target.Name,
			LabelForwardLocal:      strconv.Itoa(local),
			LabelForwardRemote:     strconv.Itoa(remote),
		},
	}
	host := &container.HostConfig{
		AutoRemove: true,
		PortBindings: network.PortMap{port: {{
			HostIP:   netip.AddrFrom4([4]byte{127, 0, 0, 1}),
			HostPort: strconv.Itoa(local),
		}}},
	}
	nets := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{ep.Network: {}}}
	return cfg, host, nets, nil
}

// StartPortForward forwards localhost:local to target:remote through a
// helper container, pulling the helper image the first time.
//
// Docker cannot publish a new port on a running container, so this is how
// a forward has to work: a relay that joins the target's network and
// publishes the port itself. The binding is on 127.0.0.1, so the forward is
// reachable from this machine only.
func (c *Client) StartPortForward(ctx context.Context, target Container, local, remote int) (PortForward, error) {
	cfg, host, nets, err := forwardConfig(target, local, remote)
	if err != nil {
		return PortForward{}, err
	}
	if _, ierr := c.api.ImageInspect(ctx, ForwardImage); ierr != nil {
		if !cerrdefs.IsNotFound(ierr) {
			return PortForward{}, fmt.Errorf("checking %s: %w", ForwardImage, ierr)
		}
		if perr := c.PullImage(ctx, ForwardImage); perr != nil {
			return PortForward{}, perr
		}
	}
	name := forwardName(target.Name, local)
	created, err := c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:           cfg,
		HostConfig:       host,
		NetworkingConfig: nets,
		Name:             name,
	})
	if err != nil {
		return PortForward{}, fmt.Errorf("creating the forward: %w", err)
	}
	if _, err := c.api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		// A port already taken fails here; the created helper is ours to clean up.
		cleanup := client.ContainerRemoveOptions{Force: true}
		_, _ = c.api.ContainerRemove(ctx, created.ID, cleanup) //nolint:errcheck // best effort; the start error is reported
		return PortForward{}, fmt.Errorf("starting the forward on localhost:%d: %w", local, err)
	}
	return PortForward{
		ID:         created.ID,
		Target:     target.ID,
		TargetName: target.Name,
		Local:      local,
		Remote:     remote,
		State:      "running",
	}, nil
}

// PortForwards lists the forward helpers on the daemon.
func (c *Client) PortForwards(ctx context.Context) ([]PortForward, error) {
	list, err := c.api.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", LabelForward+"=true"),
	})
	if err != nil {
		return nil, fmt.Errorf("listing port forwards: %w", err)
	}
	out := make([]PortForward, 0, len(list.Items))
	for _, s := range list.Items {
		local, _ := strconv.Atoi(s.Labels[LabelForwardLocal])   //nolint:errcheck // our own label; 0 on garbage
		remote, _ := strconv.Atoi(s.Labels[LabelForwardRemote]) //nolint:errcheck // our own label; 0 on garbage
		out = append(out, PortForward{
			ID:         s.ID,
			Target:     s.Labels[LabelForwardTarget],
			TargetName: s.Labels[LabelForwardTargetName],
			Local:      local,
			Remote:     remote,
			State:      string(s.State),
			Created:    time.Unix(s.Created, 0),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Local < out[j].Local })
	return out, nil
}

// StopPortForward removes a forward helper.
func (c *Client) StopPortForward(ctx context.Context, id string) error {
	if _, err := c.api.ContainerRemove(
		ctx,
		id,
		client.ContainerRemoveOptions{Force: true},
	); err != nil &&
		!cerrdefs.IsNotFound(err) {
		return fmt.Errorf("stopping the forward: %w", err)
	}
	return nil
}
