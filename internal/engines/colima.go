package engines

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/blairham/dockyard/internal/colima"
)

// ColimaName is the colima provider's name.
const ColimaName = "colima"

// Colima adapts internal/colima to Provider.
type Colima struct{ C *colima.Client }

// Name implements Provider.
func (Colima) Name() string { return ColimaName }

// Caps implements Provider: colima does everything.
func (Colima) Caps() Caps {
	return Caps{Create: true, Edit: true, Delete: true, Shell: true, Runtimes: colima.Runtimes}
}

// List implements Provider.
func (p Colima) List(ctx context.Context) ([]Machine, error) {
	profiles, err := p.C.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Machine, 0, len(profiles))
	for _, pr := range profiles {
		out = append(out, Machine{
			Provider: ColimaName,
			Name:     pr.Name,
			Status:   pr.Status,
			Running:  pr.Running(),
			Arch:     pr.Arch,
			Runtime:  pr.Runtime,
			CPUs:     pr.CPUs,
			Memory:   pr.Memory,
			Disk:     pr.Disk,
			Context:  colima.ContextName(pr.Name),
			Host:     p.C.DockerHost(pr.Name),
		})
	}
	return out, nil
}

// Start implements Provider.
func (p Colima) Start(ctx context.Context, name string) error { return p.C.Start(ctx, name) }

// Stop implements Provider.
func (p Colima) Stop(ctx context.Context, name string) error { return p.C.Stop(ctx, name) }

// Restart implements Provider.
func (p Colima) Restart(ctx context.Context, name string) error { return p.C.Restart(ctx, name) }

// Delete implements Provider.
func (p Colima) Delete(ctx context.Context, name string) error { return p.C.Delete(ctx, name) }

// Create implements Provider: colima start on a new name provisions it.
func (p Colima) Create(ctx context.Context, name string, cfg Config) error {
	return p.C.StartWith(ctx, name, colimaConfig(cfg))
}

// Edit implements Provider: colima reads resources only at start, so a
// running profile is stopped first.
func (p Colima) Edit(ctx context.Context, name string, cfg Config, running bool) error {
	if running {
		if err := p.C.Stop(ctx, name); err != nil {
			return err
		}
	}
	return p.C.StartWith(ctx, name, colimaConfig(cfg))
}

// Inspect implements Provider: `colima status --json` for a running profile
// (it carries the sockets and mounts), the list entry for a stopped one.
func (p Colima) Inspect(ctx context.Context, name string) ([]byte, error) {
	if out, err := p.C.Status(ctx, name); err == nil {
		return out, nil
	}
	profiles, err := p.C.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, pr := range profiles {
		if pr.Name == name {
			return json.Marshal(pr)
		}
	}
	return nil, fmt.Errorf("no colima profile %s", name)
}

// ShellCommand implements Provider.
func (Colima) ShellCommand(name string) []string {
	return append([]string{"colima"}, colima.SSHArgs(name)...)
}

func colimaConfig(c Config) colima.Config {
	return colima.Config{CPUs: c.CPUs, MemoryGiB: c.MemoryGiB, DiskGiB: c.DiskGiB, Runtime: c.Runtime}
}
