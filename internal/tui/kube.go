package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/kind"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// K in the runtimes view manages a kind cluster on the machine's daemon —
// the setup a kind user already has: a control-plane and a worker, and a
// registry on localhost:5001 wired into the nodes. It starts or stops the
// cluster that is there, or creates one where there is none. It does not
// touch a runtime's own built-in Kubernetes: starting colima's k3s on a VM
// that already ran a kind cluster added a second one and took kubectl's
// context, which is what this replaced.

// kubeOp is what K will do, decided when the confirm opens.
type kubeOp struct {
	verb    string // "start", "stop" or "create"
	cluster string // the cluster's name
}

func (o kubeOp) encode() string { return o.verb + "\x00" + o.cluster }

// decodeKubeOp splits "verb\x00cluster\x00<machine key>"; the machine key
// holds a \x00 itself, so it goes last.
func decodeKubeOp(p string) (kubeOp, string) {
	verb, rest, _ := strings.Cut(p, "\x00")
	cluster, key, _ := strings.Cut(rest, "\x00")
	return kubeOp{verb: verb, cluster: cluster}, key
}

// clusterOps is the daemon side of K — what docker.Client does — so tests
// drive it without a daemon.
type clusterOps interface {
	Clusters(ctx context.Context) ([]docker.Cluster, error)
	SetClusterRunning(ctx context.Context, cl docker.Cluster, on bool) error
	SetRegistryRunning(ctx context.Context, name string, on bool) error
	EnsureRegistry(ctx context.Context, r docker.Registry) error
	WireRegistry(ctx context.Context, cl docker.Cluster, r docker.Registry) error
	ConnectRegistry(ctx context.Context, r docker.Registry, network string) error
	Close() error
}

// dialCluster and kindRun reach the machine's daemon and the kind CLI;
// vars so tests answer for both.
var (
	dialCluster = func(host string) (clusterOps, error) { return docker.New(host) }
	kindRun     = kind.ExecRunner
	kubeconfigs = kind.Contexts
)

// kubeQuestion decides what K does on a machine and asks, or says why not.
func (a *App) kubeQuestion(key string) (string, kubeOp, bool) {
	p, m, found := a.runtimeTarget(key)
	if !found {
		return "", kubeOp{}, false
	}
	label := runtimeLabel(p, m.Name)
	switch {
	case !m.Running:
		a.errFlash = m.Name + " is stopped — <u> starts it, then K manages its kind cluster"
		return "", kubeOp{}, false
	case m.Host == "":
		a.errFlash = "no docker endpoint known for " + m.Name
		return "", kubeOp{}, false
	}
	if len(m.Clusters) > 0 {
		c := m.Clusters[0]
		if c.Running {
			return fmt.Sprintf("stop %s in %s? its pods stop%s", c, label, registryFate(c, m.Clusters)),
				kubeOp{verb: "stop", cluster: c.Name}, true
		}
		with := ""
		if c.Registry != "" {
			with = ", with " + c.Registry
		}
		return fmt.Sprintf("start %s in %s%s?", c, label, with), kubeOp{verb: "start", cluster: c.Name}, true
	}
	if m.K8s == engines.KubeOn {
		a.errFlash = fmt.Sprintf("%s's own Kubernetes is on in %s — K manages kind clusters; turn that one off "+
			"first (for colima: colima kubernetes delete)", p.Name(), label)
		return "", kubeOp{}, false
	}
	contexts, err := kubeconfigs()
	if err != nil {
		a.errFlash = err.Error()
		return "", kubeOp{}, false
	}
	name := kind.Name(contexts, m.Name)
	return fmt.Sprintf("create kind cluster %s in %s? control-plane + worker, registry on localhost:%s; "+
			"kubectl switches to kind-%s (a few minutes the first time)", name, label, docker.DefaultRegistry.Port, name),
		kubeOp{verb: "create", cluster: name}, true
}

// runtimeKube carries out K in the background, the row showing it.
func (a *App) runtimeKube(key string, op kubeOp) tea.Cmd {
	p, m, ok := a.runtimeTarget(key)
	if !ok {
		return nil
	}
	busy, done := op.verb+"ing kind in", op.verb+"ed kind cluster "+op.cluster+" in"
	switch op.verb {
	case "stop":
		busy = "stopping kind in"
	case "create":
		busy, done = "creating kind in", "created kind cluster "+op.cluster+" in"
	}
	host := m.Host
	return a.runtimeRun(p, m.Name, busy, done, func(ctx context.Context) error {
		return kubeDo(ctx, host, op)
	})
}

// kubeDo is the work: start or stop a cluster's nodes, or create the
// cluster and its registry.
func kubeDo(ctx context.Context, host string, op kubeOp) error {
	c, err := dialCluster(host)
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // per-operation client
	find := func() (docker.Cluster, error) {
		cs, err := c.Clusters(ctx)
		if err != nil {
			return docker.Cluster{}, err
		}
		for _, cl := range cs {
			if cl.Name == op.cluster {
				return cl, nil
			}
		}
		return docker.Cluster{}, fmt.Errorf("no cluster %s on %s", op.cluster, host)
	}
	switch op.verb {
	case "start":
		cl, err := find()
		if err != nil {
			return err
		}
		// The registry first: a node coming up may pull from it.
		if cl.Registry != "" && !cl.RegistryRunning {
			if err := c.SetRegistryRunning(ctx, cl.Registry, true); err != nil {
				return err
			}
		}
		return c.SetClusterRunning(ctx, cl, true)
	case "stop":
		cl, err := find()
		if err != nil {
			return err
		}
		if serr := c.SetClusterRunning(ctx, cl, false); serr != nil {
			return serr
		}
		if cl.Registry == "" || !cl.RegistryRunning {
			return nil
		}
		// Kind clusters on a daemon share the registry: it stops with the
		// last of them.
		all, err := c.Clusters(ctx)
		if err != nil {
			return err
		}
		for _, other := range all {
			if other.Tool == "kind" && other.Name != cl.Name && other.Running {
				return nil
			}
		}
		return c.SetRegistryRunning(ctx, cl.Registry, false)
	case "create":
		reg := docker.DefaultRegistry
		if err := c.EnsureRegistry(ctx, reg); err != nil {
			return err
		}
		if err := kind.Create(ctx, kindRun, host, op.cluster); err != nil {
			return err
		}
		cl, err := find()
		if err != nil {
			return err
		}
		if err := c.WireRegistry(ctx, cl, reg); err != nil {
			return err
		}
		return c.ConnectRegistry(ctx, reg, "kind")
	}
	return fmt.Errorf("unknown kubernetes operation %q", op.verb)
}

// registryFate says what stopping c does to its registry: it goes with the
// last running kind cluster that uses it, and stays for another.
func registryFate(c engines.Cluster, all []engines.Cluster) string {
	if c.Registry == "" {
		return ""
	}
	for _, o := range all {
		if o.Tool == "kind" && o.Name != c.Name && o.Running {
			return fmt.Sprintf("; %s stays — kind %s uses it", c.Registry, o.Name)
		}
	}
	return ", and " + c.Registry + " with it"
}

// registryLine describes the local registry of the kind cluster node is
// in, for the node view: where to push, and whether it is up. "" for a
// k3d node or a cluster without one.
func (a *App) registryLine(node string) string {
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	if cv == nil {
		return ""
	}
	n, ok := cv.ByID(node)
	if role, isNode := docker.NodeRole(n); !ok || !isNode || n.Labels["io.x-k8s.kind.role"] != role {
		return ""
	}
	for _, c := range cv.All() {
		if !docker.IsClusterRegistry(c) {
			continue
		}
		state := style.Success.Render("running")
		if !c.Running() {
			state = style.Muted.Render("stopped")
		}
		push := ""
		for _, p := range c.PortList {
			if p.Private == 5000 && p.Public != 0 {
				push = fmt.Sprintf(" · push to localhost:%d", p.Public)
			}
		}
		return style.Muted.Render(" registry ") + c.Name + style.Muted.Render(push+" · ") + state
	}
	return ""
}
