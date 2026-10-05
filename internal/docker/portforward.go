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
	// LabelForwardAddress is the host address the helper publishes on
	// (portForwardAddress). A helper without it publishes on 127.0.0.1.
	LabelForwardAddress = "dockmaster.portforward.address"
)

// Labels a user's container may carry to say how it is forwarded: k9s's
// FastForwards annotations (k9scli.io/port-forwards and
// k9scli.io/auto-port-forwards) as container labels. The value is what the
// shift-f prompt takes — "[local:]container-port", comma-separated for
// more than one — optionally after a k9s-style "name::" naming this
// container or its compose service.
//
//   - LabelPortForwards prefills the prompt with its forwards.
//   - LabelAutoPortForwards replaces the prompt with a yes/no confirm of its
//     forwards; it wins over LabelPortForwards.
//
// A label is anybody's text: a container's author sets it, not the user.
// So it is parsed by the prompt's own parser and capped, a bad one is
// reported rather than acted on, and neither label starts a forward
// without the user saying yes.
const (
	LabelPortForwards     = "dockmaster.port-forwards"
	LabelAutoPortForwards = "dockmaster.auto-port-forwards"
)

// Bounds on a forward list, from a label or the prompt: a label is not
// trusted to be short, and a container asking for dozens of forwards is
// not one to start them for.
const (
	maxForwardSpecLen = 256
	maxForwardSpecs   = 8
)

// ForwardSpec is one forward asked for: localhost:Local → container:Remote.
type ForwardSpec struct {
	Local  int
	Remote int
}

// String is the spec as the prompt takes it, "8080:80".
func (s ForwardSpec) String() string { return strconv.Itoa(s.Local) + ":" + strconv.Itoa(s.Remote) }

// JoinForwardSpecs is specs as the prompt takes them, "8080:80,8443:443".
func JoinForwardSpecs(specs []ForwardSpec) string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.String()
	}
	return strings.Join(out, ",")
}

// ForwardImage relays one TCP port. socat is the whole program; the image
// is a few megabytes.
const ForwardImage = "alpine/socat:latest"

// PortForward is one running forward: Address:Local → Target:Remote.
type PortForward struct {
	Created    time.Time
	ID         string // the helper container
	Target     string // target container ID
	TargetName string
	State      string
	// Address is where the helper publishes the port; invalid (a helper
	// started before portForwardAddress) means 127.0.0.1.
	Address netip.Addr
	Local   int
	Remote  int
}

// LoopbackForward is the address a forward publishes on by default.
var LoopbackForward = netip.AddrFrom4([4]byte{127, 0, 0, 1})

// ForwardIsLocal reports whether a forward published on addr is reachable
// from this machine only. An invalid addr is the default, loopback. Any
// other address — 0.0.0.0 most of all — puts the forward on the network.
func ForwardIsLocal(addr netip.Addr) bool { return !addr.IsValid() || addr.IsLoopback() }

// Listen is where the forward listens, as the user would type it:
// "localhost:15000" on loopback, else the address, "0.0.0.0:15000".
func (f PortForward) Listen() string {
	if ForwardIsLocal(f.Address) {
		return "localhost:" + strconv.Itoa(f.Local)
	}
	return netip.AddrPortFrom(f.Address, uint16(f.Local)).String() //nolint:gosec // a port, 1-65535
}

// URL is where the forward answers: localhost unless it was published on
// one address other than loopback, which is then the one that answers.
func (f PortForward) URL() string {
	if ForwardIsLocal(f.Address) || f.Address.IsUnspecified() {
		return "http://localhost:" + strconv.Itoa(f.Local)
	}
	return "http://" + f.Listen()
}

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

// ParseForwardSpecs reads what the shift-f prompt takes: one or more specs
// as ParseForwardSpec reads them, comma-separated. A local port may appear
// once — two forwards cannot share it.
func ParseForwardSpecs(s string) ([]ForwardSpec, error) {
	if len(s) > maxForwardSpecLen {
		return nil, fmt.Errorf("%d bytes is too long for a forward list (at most %d)", len(s), maxForwardSpecLen)
	}
	parts := strings.Split(s, ",")
	if len(parts) > maxForwardSpecs {
		return nil, fmt.Errorf("%d forwards is too many (at most %d)", len(parts), maxForwardSpecs)
	}
	specs := make([]ForwardSpec, 0, len(parts))
	seen := map[int]bool{}
	for _, p := range parts {
		local, remote, err := ParseForwardSpec(p)
		if err != nil {
			return nil, err
		}
		if seen[local] {
			return nil, fmt.Errorf("local port %d is forwarded twice", local)
		}
		seen[local] = true
		specs = append(specs, ForwardSpec{Local: local, Remote: remote})
	}
	return specs, nil
}

// LabelForwards reads a container's forward labels: the forwards they ask
// for, and whether they came from LabelAutoPortForwards, which wins. With
// neither label it returns nil and no error. A label that does not parse
// is an error naming it; its forwards are never half-taken.
func LabelForwards(c Container) (specs []ForwardSpec, auto bool, err error) {
	key := LabelAutoPortForwards
	value, ok := c.Labels[key]
	if !ok {
		key = LabelPortForwards
		if value, ok = c.Labels[key]; !ok {
			return nil, false, nil
		}
	}
	auto = key == LabelAutoPortForwards
	specs, err = parseLabelForwards(c, value)
	if err != nil {
		return nil, auto, fmt.Errorf("%s's label %s: %w", c.Name, key, err)
	}
	return specs, auto, nil
}

// parseLabelForwards strips each spec's k9s-style "name::" — which must
// name this container or its compose service, since a label naming another
// is a mistake to report, not to act on — and parses what is left as the
// prompt would.
func parseLabelForwards(c Container, value string) ([]ForwardSpec, error) {
	if len(value) > maxForwardSpecLen {
		return nil, fmt.Errorf("%d bytes is too long for a forward list (at most %d)", len(value), maxForwardSpecLen)
	}
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("is empty")
	}
	parts := strings.Split(value, ",")
	for i, p := range parts {
		name, spec, named := strings.Cut(p, "::")
		if !named {
			continue
		}
		name = strings.TrimSpace(name)
		if name != c.Name && (c.Service == "" || name != c.Service) {
			return nil, fmt.Errorf("%q names another container", name)
		}
		parts[i] = spec
	}
	return ParseForwardSpecs(strings.Join(parts, ","))
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
// port inside the target's network, published on addr — loopback unless
// portForwardAddress says otherwise — relaying to the target's address.
// AutoRemove makes stopping it the whole cleanup.
func forwardConfig(
	target Container,
	addr netip.Addr,
	local, remote int,
) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	ep, err := forwardEndpoint(target)
	if err != nil {
		return nil, nil, nil, err
	}
	if !addr.IsValid() {
		addr = LoopbackForward
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
			LabelForwardAddress:    addr.String(),
		},
	}
	host := &container.HostConfig{
		AutoRemove: true,
		PortBindings: network.PortMap{port: {{
			HostIP:   addr,
			HostPort: strconv.Itoa(local),
		}}},
	}
	nets := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{ep.Network: {}}}
	return cfg, host, nets, nil
}

// StartPortForward forwards addr:local to target:remote through a helper
// container, pulling the helper image the first time.
//
// Docker cannot publish a new port on a running container, so this is how
// a forward has to work: a relay that joins the target's network and
// publishes the port itself. The binding is on addr — 127.0.0.1 when it is
// invalid — so by default the forward is reachable from this machine only.
func (c *Client) StartPortForward(
	ctx context.Context,
	target Container,
	addr netip.Addr,
	local, remote int,
) (PortForward, error) {
	if !addr.IsValid() {
		addr = LoopbackForward
	}
	cfg, host, nets, err := forwardConfig(target, addr, local, remote)
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
		where := PortForward{Address: addr, Local: local}.Listen()
		return PortForward{}, fmt.Errorf("starting the forward on %s: %w", where, err)
	}
	return PortForward{
		Address:    addr,
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
		local, _ := strconv.Atoi(s.Labels[LabelForwardLocal])     //nolint:errcheck // our own label; 0 on garbage
		remote, _ := strconv.Atoi(s.Labels[LabelForwardRemote])   //nolint:errcheck // our own label; 0 on garbage
		addr, _ := netip.ParseAddr(s.Labels[LabelForwardAddress]) //nolint:errcheck // absent or garbage: loopback
		out = append(out, PortForward{
			Address:    addr,
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
