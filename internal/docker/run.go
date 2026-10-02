package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
)

// RunSpec is `docker run -d` as the run form fills it in.
type RunSpec struct {
	Image string
	Name  string
	// Ports are "8080:80", "127.0.0.1:8080:80" or "80" (a random host
	// port), as -p takes them.
	Ports []string
	// Env are "KEY=value", as -e takes them.
	Env []string
	// Volumes are "host-path-or-volume:container-path[:ro]", as -v takes them.
	Volumes []string
	// Command replaces the image's default command when set.
	Command []string
	// Remove deletes the container when it exits (--rm).
	Remove bool
}

// RunConfig turns a spec into the create request, or explains the first
// part of it that is wrong — before anything reaches the daemon.
func RunConfig(s RunSpec) (*container.Config, *container.HostConfig, error) {
	if strings.TrimSpace(s.Image) == "" {
		return nil, nil, fmt.Errorf("no image to run")
	}
	exposed, bindings, err := nat.ParsePortSpecs(s.Ports)
	if err != nil {
		return nil, nil, fmt.Errorf("ports: %w", err)
	}
	for _, e := range s.Env {
		if k, _, ok := strings.Cut(e, "="); !ok || k == "" {
			return nil, nil, fmt.Errorf("env %q is not KEY=value", e)
		}
	}
	for _, v := range s.Volumes {
		if parts := strings.Split(v, ":"); len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return nil, nil, fmt.Errorf("volume %q is not source:/path", v)
		}
	}
	cfg := &container.Config{
		Image:        s.Image,
		Env:          s.Env,
		ExposedPorts: exposed,
		Cmd:          s.Command,
	}
	host := &container.HostConfig{
		PortBindings: bindings,
		Binds:        s.Volumes,
		AutoRemove:   s.Remove,
	}
	return cfg, host, nil
}

// Run creates and starts a container from spec, detached, and returns its
// ID. A container that was created but would not start is removed, so a
// bad port or a missing bind path does not leave a dead container behind.
func (c *Client) Run(ctx context.Context, s RunSpec) (string, error) {
	cfg, host, err := RunConfig(s)
	if err != nil {
		return "", err
	}
	created, err := c.api.ContainerCreate(ctx, cfg, host, nil, nil, s.Name)
	if err != nil {
		return "", fmt.Errorf("creating the container: %w", err)
	}
	if err := c.api.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		cleanup := container.RemoveOptions{Force: true}
		_ = c.api.ContainerRemove(ctx, created.ID, cleanup) //nolint:errcheck // best effort; the start error is reported
		return "", fmt.Errorf("starting the container: %w", err)
	}
	return created.ID, nil
}
