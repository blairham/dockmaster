// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// Cluster is a Kubernetes cluster whose nodes are containers on a daemon:
// kind or k3d, its name, its node containers, and whether any is running.
type Cluster struct {
	Tool            string
	Name            string
	Registry        string
	Nodes           []string
	Running         bool
	RegistryRunning bool
}

// IsClusterRegistry reports whether a container is a kind cluster's local
// registry, as kind's recipe makes one: named kind-registry, or a registry
// image attached to the "kind" network.
func IsClusterRegistry(c Container) bool {
	if c.Name == DefaultRegistry.Name {
		return true
	}
	if c.Image != "registry" && !strings.HasPrefix(c.Image, "registry:") {
		return false
	}
	for _, e := range c.Endpoints {
		if e.Network == "kind" {
			return true
		}
	}
	return false
}

// SetRegistryRunning starts or stops a cluster's registry container.
func (c *Client) SetRegistryRunning(ctx context.Context, name string, on bool) error {
	var err error
	if on {
		_, err = c.api.ContainerStart(ctx, name, client.ContainerStartOptions{})
	} else {
		_, err = c.api.ContainerStop(ctx, name, client.ContainerStopOptions{})
	}
	if err != nil {
		return fmt.Errorf("registry %s: %w", name, err)
	}
	return nil
}

// String is "kind k8s".
func (c Cluster) String() string { return c.Tool + " " + c.Name }

// clusterLabels are the labels kind and k3d put a cluster's name under.
var clusterLabels = []struct{ tool, label string }{
	{tool: "kind", label: "io.x-k8s.kind.cluster"},
	{tool: "k3d", label: "k3d.cluster"},
}

// Clusters lists the kind and k3d clusters on the daemon at host, running
// or stopped — how dockmaster knows a machine already has Kubernetes. It
// dials host itself, so it works for machines dockmaster is not connected to.
func Clusters(ctx context.Context, host string) ([]Cluster, error) {
	c, err := New(host)
	if err != nil {
		return nil, err
	}
	defer c.Close() //nolint:errcheck // probe client
	return c.Clusters(ctx)
}

// Clusters is the package-level Clusters on this client's daemon.
func (c *Client) Clusters(ctx context.Context) ([]Cluster, error) {
	var out []Cluster
	for _, probe := range clusterLabels {
		list, err := c.api.ContainerList(ctx, client.ContainerListOptions{
			All:     true,
			Filters: make(client.Filters).Add("label", probe.label),
		})
		if err != nil {
			return nil, err
		}
		byName := map[string]*Cluster{}
		for _, s := range list.Items {
			name := s.Labels[probe.label]
			if name == "" || isK3dHelper(s.Labels) {
				continue
			}
			cl := byName[name]
			if cl == nil {
				cl = &Cluster{Tool: probe.tool, Name: name}
				byName[name] = cl
			}
			if len(s.Names) > 0 {
				cl.Nodes = append(cl.Nodes, trimSlash(s.Names[0]))
			}
			cl.Running = cl.Running || s.State == container.StateRunning
		}
		for _, cl := range byName {
			sort.Strings(cl.Nodes)
			out = append(out, *cl)
		}
	}
	if err := c.attachRegistry(ctx, out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// attachRegistry finds the kind registry on the daemon, if any, and gives
// it to every kind cluster there — they share it over the kind network.
func (c *Client) attachRegistry(ctx context.Context, clusters []Cluster) error {
	hasKind := false
	for _, cl := range clusters {
		hasKind = hasKind || cl.Tool == "kind"
	}
	if !hasKind {
		return nil
	}
	list, err := c.api.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return err
	}
	for _, s := range list.Items {
		r := newContainer(s)
		if !IsClusterRegistry(r) {
			continue
		}
		for i := range clusters {
			if clusters[i].Tool == "kind" {
				clusters[i].Registry, clusters[i].RegistryRunning = r.Name, r.Running()
			}
		}
		return nil
	}
	return nil
}

// isK3dHelper is k3d's load balancer or registry: labeled with the
// cluster, but not a node.
func isK3dHelper(labels map[string]string) bool {
	switch labels["k3d.role"] {
	case "loadbalancer", "registry", "noRole":
		return true
	}
	return false
}

func trimSlash(name string) string {
	if name != "" && name[0] == '/' {
		return name[1:]
	}
	return name
}

// SetClusterRunning starts or stops a cluster's node containers. kind and
// k3d clusters come back from a docker stop/start as they were; nothing
// else about the cluster changes, and the registry is left alone.
func (c *Client) SetClusterRunning(ctx context.Context, cl Cluster, on bool) error {
	for _, n := range cl.Nodes {
		var err error
		if on {
			_, err = c.api.ContainerStart(ctx, n, client.ContainerStartOptions{})
		} else {
			_, err = c.api.ContainerStop(ctx, n, client.ContainerStopOptions{})
		}
		if err != nil {
			return fmt.Errorf("%s node %s: %w", cl, n, err)
		}
	}
	return nil
}

// Registry is the local registry next to a kind cluster, as kind's own
// "local registry" recipe sets it up: registry:2, published on
// 127.0.0.1:<Port>, restarting with the daemon.
type Registry struct {
	Name string // container name, "kind-registry"
	Port string // host port, "5001"
}

// DefaultRegistry is kind's documented local registry.
var DefaultRegistry = Registry{Name: "kind-registry", Port: "5001"}

// registryImage is the image kind's recipe runs.
const registryImage = "registry:2"

// EnsureRegistry makes sure the registry container exists and is running:
// created (pulling registry:2 if needed) when absent, started when stopped,
// left alone when up. A registry already there is reused, not replaced.
func (c *Client) EnsureRegistry(ctx context.Context, r Registry) error {
	insp, err := c.api.ContainerInspect(ctx, r.Name, client.ContainerInspectOptions{})
	switch {
	case err == nil:
		if insp.Container.State != nil && insp.Container.State.Running {
			return nil
		}
		_, err = c.api.ContainerStart(ctx, r.Name, client.ContainerStartOptions{})
		return err
	case !cerrdefs.IsNotFound(err):
		return fmt.Errorf("checking %s: %w", r.Name, err)
	}
	if _, ierr := c.api.ImageInspect(ctx, registryImage); ierr != nil {
		if perr := c.PullImage(ctx, registryImage); perr != nil {
			return perr
		}
	}
	port := network.MustParsePort("5000/tcp")
	created, err := c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{Image: registryImage, ExposedPorts: network.PortSet{port: {}}},
		HostConfig: &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			PortBindings: network.PortMap{port: {{
				HostIP:   netip.AddrFrom4([4]byte{127, 0, 0, 1}),
				HostPort: r.Port,
			}}},
		},
		Name: r.Name,
	})
	if err != nil {
		return fmt.Errorf("creating %s: %w", r.Name, err)
	}
	_, err = c.api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{})
	return err
}

// ConnectRegistry attaches the registry to the cluster's network ("kind"),
// so nodes reach it by name. Already connected is fine.
func (c *Client) ConnectRegistry(ctx context.Context, r Registry, networkName string) error {
	_, err := c.api.NetworkConnect(ctx, networkName, client.NetworkConnectOptions{
		Container:      r.Name,
		EndpointConfig: &network.EndpointSettings{},
	})
	if err != nil && !cerrdefs.IsConflict(err) && !cerrdefs.IsAlreadyExists(err) && !alreadyConnected(err) {
		return fmt.Errorf("connecting %s to %s: %w", r.Name, networkName, err)
	}
	return nil
}

// alreadyConnected matches the daemon's "endpoint ... already exists"
// answer, which older daemons do not classify as a conflict.
func alreadyConnected(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "already exists")
}

// RegistryHostsToml is what each node's containerd reads for
// localhost:<port>: pull and resolve through the registry container.
func RegistryHostsToml(r Registry) string {
	return fmt.Sprintf("[host.\"http://%s:5000\"]\n  capabilities = [\"pull\", \"resolve\"]\n", r.Name)
}

// WireRegistry points each node's containerd at the registry, so an image
// pushed to localhost:<port> on this machine pulls inside the cluster.
func (c *Client) WireRegistry(ctx context.Context, cl Cluster, r Registry) error {
	dir := "/etc/containerd/certs.d/localhost:" + r.Port
	script := fmt.Sprintf("mkdir -p '%s' && cat > '%s/hosts.toml' <<'EOF'\n%sEOF\n", dir, dir, RegistryHostsToml(r))
	for _, n := range cl.Nodes {
		if _, err := c.Exec(ctx, n, "sh", "-c", script); err != nil {
			return fmt.Errorf("wiring the registry into %s: %w", n, err)
		}
	}
	return nil
}
