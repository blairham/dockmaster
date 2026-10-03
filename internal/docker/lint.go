// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Severity orders lint findings: Risk is a hole in isolation, Warn is
// fragile, Info is worth knowing.
type Severity int

// Severities, least to most serious.
const (
	Info Severity = iota
	Warn
	Risk
)

func (s Severity) String() string {
	switch s {
	case Risk:
		return "risk"
	case Warn:
		return "warn"
	default:
		return "info"
	}
}

// Finding is one thing container lint (#10) has to say about a container.
type Finding struct {
	Rule     string // short, stable: "privileged", "no-healthcheck"
	Message  string // why it matters, one line
	Severity Severity
}

// LintResult is one container and what lint found in it, worst first.
type LintResult struct {
	Err       error
	Findings  []Finding
	Container Container
}

// Worst is the most serious finding's severity, and false when there are
// none.
func (r LintResult) Worst() (Severity, bool) {
	if len(r.Findings) == 0 {
		return Info, false
	}
	return r.Findings[0].Severity, true
}

// LintContainer checks one container's inspect data against the rules: the
// settings that weaken isolation or make a service fragile. It is pure, so
// every rule is tested on hand-built inspect data. node is true for a kind
// or k3d node, which needs privilege to run its own containers — said, but
// not raised as a risk.
func LintContainer(insp container.InspectResponse, node bool) []Finding {
	var out []Finding
	add := func(sev Severity, rule, msg string) {
		out = append(out, Finding{Rule: rule, Message: msg, Severity: sev})
	}
	cfg, hc := insp.Config, insp.HostConfig
	if cfg == nil {
		cfg = &container.Config{}
	}
	if hc == nil {
		hc = &container.HostConfig{}
	}

	if hc.Privileged {
		if node {
			add(Info, "privileged", "privileged — expected for a Kubernetes node, which runs its own containers")
		} else {
			add(Risk, "privileged", "privileged: every device and capability of the host, little left between it and the host")
		}
	}
	for _, m := range insp.Mounts {
		if strings.HasSuffix(m.Source, "/docker.sock") {
			add(
				Risk,
				"docker-socket",
				"mounts the docker socket "+m.Source+": control of every container on the host, and the host",
			)
			break
		}
	}
	if hc.PidMode.IsHost() && !node {
		add(Risk, "host-pid", "shares the host's process namespace: it sees and can signal the host's processes")
	}
	if hc.NetworkMode.IsHost() {
		add(Warn, "host-network", "host networking: every port it opens is open on the host, with no isolation")
	}
	if user := cfg.User; user == "" || user == "root" || user == "0" || strings.HasPrefix(user, "0:") ||
		strings.HasPrefix(user, "root:") {
		add(Warn, "root", "runs as root: a breakout lands as root on the host unless user namespaces remap it")
	}
	if check := cfg.Healthcheck; check == nil || len(check.Test) == 0 || check.Test[0] == "NONE" {
		add(
			Warn,
			"no-healthcheck",
			"no healthcheck: docker reports it running while it is hung, and depends_on cannot wait for it",
		)
	}
	if ref := cfg.Image; unpinnedTag(ref) {
		add(Warn, "latest", "image "+ref+" is not pinned: a pull can change what runs on the next restart")
	}
	if hc.Memory == 0 {
		add(Warn, "no-memory-limit", "no memory limit: a leak takes memory from everything else on the host")
	}
	if hc.NanoCPUs == 0 && hc.CPUQuota == 0 {
		add(Info, "no-cpu-limit", "no CPU limit: a busy loop competes with everything else on the host")
	}
	if p := hc.RestartPolicy.Name; (p == "" || p == container.RestartPolicyDisabled) && insp.State != nil &&
		insp.State.Running {
		add(Info, "no-restart", "no restart policy: if it crashes, or the daemon restarts, it stays down")
	}

	slices.SortStableFunc(out, func(a, b Finding) int { return int(b.Severity) - int(a.Severity) })
	return out
}

// unpinnedTag reports whether an image reference floats: tagged latest, or
// not tagged at all, which means latest. A digest pins it whatever the tag:
// "name@sha256:…" parses with the digest as its tag, never "latest".
func unpinnedTag(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, "sha256:") {
		return false
	}
	name := ref[strings.LastIndexByte(ref, '/')+1:]
	_, tag, ok := strings.Cut(name, ":")
	return !ok || tag == "latest"
}

// lintParallel bounds how many inspects a lint has outstanding: each is one
// daemon request, and a slow daemon should not see a hundred at once.
const lintParallel = 8

// Lint inspects every container — stopped ones too, since a setting is
// there before it is running — and checks each against the rules. One
// container failing to inspect is reported on its row, not for the lint.
func (c *Client) Lint(ctx context.Context) ([]LintResult, error) {
	all, err := c.Containers(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]LintResult, len(all))
	sem := make(chan struct{}, lintParallel)
	var wg sync.WaitGroup
	for i, ctr := range all {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := c.api.ContainerInspect(ctx, ctr.ID, client.ContainerInspectOptions{})
			if err != nil {
				out[i] = LintResult{Container: ctr, Err: fmt.Errorf("inspecting %s: %w", ctr.Name, err)}
				return
			}
			_, node := NodeRole(ctr)
			out[i] = LintResult{Container: ctr, Findings: LintContainer(res.Container, node)}
		})
	}
	wg.Wait()
	return out, nil
}
