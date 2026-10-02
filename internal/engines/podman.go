// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package engines

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PodmanName is the podman provider's name.
const PodmanName = "podman"

// Podman drives `podman machine`. A podman machine serves the Docker API on
// its own socket, so dockmaster can connect to it like any daemon.
type Podman struct{ Run Runner }

// Name implements Provider.
func (Podman) Name() string { return PodmanName }

// Caps implements Provider.
func (Podman) Caps() Caps {
	return Caps{Create: true, Edit: true, Delete: true, Shell: true, Toggles: true}
}

// podmanMachine is one entry of `podman machine list --format json`.
// Memory and DiskSize are byte counts, which podman 5 prints as strings and
// older releases as numbers; flexInt takes either.
type podmanMachine struct {
	Name     string  `json:"Name"`
	VMType   string  `json:"VMType"`
	Memory   flexInt `json:"Memory"`
	DiskSize flexInt `json:"DiskSize"`
	CPUs     int     `json:"CPUs"`
	Running  bool    `json:"Running"`
	Starting bool    `json:"Starting"`
}

type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("not a byte count: %s", b)
	}
	*f = flexInt(n)
	return nil
}

// List implements Provider.
func (p Podman) List(ctx context.Context) ([]Machine, error) {
	out, err := p.Run(ctx, "podman", "machine", "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	var ms []podmanMachine
	if err := json.Unmarshal(out, &ms); err != nil {
		return nil, fmt.Errorf("parsing podman machine list: %w", err)
	}
	machines := make([]Machine, 0, len(ms))
	for _, m := range ms {
		status := "Stopped"
		switch {
		case m.Running:
			status = "Running"
		case m.Starting:
			status = "Starting"
		}
		mc := Machine{
			Provider: PodmanName, Name: m.Name, Status: status, Running: m.Running,
			Arch: m.VMType, Runtime: "podman", CPUs: m.CPUs, Memory: int64(m.Memory), Disk: int64(m.DiskSize),
		}
		if info, ok := p.inspectInfo(ctx, m.Name); ok {
			mc.Rootful, mc.UserNet = info.Rootful, info.UserModeNetworking
			if m.Running && info.ConnectionInfo.PodmanSocket != nil && info.ConnectionInfo.PodmanSocket.Path != "" {
				mc.Host = "unix://" + info.ConnectionInfo.PodmanSocket.Path
			}
		}
		machines = append(machines, mc)
	}
	return machines, nil
}

// podmanInfo is the part of `podman machine inspect` dockmaster reads: the
// Docker-API socket a running machine serves, and its modes.
type podmanInfo struct {
	ConnectionInfo struct {
		PodmanSocket *struct{ Path string } `json:"PodmanSocket"`
	} `json:"ConnectionInfo"`
	Rootful            bool `json:"Rootful"`
	UserModeNetworking bool `json:"UserModeNetworking"`
}

func (p Podman) inspectInfo(ctx context.Context, name string) (podmanInfo, bool) {
	out, err := p.Run(ctx, "podman", "machine", "inspect", name)
	if err != nil {
		return podmanInfo{}, false
	}
	var info []podmanInfo
	if json.Unmarshal(out, &info) != nil || len(info) == 0 {
		return podmanInfo{}, false
	}
	return info[0], true
}

// Inspect implements Provider.
func (p Podman) Inspect(ctx context.Context, name string) ([]byte, error) {
	return p.Run(ctx, "podman", "machine", "inspect", name)
}

func (p Podman) machine(ctx context.Context, verb, name string, extra ...string) error {
	args := append([]string{"machine", verb}, extra...)
	_, err := p.Run(ctx, "podman", append(args, name)...)
	return err
}

// Start implements Provider.
func (p Podman) Start(ctx context.Context, name string) error { return p.machine(ctx, "start", name) }

// Stop implements Provider.
func (p Podman) Stop(ctx context.Context, name string) error { return p.machine(ctx, "stop", name) }

// Restart implements Provider: `podman machine restart` where podman has it
// (6.0+), stop and start where it does not.
func (p Podman) Restart(ctx context.Context, name string) error {
	err := p.machine(ctx, "restart", name)
	if err == nil ||
		!strings.Contains(err.Error(), "unrecognized command") && !strings.Contains(err.Error(), "unknown command") {
		return err
	}
	if err := p.Stop(ctx, name); err != nil {
		return err
	}
	return p.Start(ctx, name)
}

// Delete implements Provider. -f skips podman's own prompt; dockmaster asks.
func (p Podman) Delete(ctx context.Context, name string) error {
	return p.machine(ctx, "rm", name, "-f")
}

// podmanResourceArgs renders cfg as podman's flags: memory in MiB, disk in
// GiB — podman's units, unlike colima's GiB memory.
func podmanResourceArgs(cfg Config) []string {
	var args []string
	if cfg.CPUs > 0 {
		args = append(args, "--cpus", strconv.Itoa(cfg.CPUs))
	}
	if cfg.MemoryGiB > 0 {
		args = append(args, "--memory", strconv.Itoa(int(cfg.MemoryGiB*1024)))
	}
	if cfg.DiskGiB > 0 {
		args = append(args, "--disk-size", strconv.Itoa(cfg.DiskGiB))
	}
	if cfg.Rootful != nil {
		args = append(args, "--rootful="+strconv.FormatBool(*cfg.Rootful))
	}
	if cfg.UserNet != nil {
		args = append(args, "--user-mode-networking="+strconv.FormatBool(*cfg.UserNet))
	}
	return args
}

// Create implements Provider: init, then start — `--now` does both.
func (p Podman) Create(ctx context.Context, name string, cfg Config) error {
	return p.machine(ctx, "init", name, append(podmanResourceArgs(cfg), "--now")...)
}

// Edit implements Provider: `podman machine set` needs the machine stopped,
// so a running one is stopped, changed and started again.
//
// The disk size is passed only when it grows: podman rejects a --disk-size
// that is not larger than the current one ("new disk size must be larger"),
// even when it is the same, so sending the unchanged size from the form
// failed every resize that left the disk alone.
func (p Podman) Edit(ctx context.Context, name string, cfg Config, running bool) error {
	if cfg.DiskGiB > 0 {
		if ms, err := p.List(ctx); err == nil {
			for _, m := range ms {
				if m.Name == name && int64(cfg.DiskGiB) <= m.Disk>>30 {
					cfg.DiskGiB = 0
				}
			}
		}
	}
	if running {
		if err := p.Stop(ctx, name); err != nil {
			return err
		}
	}
	if err := p.machine(ctx, "set", name, podmanResourceArgs(cfg)...); err != nil {
		return err
	}
	return p.Start(ctx, name)
}

// ShellCommand implements Provider.
func (Podman) ShellCommand(name string) []string { return []string{"podman", "machine", "ssh", name} }
