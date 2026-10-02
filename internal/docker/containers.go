// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
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
	res, err := c.api.ContainerList(ctx, client.ContainerListOptions{All: all})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	out := make([]Container, 0, len(res.Items))
	for _, s := range res.Items {
		ctr := newContainer(s)
		// Created from an image ID — as Kubernetes's cri-dockerd creates
		// every pod container — the container's image reads as a bare
		// sha256. Show the image's name instead, where it has one.
		if strings.HasPrefix(ctr.Image, "sha256:") {
			if name := c.imageName(ctx, ctr.Image); name != "" {
				ctr.Image = name
			}
		}
		out = append(out, ctr)
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
			// The address is a netip.Addr now; the zero value is "no
			// address", which must stay "" rather than print "invalid IP".
			if ep != nil && ep.IPAddress.IsValid() {
				e.IP = ep.IPAddress.String()
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
		State:     string(s.State),
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
func formatPorts(ports []container.PortSummary) string {
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
	res, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{Size: false})
	if err != nil {
		return nil, fmt.Errorf("inspecting container %s: %w", shortID(id), err)
	}
	return indentJSON(res.Raw)
}

// StartContainer starts a stopped container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	if _, err := c.api.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("starting %s: %w", shortID(id), err)
	}
	return nil
}

// StopContainer stops a running container, giving it timeout seconds to
// exit before the daemon escalates to SIGKILL.
func (c *Client) StopContainer(ctx context.Context, id string, timeout int) error {
	if _, err := c.api.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stopping %s: %w", shortID(id), err)
	}
	return nil
}

// RestartContainer restarts a container.
func (c *Client) RestartContainer(ctx context.Context, id string, timeout int) error {
	if _, err := c.api.ContainerRestart(ctx, id, client.ContainerRestartOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("restarting %s: %w", shortID(id), err)
	}
	return nil
}

// KillContainer sends a signal (default SIGKILL) to a container.
func (c *Client) KillContainer(ctx context.Context, id, signal string) error {
	if _, err := c.api.ContainerKill(ctx, id, client.ContainerKillOptions{Signal: signal}); err != nil {
		return fmt.Errorf("killing %s: %w", shortID(id), err)
	}
	return nil
}

// PauseContainer freezes every process in the container's cgroup.
func (c *Client) PauseContainer(ctx context.Context, id string) error {
	if _, err := c.api.ContainerPause(ctx, id, client.ContainerPauseOptions{}); err != nil {
		return fmt.Errorf("pausing %s: %w", shortID(id), err)
	}
	return nil
}

// UnpauseContainer thaws a paused container.
func (c *Client) UnpauseContainer(ctx context.Context, id string) error {
	if _, err := c.api.ContainerUnpause(ctx, id, client.ContainerUnpauseOptions{}); err != nil {
		return fmt.Errorf("unpausing %s: %w", shortID(id), err)
	}
	return nil
}

// RemoveContainer deletes a container. force kills it first; volumes also
// drops its anonymous volumes.
func (c *Client) RemoveContainer(ctx context.Context, id string, force, volumes bool) error {
	_, err := c.api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{
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
	res, err := c.api.ContainerPrune(ctx, client.ContainerPruneOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("pruning containers: %w", err)
	}
	return len(res.Report.ContainersDeleted), res.Report.SpaceReclaimed, nil
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

// imageName is what image id is called: its first tag, else its repository
// from a digest ("rancher/mirrored-coredns-coredns@sha256:…"), else "".
// Each ID is inspected once, ever — listing every image to find it can
// take minutes on a large host, and an ID's name does not change.
func (c *Client) imageName(ctx context.Context, id string) string {
	c.imageMu.Lock()
	name, ok := c.imageNames[id]
	c.imageMu.Unlock()
	if ok {
		return name
	}
	insp, err := c.api.ImageInspect(ctx, id)
	if err != nil {
		return "" // not cached: the next refresh asks again
	}
	for _, t := range insp.RepoTags {
		if t != "" && t != "<none>:<none>" {
			name = t
			break
		}
	}
	if name == "" && len(insp.RepoDigests) > 0 {
		if repo, digest, ok := strings.Cut(insp.RepoDigests[0], "@"); ok {
			name = repo + "@" + shortDigest(digest)
		}
	}
	c.imageMu.Lock()
	if c.imageNames == nil {
		c.imageNames = map[string]string{}
	}
	c.imageNames[id] = name
	c.imageMu.Unlock()
	return name
}

// shortDigest is "sha256:" and the first twelve hex digits of a digest.
func shortDigest(d string) string {
	algo, hex, ok := strings.Cut(d, ":")
	if !ok || len(hex) <= 12 {
		return d
	}
	return algo + ":" + hex[:12]
}

// ExitCode is the code a stopped container exited with, read from docker's
// status ("Exited (137) 2 hours ago"); ok is false for one that has not
// exited.
func (c Container) ExitCode() (int, bool) {
	rest, ok := strings.CutPrefix(c.Status, "Exited (")
	if !ok {
		return 0, false
	}
	code, _, ok := strings.Cut(rest, ")")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(code)
	return n, err == nil
}

// Fault reports a container in trouble, as k9s's faults are pods that are
// neither running healthily nor completed: unhealthy, restarting, dead, or
// exited with a non-zero code. A clean exit 0 is finished, not failed.
func (c Container) Fault() bool {
	switch {
	case c.Health == "unhealthy", c.State == "restarting", c.State == "dead":
		return true
	}
	code, exited := c.ExitCode()
	return exited && code != 0
}
