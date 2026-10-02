package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
)

// Container is one row of the containers view — already flattened and
// formatted, so the view does no Engine-API reshaping of its own.
type Container struct {
	Created time.Time

	Labels map[string]string

	ID      string
	Name    string
	Image   string
	Command string
	State   string // created|running|paused|restarting|removing|exited|dead
	Status  string // human blurb, e.g. "Up 3 hours (healthy)"
	Health  string // healthy|unhealthy|starting|"" (parsed out of Status)
	Ports   string
	Network string
	IP      string
	Project string // compose project, "" when not compose-managed
	Service string // compose service

	// Endpoints are the networks the container is attached to and its
	// address on each, sorted by network name. PortList is its port map.
	Endpoints []Endpoint
	PortList  []PortMapping

	SizeRw int64
}

// Endpoint is a container's address on one network.
type Endpoint struct {
	Network string
	IP      string
}

// PortMapping is one entry of a container's port map. Public is 0 for a
// port that is exposed but not published.
type PortMapping struct {
	Type    string
	Private uint16
	Public  uint16
}

// Running reports whether this container is currently executing. Paused
// containers are deliberately excluded — they cannot serve traffic, and the
// stats poller must not wait on them.
func (c Container) Running() bool { return c.State == "running" }

// Short is the 12-char ID docker displays.
func (c Container) Short() string { return shortID(c.ID) }

// Age is the container's creation age, one unit.
func (c Container) Age() string { return since(c.Created) }

// Containers lists containers. all=false mirrors `docker ps` (running
// only); all=true mirrors `docker ps -a`.
func (c *Client) Containers(ctx context.Context, all bool) ([]Container, error) {
	raw, err := c.api.ContainerList(ctx, container.ListOptions{All: all})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	out := make([]Container, 0, len(raw))
	for _, s := range raw {
		out = append(out, newContainer(s))
	}
	// Running first, then by name — the same ordering k9s uses for pods, so
	// the thing you are most likely to act on never sorts below the debris.
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].Running(), out[j].Running()
		if ri != rj {
			return ri
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func newContainer(s container.Summary) Container {
	name := ""
	if len(s.Names) > 0 {
		name = strings.TrimPrefix(s.Names[0], "/")
	}

	net, ip := "", ""
	var endpoints []Endpoint
	if s.NetworkSettings != nil {
		names := make([]string, 0, len(s.NetworkSettings.Networks))
		for n, ep := range s.NetworkSettings.Networks {
			names = append(names, n)
			e := Endpoint{Network: n}
			if ep != nil {
				e.IP = ep.IPAddress
			}
			endpoints = append(endpoints, e)
		}
		sort.Strings(names)
		sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].Network < endpoints[j].Network })
		net = strings.Join(names, ",")
		// The first network's address, by name — map order would make it
		// a different network from one refresh to the next.
		for _, e := range endpoints {
			if e.IP != "" {
				ip = e.IP
				break
			}
		}
	}
	ports := make([]PortMapping, 0, len(s.Ports))
	for _, p := range s.Ports {
		ports = append(ports, PortMapping{Type: p.Type, Private: p.PrivatePort, Public: p.PublicPort})
	}
	if net == "" {
		net = s.HostConfig.NetworkMode
	}

	return Container{
		ID:        s.ID,
		Name:      name,
		Image:     s.Image,
		Command:   s.Command,
		Created:   time.Unix(s.Created, 0),
		State:     s.State,
		Status:    s.Status,
		Health:    parseHealth(s.Status),
		Ports:     formatPorts(s.Ports),
		Network:   net,
		IP:        ip,
		Labels:    s.Labels,
		Project:   s.Labels[LabelProject],
		Service:   s.Labels[LabelService],
		Endpoints: endpoints,
		PortList:  ports,
		SizeRw:    s.SizeRw,
	}
}

// parseHealth pulls the healthcheck verdict out of the status blurb. The
// list endpoint has no dedicated health field — the only place it appears
// is parenthesized inside Status ("Up 2 hours (healthy)"), so reading it
// here saves an inspect call per row on every poll.
func parseHealth(status string) string {
	open := strings.IndexByte(status, '(')
	if open < 0 || !strings.HasSuffix(status, ")") {
		return ""
	}
	inner := status[open+1 : len(status)-1]
	switch inner {
	case "healthy", "unhealthy", "starting", "health: starting":
		if inner == "health: starting" {
			return "starting"
		}
		return inner
	}
	return ""
}

// formatPorts renders the port map the way `docker ps` does, collapsing the
// published mappings ahead of the merely-exposed ones. Unpublished ports are
// noise in a narrow column, so they only appear when nothing is published.
func formatPorts(ports []container.Port) string {
	published := make([]string, 0, len(ports))
	exposed := make([]string, 0, len(ports))
	for _, p := range ports {
		if p.PublicPort != 0 {
			published = append(published, fmt.Sprintf("%d→%d/%s", p.PublicPort, p.PrivatePort, p.Type))
			continue
		}
		exposed = append(exposed, fmt.Sprintf("%d/%s", p.PrivatePort, p.Type))
	}
	sort.Strings(published)
	sort.Strings(exposed)
	if len(published) > 0 {
		return strings.Join(published, " ")
	}
	return strings.Join(exposed, " ")
}

// InspectContainer returns the pretty-printed inspect JSON for a container.
func (c *Client) InspectContainer(ctx context.Context, id string) ([]byte, error) {
	_, raw, err := c.api.ContainerInspectWithRaw(ctx, id, false)
	if err != nil {
		return nil, fmt.Errorf("inspecting container %s: %w", shortID(id), err)
	}
	return indentJSON(raw)
}

// StartContainer starts a stopped container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	if err := c.api.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return fmt.Errorf("starting %s: %w", shortID(id), err)
	}
	return nil
}

// StopContainer stops a running container, giving it timeout seconds to
// exit before the daemon escalates to SIGKILL.
func (c *Client) StopContainer(ctx context.Context, id string, timeout int) error {
	if err := c.api.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stopping %s: %w", shortID(id), err)
	}
	return nil
}

// RestartContainer restarts a container.
func (c *Client) RestartContainer(ctx context.Context, id string, timeout int) error {
	if err := c.api.ContainerRestart(ctx, id, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("restarting %s: %w", shortID(id), err)
	}
	return nil
}

// KillContainer sends a signal (default SIGKILL) to a container.
func (c *Client) KillContainer(ctx context.Context, id, signal string) error {
	if err := c.api.ContainerKill(ctx, id, signal); err != nil {
		return fmt.Errorf("killing %s: %w", shortID(id), err)
	}
	return nil
}

// PauseContainer freezes every process in the container's cgroup.
func (c *Client) PauseContainer(ctx context.Context, id string) error {
	if err := c.api.ContainerPause(ctx, id); err != nil {
		return fmt.Errorf("pausing %s: %w", shortID(id), err)
	}
	return nil
}

// UnpauseContainer thaws a paused container.
func (c *Client) UnpauseContainer(ctx context.Context, id string) error {
	if err := c.api.ContainerUnpause(ctx, id); err != nil {
		return fmt.Errorf("unpausing %s: %w", shortID(id), err)
	}
	return nil
}

// RemoveContainer deletes a container. force kills it first; volumes also
// drops its anonymous volumes.
func (c *Client) RemoveContainer(ctx context.Context, id string, force, volumes bool) error {
	err := c.api.ContainerRemove(ctx, id, container.RemoveOptions{
		Force:         force,
		RemoveVolumes: volumes,
	})
	if err != nil {
		return fmt.Errorf("removing %s: %w", shortID(id), err)
	}
	return nil
}

// PruneContainers deletes every stopped container and reports how much
// space came back.
func (c *Client) PruneContainers(ctx context.Context) (deleted int, reclaimed uint64, err error) {
	rep, err := c.api.ContainersPrune(ctx, filters.NewArgs())
	if err != nil {
		return 0, 0, fmt.Errorf("pruning containers: %w", err)
	}
	return len(rep.ContainersDeleted), rep.SpaceReclaimed, nil
}

// KubeRef is a Kubernetes container's identity, from the labels
// cri-dockerd puts on the docker containers it runs pods as — what a
// runtime's built-in Kubernetes (Rancher Desktop, Docker Desktop) shows
// on its docker daemon.
type KubeRef struct {
	Namespace string
	Pod       string
	Container string
	// Sandbox is the pod's pause container, holding its namespaces: pure
	// plumbing, with nothing to act on.
	Sandbox bool
}

// Kube reports whether c runs part of a Kubernetes pod, and which.
func (c Container) Kube() (KubeRef, bool) {
	pod := c.Labels["io.kubernetes.pod.name"]
	if pod == "" {
		return KubeRef{}, false
	}
	return KubeRef{
		Namespace: c.Labels["io.kubernetes.pod.namespace"],
		Pod:       pod,
		Container: c.Labels["io.kubernetes.container.name"],
		Sandbox:   c.Labels["io.kubernetes.docker.type"] == "podsandbox",
	}, true
}
