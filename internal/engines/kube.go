// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package engines

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/docker/go-units"
)

// kubeStatus runs argv and reads the cluster's state from its output.
func kubeStatus(run Runner, argv []string, parse func([]byte) (on, ok bool)) func(context.Context) (bool, bool) {
	return func(ctx context.Context) (bool, bool) {
		out, err := run(ctx, argv[0], argv[1:]...)
		if err != nil {
			return false, false
		}
		return parse(out)
	}
}

// OrbStackKubeStatus reads `orb config get k8s.enable`: "true" or "false".
func OrbStackKubeStatus(out []byte) (on, ok bool) {
	switch strings.TrimSpace(string(out)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// RancherResources reads `rdctl list-settings`: virtualMachine.numberCPUs
// and memoryInGB, and experimental.virtualMachine.diskSize ("100GiB").
func RancherResources(out []byte) (Resources, bool) {
	var s struct {
		Experimental struct {
			VM struct {
				Disk string `json:"diskSize"`
			} `json:"virtualMachine"`
		} `json:"experimental"`
		VM struct {
			CPUs   int     `json:"numberCPUs"`
			Memory float64 `json:"memoryInGB"`
		} `json:"virtualMachine"`
	}
	if json.Unmarshal(out, &s) != nil || s.VM.CPUs == 0 {
		return Resources{}, false
	}
	r := Resources{CPUs: s.VM.CPUs, Memory: int64(s.VM.Memory * (1 << 30))}
	if d, err := units.RAMInBytes(s.Experimental.VM.Disk); err == nil {
		r.Disk = d
	}
	return r, true
}

// resources runs a settings command and reads a VM's size from it.
func resources(
	run Runner,
	argv []string,
	parse func([]byte) (Resources, bool),
) func(context.Context) (Resources, bool) {
	return func(ctx context.Context) (Resources, bool) {
		out, err := run(ctx, argv[0], argv[1:]...)
		if err != nil {
			return Resources{}, false
		}
		return parse(out)
	}
}

// RancherKubeStatus reads `rdctl list-settings`: kubernetes.enabled.
func RancherKubeStatus(out []byte) (on, ok bool) {
	var s struct {
		Kubernetes struct {
			Enabled *bool `json:"enabled"`
		} `json:"kubernetes"`
	}
	if json.Unmarshal(out, &s) != nil || s.Kubernetes.Enabled == nil {
		return false, false
	}
	return *s.Kubernetes.Enabled, true
}

// DockerDesktopKubeStatus reads `docker desktop kubernetes status --format
// json`. Its shape is not documented and was not available to check, so
// this only trusts a top-level status string it recognizes; anything else
// is "unknown" rather than a guess.
func DockerDesktopKubeStatus(out []byte) (on, ok bool) {
	var fields map[string]any
	if json.Unmarshal(out, &fields) != nil {
		return false, false
	}
	for k, v := range fields {
		s, isString := v.(string)
		if !isString || !strings.EqualFold(k, "status") {
			continue
		}
		switch strings.ToLower(s) {
		case "running", "started", "enabled":
			return true, true
		case "stopped", "disabled", "not running":
			return false, true
		}
	}
	return false, false
}
