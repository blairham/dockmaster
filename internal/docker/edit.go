// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/docker/go-units"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// RestartPolicies are the policies the edit form offers, as docker names
// them.
var RestartPolicies = []string{"no", "on-failure", "unless-stopped", "always"}

// EditState is what the edit form changes, as the container has it now.
type EditState struct {
	Name       string
	Restart    string
	NanoCPUs   int64 // 0: no CPU limit
	Memory     int64 // bytes; 0: no limit
	MemorySwap int64 // bytes of memory+swap; -1 unlimited swap, 0 unset
}

// EditSpec is a submitted edit form: the fields as typed.
type EditSpec struct {
	Name    string `json:"name"`
	CPUs    string `json:"cpus"`   // "1.5"; "" leaves it
	Memory  string `json:"memory"` // "512m", "2g"; "" leaves it
	Restart string `json:"restart"`
}

// EditState reads a container's current name, limits and restart policy.
func (c *Client) EditState(ctx context.Context, id string) (EditState, error) {
	res, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return EditState{}, fmt.Errorf("inspecting %s: %w", shortID(id), err)
	}
	insp := res.Container
	s := EditState{Name: strings.TrimPrefix(insp.Name, "/"), Restart: "no"}
	if h := insp.HostConfig; h != nil {
		s.NanoCPUs, s.Memory, s.MemorySwap = h.NanoCPUs, h.Memory, h.MemorySwap
		if h.RestartPolicy.Name != "" {
			s.Restart = string(h.RestartPolicy.Name)
		}
	}
	return s, nil
}

// CPUsText is a NanoCPUs value as the form shows it: "1.5", or "" for none.
func CPUsText(nano int64) string {
	if nano <= 0 {
		return ""
	}
	return strconv.FormatFloat(float64(nano)/1e9, 'f', -1, 64)
}

// MemoryText is a byte count as the form shows it: "512m", or "" for none.
func MemoryText(b int64) string {
	if b <= 0 {
		return ""
	}
	for _, u := range []struct {
		suffix string
		size   int64
	}{{suffix: "g", size: 1 << 30}, {suffix: "m", size: 1 << 20}, {suffix: "k", size: 1 << 10}} {
		if b%u.size == 0 {
			return strconv.FormatInt(b/u.size, 10) + u.suffix
		}
	}
	return strconv.FormatInt(b, 10)
}

// EditUpdate works out the docker update an edit needs, from the current
// state: the resources and restart policy that changed, and a new name.
// It explains a field it cannot read instead of guessing.
//
// Memory is the subtle one: the daemon refuses a memory limit above the
// container's existing memory+swap limit ("update the memoryswap at the
// same time"), so a memory change carries a swap value too — keeping the
// swap headroom the container had, unlimited staying unlimited, and docker
// run's default of twice the memory where it had no limit.
func EditUpdate(cur EditState, s EditSpec) (container.UpdateConfig, bool, string, error) {
	var u container.UpdateConfig
	changed := false

	if t := strings.TrimSpace(s.CPUs); t != "" && t != CPUsText(cur.NanoCPUs) {
		cpus, err := strconv.ParseFloat(t, 64)
		if err != nil || cpus <= 0 {
			return u, false, "", fmt.Errorf("cpus: %q is not a number of CPUs, e.g. 1.5", t)
		}
		u.NanoCPUs = int64(cpus * 1e9)
		changed = true
	}

	if t := strings.TrimSpace(s.Memory); t != "" && t != MemoryText(cur.Memory) {
		mem, swap, err := editMemory(cur, t)
		if err != nil {
			return u, false, "", err
		}
		u.Memory, u.MemorySwap = mem, swap
		changed = true
	}

	if r := strings.TrimSpace(s.Restart); r != "" && r != cur.Restart {
		u.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyMode(r)}
		changed = true
	}

	rename := ""
	if n := strings.TrimSpace(s.Name); n != "" && n != cur.Name {
		rename = n
	}
	return u, changed, rename, nil
}

// editMemory parses a new memory limit and the swap value that must go with
// it: the swap headroom the container had, unlimited staying unlimited, and
// docker run's default of twice the memory where it had no limit.
func editMemory(cur EditState, t string) (mem, swap int64, err error) {
	mem, err = units.RAMInBytes(t)
	if err != nil || mem <= 0 {
		return 0, 0, fmt.Errorf("memory: %q is not a size, e.g. 512m or 2g", t)
	}
	if mem < 6<<20 {
		return 0, 0, fmt.Errorf("memory: docker's minimum is 6m")
	}
	switch {
	case cur.MemorySwap == -1:
		swap = -1
	case cur.MemorySwap > 0 && cur.Memory > 0:
		swap = mem + (cur.MemorySwap - cur.Memory)
	default:
		swap = 2 * mem
	}
	return mem, swap, nil
}

// Edit applies an edit: docker update for limits and restart policy, then
// docker rename. Nothing is sent for a field that did not change.
func (c *Client) Edit(ctx context.Context, id string, s EditSpec) error {
	cur, err := c.EditState(ctx, id)
	if err != nil {
		return err
	}
	u, changed, rename, err := EditUpdate(cur, s)
	if err != nil {
		return err
	}
	if changed {
		// The client takes the two halves of the update separately and
		// sends them as one UpdateConfig, so the request body is the same
		// u the old ContainerUpdate(ctx, id, u) sent.
		if _, err := c.api.ContainerUpdate(ctx, id, client.ContainerUpdateOptions{
			Resources:     &u.Resources,
			RestartPolicy: &u.RestartPolicy,
		}); err != nil {
			return fmt.Errorf("updating %s: %w", cur.Name, err)
		}
	}
	if rename != "" {
		if _, err := c.api.ContainerRename(ctx, id, client.ContainerRenameOptions{NewName: rename}); err != nil {
			return fmt.Errorf("renaming %s: %w", cur.Name, err)
		}
	}
	return nil
}
