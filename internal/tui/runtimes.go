package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockyard/internal/engines"
	"github.com/blairham/dockyard/internal/tui/style"
	"github.com/blairham/dockyard/internal/tui/views"
)

// runtimeTimeout bounds a runtime lifecycle call. A first start provisions
// a VM and can take minutes; abandoning one about to succeed is worse than
// waiting.
const runtimeTimeout = 10 * time.Minute

// runtimeDoneMsg reports the outcome of a runtime lifecycle call.
type runtimeDoneMsg struct {
	err      error
	provider string
	name     string
	verb     string // "started", "stopped", ...
}

func (a *App) runtimesView() *views.RuntimesView {
	return typedView[*views.RuntimesView](a, style.ViewRuntimes)
}

// runtimeTarget resolves a MachineKey to its provider and machine.
func (a *App) runtimeTarget(key string) (engines.Provider, engines.Machine, bool) {
	rv := a.runtimesView()
	if rv == nil {
		return nil, engines.Machine{}, false
	}
	provider, name := views.SplitMachineKey(key)
	p, ok := rv.Provider(provider)
	if !ok {
		return nil, engines.Machine{}, false
	}
	m, ok := rv.Machine(provider, name)
	if !ok {
		m = engines.Machine{Provider: provider, Name: name}
	}
	return p, m, true
}

// runtimeLabel names a machine in messages: "colima default", or just the
// app for a single-engine runtime.
func runtimeLabel(p engines.Provider, name string) string {
	if p.Caps().Single {
		return name
	}
	return p.Name() + " " + name
}

// runtimeRun marks a machine busy and runs a lifecycle verb in the
// background. busy is the present participle the row shows meanwhile.
func (a *App) runtimeRun(p engines.Provider, name, busy, verb string, fn func(context.Context) error) tea.Cmd {
	if rv := a.runtimesView(); rv != nil {
		rv.SetBusy(p.Name(), name, busy)
	}
	a.flash = fmt.Sprintf("%s %s — this can take a minute", busy, runtimeLabel(p, name))
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeTimeout)
		defer cancel()
		return runtimeDoneMsg{provider: p.Name(), name: name, verb: verb, err: fn(ctx)}
	}
}

// handleRuntimeDone clears the busy marker and reports. When the machine
// serves dockyard's daemon, a start or restart reconnects — the old client
// negotiated against a daemon that no longer exists.
func (a *App) handleRuntimeDone(msg runtimeDoneMsg) (tea.Model, tea.Cmd) {
	rv := a.runtimesView()
	var refresh tea.Cmd
	if rv != nil {
		rv.SetBusy(msg.provider, msg.name, "")
		refresh = rv.Refresh()
	}
	if msg.err != nil {
		a.errFlash = msg.err.Error()
		return a, refresh
	}
	label := msg.provider + " " + msg.name
	if p, ok := rv.Provider(msg.provider); ok {
		label = runtimeLabel(p, msg.name)
	}
	a.flash = msg.verb + " " + label

	m, ok := rv.Machine(msg.provider, msg.name)
	if !ok || !a.servesDockyard(m) {
		return a, refresh
	}
	switch msg.verb {
	case "started", "restarted", "updated":
		a.flash += ", reconnecting…"
		name := m.Context
		if name == "" {
			name = m.Name
		}
		return a, tea.Batch(refresh, doSwitchContextKeep(name, m.Host, true))
	case "stopped", "deleted":
		a.flash += " — dockyard has no daemon until a machine is started"
	}
	return a, refresh
}

// servesDockyard reports whether m is the machine dockyard's daemon runs on.
func (a *App) servesDockyard(m engines.Machine) bool {
	return m.Host != "" && a.client != nil && m.Host == a.client.Host
}

func unsupported(p engines.Provider, verb string) string {
	return fmt.Sprintf("%s cannot %s from here", p.Name(), verb)
}

func (a *App) runtimeStart(key string) tea.Cmd {
	p, m, ok := a.runtimeTarget(key)
	if !ok {
		return nil
	}
	return a.runtimeRun(p, m.Name, "starting", "started", func(ctx context.Context) error { return p.Start(ctx, m.Name) })
}

// runtimeQuestion is the confirm prompt for a destructive verb. It says so
// when the machine is dockyard's own daemon, since then every other view
// goes dark too.
func (a *App) runtimeQuestion(verb, key string) (string, bool) {
	p, m, ok := a.runtimeTarget(key)
	if !ok {
		return "", false
	}
	if verb == "delete" && !p.Caps().Delete {
		a.errFlash = unsupported(p, "delete machines")
		return "", false
	}
	label := runtimeLabel(p, m.Name)
	var q string
	switch verb {
	case "stop":
		q = fmt.Sprintf("stop %s? every container in it stops", label)
	case "restart":
		q = fmt.Sprintf("restart %s? every container in it restarts", label)
	case "delete":
		q = fmt.Sprintf("delete %s? the VM and everything in it is destroyed", label)
	}
	if a.servesDockyard(m) {
		q += " — dockyard is connected to it"
	}
	return q, true
}

func (a *App) runtimeConfirmed(verb, key string) tea.Cmd {
	p, m, ok := a.runtimeTarget(key)
	if !ok {
		return nil
	}
	switch verb {
	case "stop":
		return a.runtimeRun(p, m.Name, "stopping", "stopped", func(ctx context.Context) error { return p.Stop(ctx, m.Name) })
	case "restart":
		return a.runtimeRun(
			p,
			m.Name,
			"restarting",
			"restarted",
			func(ctx context.Context) error { return p.Restart(ctx, m.Name) },
		)
	case "delete":
		return a.runtimeRun(
			p,
			m.Name,
			"deleting",
			"deleted",
			func(ctx context.Context) error { return p.Delete(ctx, m.Name) },
		)
	}
	return nil
}

// runtimeConnect points dockyard at a machine's daemon.
func (a *App) runtimeConnect(key string) tea.Cmd {
	_, m, ok := a.runtimeTarget(key)
	if !ok {
		return nil
	}
	switch {
	case !m.Running:
		a.errFlash = m.Name + " is stopped — <u> starts it"
		return nil
	case m.Host == "":
		a.errFlash = "no docker endpoint known for " + m.Name
		return nil
	case a.servesDockyard(m):
		a.flash = "already connected to " + m.Name
		return nil
	}
	name := m.Context
	if name == "" {
		name = m.Name
	}
	a.flash = "connecting to " + name + "..."
	return doSwitchContext(name, m.Host)
}

// runtimeShell opens a shell in a machine's VM, handing the terminal to the
// runtime's CLI as a container shell hands it to docker's.
func (a *App) runtimeShell(key string) tea.Cmd {
	p, m, ok := a.runtimeTarget(key)
	if !ok {
		return nil
	}
	argv := p.ShellCommand(m.Name)
	switch {
	case !p.Caps().Shell || len(argv) == 0:
		a.errFlash = unsupported(p, "open a shell")
		return nil
	case !m.Running:
		a.errFlash = m.Name + " is stopped — <u> starts it"
		return nil
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		a.errFlash = argv[0] + " is not on PATH"
		return nil
	}
	// context.Background(): the session belongs to the user, not a timeout.
	cmd := exec.CommandContext(
		context.Background(),
		bin,
		argv[1:]...,
	) //nolint:gosec // runtime CLI; machine name from its own listing
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			return execDoneMsg{err: fmt.Errorf("%s shell: %w", p.Name(), err)}
		}
		return execDoneMsg{}
	})
}

// runtimeNew opens the create form, for the selected machine's runtime or
// else the first that can create machines.
func (a *App) runtimeNew(provider string) tea.Cmd {
	rv := a.runtimesView()
	if rv == nil {
		return nil
	}
	var creatable []engines.Provider
	for _, p := range rv.Providers() {
		if p.Caps().Create {
			creatable = append(creatable, p)
		}
	}
	if len(creatable) == 0 {
		a.errFlash = "no runtime here creates machines — " + rv.Providers()[0].Name() + " manages a single engine"
		return nil
	}
	return a.openRuntimeForm(views.NewRuntimeCreateForm(provider, creatable, rv.Machines()))
}

func (a *App) runtimeEdit(key string) tea.Cmd {
	p, m, ok := a.runtimeTarget(key)
	if !ok {
		return nil
	}
	if !p.Caps().Edit {
		a.errFlash = unsupported(p, "change resources")
		return nil
	}
	return a.openRuntimeForm(views.NewRuntimeEditForm(m, p.Caps()))
}

func (a *App) openRuntimeForm(f *views.RuntimeFormView) tea.Cmd {
	a.setView(style.ViewRuntimeForm, f)
	a.pushView(style.ViewRuntimeForm)
	return f.Init()
}

// runtimeCreate leaves the form and creates the machine.
func (a *App) runtimeCreate(param string) tea.Cmd {
	spec, err := views.DecodeRuntimeSpec(param)
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	a.popView()
	p, ok := a.runtimesView().Provider(spec.Provider)
	if !ok {
		return nil
	}
	return a.runtimeRun(p, spec.Name, "creating", "created", func(ctx context.Context) error {
		return p.Create(ctx, spec.Name, spec.Config)
	})
}

// runtimeApply leaves the form and applies new resources. Runtimes read
// resources only at start, so a running machine restarts — which takes
// every container in it down, so that path confirms.
func (a *App) runtimeApply(param string) tea.Cmd {
	spec, err := views.DecodeRuntimeSpec(param)
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	a.popView()
	name := spec.Name
	p, m, ok := a.runtimeTarget(views.MachineKey(spec.Provider, name))
	if !ok {
		return nil
	}
	if m.Running {
		q := fmt.Sprintf("apply to %s? it restarts — every container in it stops", runtimeLabel(p, name))
		if a.servesDockyard(m) {
			q += " — dockyard is connected to it"
		}
		a.openConfirm("runtime_apply", param, q)
		return nil
	}
	return a.runtimeApplyNow(param)
}

func (a *App) runtimeApplyNow(param string) tea.Cmd {
	spec, err := views.DecodeRuntimeSpec(param)
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	p, m, ok := a.runtimeTarget(views.MachineKey(spec.Provider, spec.Name))
	if !ok {
		return nil
	}
	return a.runtimeRun(p, spec.Name, "applying", "updated", func(ctx context.Context) error {
		return p.Edit(ctx, spec.Name, spec.Config, m.Running)
	})
}

// runtimeInspect shows the runtime's own description of a machine.
func (a *App) runtimeInspect(key string) tea.Cmd {
	p, m, ok := a.runtimeTarget(key)
	if !ok {
		return nil
	}
	name := m.Name
	v := views.NewInspectFetchView(runtimeLabel(p, name), func(ctx context.Context) ([]byte, error) {
		return p.Inspect(ctx, name)
	})
	a.setView(style.ViewInspect, v)
	a.pushView(style.ViewInspect)
	return v.Init()
}
