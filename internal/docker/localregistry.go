// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// The local registry dockmaster runs itself (#120).
const (
	// LocalRegistryLabel marks dockmaster's own registry container.
	LocalRegistryLabel = "dockmaster.registry"
	// localRegistryValue is LocalRegistryLabel's value on it.
	localRegistryValue = "local"
	// deleteEnabledEnv lets a registry:2 delete a manifest; without it the
	// registry answers every DELETE with 405.
	deleteEnabledEnv = "REGISTRY_STORAGE_DELETE_ENABLED=true"
)

// The kinds of registry container LocalRegistries finds.
const (
	RegistryKindDockmaster = "dockmaster"
	RegistryKindKind       = "kind"
	RegistryKindK3d        = "k3d"
	RegistryKindOther      = registryName
)

// registryName is the registry image's name, its binary's, and k3d's role
// label for one.
const registryName = "registry"

// LocalRegistrySpec is dockmaster's own registry: registry:2 named name on
// 127.0.0.1:port, its storage in the named volume <name>-data so images
// survive a removed container, labeled so dockmaster finds it, and with
// deletes enabled so a tag can be removed from the browser.
func LocalRegistrySpec(name, port string) Registry {
	return Registry{
		Name:   name,
		Port:   port,
		Volume: name + "-data",
		Labels: map[string]string{LocalRegistryLabel: localRegistryValue},
		Env:    []string{deleteEnabledEnv},
	}
}

// LocalRegistry is a registry container on the daemon: dockmaster's own,
// a kind or k3d cluster's, or any other registry image.
type LocalRegistry struct {
	// Name is the container's name.
	Name string
	ID   string
	// Kind is RegistryKindDockmaster, RegistryKindKind, RegistryKindK3d or
	// RegistryKindOther.
	Kind string
	// Port is the host port the daemon publishes the registry's 5000 on;
	// empty when it publishes none, and the registry is reachable only from
	// other containers.
	Port string
	// Running reports the container's state.
	Running bool
	// DeleteEnabled reports that the registry was started with deletes
	// allowed (REGISTRY_STORAGE_DELETE_ENABLED=true).
	DeleteEnabled bool
}

// Address is how an image reference names the registry — localhost:<port>
// — which is also where the daemon pushes and pulls it: the daemon
// publishes the port on its own host. "" when no port is published.
func (r LocalRegistry) Address() string {
	if r.Port == "" {
		return ""
	}
	return net.JoinHostPort(localhost, r.Port)
}

// Managed reports that dockmaster created it, so dockmaster may remove it;
// a cluster's registry belongs to the cluster.
func (r LocalRegistry) Managed() bool { return r.Kind == RegistryKindDockmaster }

// registryKind says which kind of registry container s is, or "".
func registryKind(s container.Summary) string {
	c := newContainer(s)
	switch {
	case s.Labels[LocalRegistryLabel] == localRegistryValue:
		return RegistryKindDockmaster
	case s.Labels["k3d.role"] == registryName:
		return RegistryKindK3d
	case IsClusterRegistry(c):
		return RegistryKindKind
	case isRegistryImage(c.Image):
		return RegistryKindOther
	}
	return ""
}

// isRegistryImage reports whether image is the registry image, under any
// of the names it is pulled by.
func isRegistryImage(image string) bool {
	image = strings.TrimPrefix(image, "docker.io/")
	image = strings.TrimPrefix(image, "library/")
	name, _, _ := strings.Cut(image, "@")
	name, _, _ = strings.Cut(name, ":")
	return name == registryName
}

// LocalRegistries lists the registry containers on the daemon, running or
// stopped, dockmaster's own first, then by name. The published port is
// read from each one's inspect, since a stopped container's listing
// carries no ports.
func (c *Client) LocalRegistries(ctx context.Context) ([]LocalRegistry, error) {
	list, err := c.api.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	var out []LocalRegistry
	for _, s := range list.Items {
		kind := registryKind(s)
		if kind == "" {
			continue
		}
		r := LocalRegistry{
			ID: s.ID, Name: trimSlash(firstName(s.Names)), Kind: kind,
			Running: s.State == container.StateRunning,
		}
		insp, ierr := c.api.ContainerInspect(ctx, s.ID, client.ContainerInspectOptions{})
		if ierr != nil {
			return nil, fmt.Errorf("inspecting %s: %w", r.Name, ierr)
		}
		r.Port, r.DeleteEnabled = registryPublished(insp.Container)
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b LocalRegistry) int {
		if a.Managed() != b.Managed() {
			if a.Managed() {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// registryPublished is the host port a registry container publishes its
// 5000 on — the running binding, else the configured one — and whether
// deletes are enabled in its environment.
func registryPublished(insp container.InspectResponse) (port string, deletes bool) {
	if insp.NetworkSettings != nil {
		for _, b := range insp.NetworkSettings.Ports[registryPort] {
			if b.HostPort != "" {
				port = b.HostPort
				break
			}
		}
	}
	if port == "" && insp.HostConfig != nil {
		for _, b := range insp.HostConfig.PortBindings[registryPort] {
			if b.HostPort != "" {
				port = b.HostPort
				break
			}
		}
	}
	if insp.Config != nil {
		deletes = slices.Contains(insp.Config.Env, deleteEnabledEnv)
	}
	return port, deletes
}

// UpLocalRegistry makes sure a local registry is running and says which:
// an existing one is reused rather than duplicated — spec's own container
// first, else dockmaster's labeled one, else a kind or k3d cluster's (or
// any registry) that publishes a port — started when stopped. Only when
// there is none is spec created. reused reports that it was already there.
func (c *Client) UpLocalRegistry(ctx context.Context, spec Registry) (LocalRegistry, bool, error) {
	regs, err := c.LocalRegistries(ctx)
	if err != nil {
		return LocalRegistry{}, false, err
	}
	if r, ok := pickLocalRegistry(regs, spec.Name); ok {
		if !r.Running {
			if _, err := c.api.ContainerStart(ctx, r.ID, client.ContainerStartOptions{}); err != nil {
				return r, true, fmt.Errorf("starting %s: %w", r.Name, err)
			}
			r.Running = true
		}
		return r, true, nil
	}
	if err := c.EnsureRegistry(ctx, spec); err != nil {
		return LocalRegistry{}, false, err
	}
	return LocalRegistry{
		Name: spec.Name, Kind: RegistryKindDockmaster, Port: spec.Port, Running: true,
		DeleteEnabled: slices.Contains(spec.Env, deleteEnabledEnv),
	}, false, nil
}

// pickLocalRegistry is the registry `:registry up` reuses, by the order
// UpLocalRegistry documents.
func pickLocalRegistry(regs []LocalRegistry, name string) (LocalRegistry, bool) {
	for _, r := range regs {
		if r.Name == name {
			return r, true
		}
	}
	for _, r := range regs {
		if r.Managed() {
			return r, true
		}
	}
	for _, r := range regs {
		if r.Port != "" {
			return r, true
		}
	}
	return LocalRegistry{}, false
}

// errNoRegistryOutput is a garbage collection that printed nothing.
var errNoRegistryOutput = errors.New("garbage-collect printed nothing")

// GarbageCollectRegistry runs the registry's garbage collection inside its
// container — `registry garbage-collect --delete-untagged` over the
// image's own config — which frees the blobs no manifest references
// (deleting a tag removes only its manifest). It returns how many blobs it
// removed.
func (c *Client) GarbageCollectRegistry(ctx context.Context, name string) (int, error) {
	out, err := c.Exec(ctx, name, registryName, "garbage-collect", "--delete-untagged",
		"/etc/docker/registry/config.yml")
	if err != nil {
		return 0, fmt.Errorf("garbage-collecting %s: %w", name, err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return 0, fmt.Errorf("garbage-collecting %s: %w", name, errNoRegistryOutput)
	}
	return CountCollected(string(out)), nil
}

// CountCollected counts the blobs a garbage-collect run reports deleting.
func CountCollected(out string) int {
	n := 0
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "blob eligible for deletion") {
			n++
		}
	}
	return n
}
