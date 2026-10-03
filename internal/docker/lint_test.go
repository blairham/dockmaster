// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
)

// tidy is a container that passes every rule; each case breaks one thing.
func tidy() container.InspectResponse {
	return container.InspectResponse{
		Config: &container.Config{
			User:        "1000",
			Image:       "nginx:1.27",
			Healthcheck: &container.HealthConfig{Test: []string{"CMD", "true"}},
		},
		HostConfig: &container.HostConfig{
			Resources:     container.Resources{Memory: 256 << 20, NanoCPUs: 1e9},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			NetworkMode:   "bridge",
		},
		State: &container.State{Running: true},
	}
}

func rules(fs []Finding) string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Severity.String()+":"+f.Rule)
	}
	return strings.Join(out, ",")
}

// TestLintRules: each rule fires on the one setting it is about and nothing
// else does, and findings come worst first (#10).
func TestLintRules(t *testing.T) {
	if got := rules(LintContainer(tidy(), false)); got != "" {
		t.Fatalf("a tidy container has findings: %s", got)
	}
	for _, c := range []struct {
		change func(*container.InspectResponse)
		name   string
		want   string
		node   bool
	}{
		{name: "privileged", change: func(i *container.InspectResponse) { i.HostConfig.Privileged = true }, want: "risk:privileged"},
		{name: "privileged node", node: true, change: func(i *container.InspectResponse) { i.HostConfig.Privileged = true }, want: "info:privileged"},
		{name: "docker socket", change: func(i *container.InspectResponse) {
			i.Mounts = []container.MountPoint{{Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"}}
		}, want: "risk:docker-socket"},
		{name: "host pid", change: func(i *container.InspectResponse) { i.HostConfig.PidMode = "host" }, want: "risk:host-pid"},
		{name: "host network", change: func(i *container.InspectResponse) { i.HostConfig.NetworkMode = "host" }, want: "warn:host-network"},
		{name: "root by default", change: func(i *container.InspectResponse) { i.Config.User = "" }, want: "warn:root"},
		{name: "root by uid", change: func(i *container.InspectResponse) { i.Config.User = "0:0" }, want: "warn:root"},
		{name: "no healthcheck", change: func(i *container.InspectResponse) { i.Config.Healthcheck = nil }, want: "warn:no-healthcheck"},
		{name: "healthcheck off", change: func(i *container.InspectResponse) {
			i.Config.Healthcheck = &container.HealthConfig{Test: []string{"NONE"}}
		}, want: "warn:no-healthcheck"},
		{name: "latest", change: func(i *container.InspectResponse) { i.Config.Image = "nginx:latest" }, want: "warn:latest"},
		{name: "untagged", change: func(i *container.InspectResponse) { i.Config.Image = "localhost:5001/app" }, want: "warn:latest"},
		{name: "no memory limit", change: func(i *container.InspectResponse) { i.HostConfig.Memory = 0 }, want: "warn:no-memory-limit"},
		{name: "no cpu limit", change: func(i *container.InspectResponse) { i.HostConfig.NanoCPUs = 0 }, want: "info:no-cpu-limit"},
		{name: "cpu quota counts", change: func(i *container.InspectResponse) {
			i.HostConfig.NanoCPUs, i.HostConfig.CPUQuota = 0, 50000
		}, want: ""},
		{name: "no restart while running", change: func(i *container.InspectResponse) {
			i.HostConfig.RestartPolicy.Name = container.RestartPolicyDisabled
		}, want: "info:no-restart"},
		{name: "no restart, stopped", change: func(i *container.InspectResponse) {
			i.HostConfig.RestartPolicy.Name, i.State.Running = "", false
		}, want: ""},
	} {
		in := tidy()
		c.change(&in)
		if got := rules(LintContainer(in, c.node)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}

	worst := tidy()
	worst.Config.User, worst.HostConfig.Privileged, worst.HostConfig.NanoCPUs = "", true, 0
	fs := LintContainer(worst, false)
	if got := rules(fs); got != "risk:privileged,warn:root,info:no-cpu-limit" {
		t.Errorf("not worst first: %s", got)
	}
	if !slices.IsSortedFunc(fs, func(a, b Finding) int { return int(b.Severity) - int(a.Severity) }) {
		t.Error("findings not sorted by severity")
	}
}

func TestUnpinnedTag(t *testing.T) {
	for ref, want := range map[string]bool{
		"nginx": true, "nginx:latest": true, "localhost:5001/app": true, "localhost:5001/app:latest": true,
		"nginx:1.27": false, "nginx@sha256:abc": false, "nginx:latest@sha256:abc": false, "sha256:abc": false, "": false,
	} {
		if got := unpinnedTag(ref); got != want {
			t.Errorf("unpinnedTag(%q) = %v, want %v", ref, got, want)
		}
	}
}
