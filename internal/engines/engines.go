// Package engines finds the container runtimes installed on this machine —
// Colima, Podman machines, Docker Desktop, Rancher Desktop, OrbStack — and
// drives each through its own CLI, behind one Provider interface so the
// runtimes view does not care which it is talking to.
//
// Like internal/docker and internal/colima, nothing here imports bubbletea.
package engines

import (
	"context"
	"errors"
)

// Machine is one VM or engine a provider manages.
type Machine struct {
	Provider string // the Provider's Name
	Name     string
	Status   string // as the provider says it: "Running", "Stopped", ...
	Arch     string
	Runtime  string // container runtime inside it, when the provider has a choice
	Context  string // docker context name, when it registers one
	Host     string // docker endpoint the machine serves; "" when unknown
	CPUs     int
	Memory   int64 // bytes
	Disk     int64 // bytes
	Running  bool
	// Rootful and UserNet are podman's per-machine modes: containers run as
	// root in the VM, and traffic is routed through a user-mode network
	// stack (for VPNs that break the default one).
	Rootful bool
	UserNet bool
	// K8s is the machine's built-in Kubernetes cluster: KubeOn, KubeOff,
	// or "" where the runtime has none or the machine cannot say.
	K8s string
	// Clusters are the kind and k3d clusters on the machine's daemon,
	// running or stopped. Filled in by the runtimes view, which can reach
	// the daemon.
	Clusters []Cluster
}

// Cluster is a kind or k3d cluster whose nodes are containers on a
// machine's daemon.
type Cluster struct {
	Tool     string // "kind" or "k3d"
	Name     string
	Registry string // its local registry container, "" when none
	Running  bool   // any of its nodes is running
}

// String is "kind k8s".
func (c Cluster) String() string { return c.Tool + " " + c.Name }

// Machine.K8s values.
const (
	KubeOn  = "on"
	KubeOff = "off"
)

// Config is the resource shape a machine is created or resized with. Zero
// fields keep the provider's default or the machine's current value.
type Config struct {
	Rootful   *bool
	UserNet   *bool
	Runtime   string
	MemoryGiB float64
	CPUs      int
	DiskGiB   int
}

// Caps is what a provider can do beyond start and stop.
type Caps struct {
	Runtimes []string
	Create   bool
	Edit     bool
	Delete   bool
	Shell    bool
	Toggles  bool
	Single   bool
}

// Provider drives one runtime's CLI.
//
//nolint:interfacebloat // one runtime's whole verb set; Caps says which apply
type Provider interface {
	Name() string
	Caps() Caps
	List(ctx context.Context) ([]Machine, error)
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string) error
	Restart(ctx context.Context, name string) error
	Delete(ctx context.Context, name string) error
	Create(ctx context.Context, name string, cfg Config) error
	// Edit applies cfg to a machine; running reports whether it is up, for
	// providers that have to stop it first.
	Edit(ctx context.Context, name string, cfg Config, running bool) error
	// ShellCommand is the argv for an interactive shell in the machine.
	ShellCommand(name string) []string
	// Inspect is the runtime's own description of the machine, JSON where
	// the runtime has it.
	Inspect(ctx context.Context, name string) ([]byte, error)
}

// ErrUnsupported is returned by a verb a provider does not offer.
var ErrUnsupported = errors.New("not supported by this runtime")
