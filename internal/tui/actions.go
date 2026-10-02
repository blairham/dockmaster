package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// actionTimeout bounds every mutating call. A container that refuses to
// stop still has to give the UI back.
const actionTimeout = 60 * time.Second

// actionDoneMsg reports the outcome of a mutation.
type actionDoneMsg struct {
	err     error
	verb    string // "started", "stopped", ... — used in the flash
	subject string
	// refresh names the view to refetch. Zero value means the active one.
	refresh style.ViewType
	// hasRefresh distinguishes "refresh the containers view" (ViewContainers
	// is the zero value) from "refresh whatever is active".
	hasRefresh bool
}

// execDoneMsg reports that an interactive exec session ended.
type execDoneMsg struct{ err error }

// mutating is the set of actions that change daemon state. --readonly
// refuses exactly these, in one place, rather than per view.
var mutating = map[string]bool{
	"start": true, "stop": true, "restart": true, "kill": true,
	"pause": true, "unpause": true, "exec": true,
	"confirm_remove_container": true, "confirm_remove_image": true,
	"confirm_remove_volume": true, "confirm_remove_network": true,
	"confirm_prune_images": true, "confirm_prune_volumes": true,
	"confirm_prune_networks": true, "confirm_prune_containers": true,
	"start_project": true, "confirm_stop_project": true,
	"confirm_remove_project": true, "pull": true,
	"runtime_start": true, "runtime_shell": true,
	"confirm_runtime_stop": true, "confirm_runtime_restart": true, "confirm_runtime_delete": true,
	"runtime_new": true, "runtime_edit": true, "runtime_create": true, "runtime_apply": true,
	"compose_up": true, "compose_restart": true, "compose_pull": true, "confirm_compose_down": true,
	"confirm_prune_all": true, "confirm_prune_all_volumes": true, "confirm_prune_cache": true,
	"portforward": true, "confirm_stop_forward": true,
	"pod_start": true, "pod_stop": true, "pod_restart": true, "confirm_pod_rm": true,
	"node_shell": true, "confirm_node_remove": true,
	"run_image": true, "run_create": true,
}

// handleAction turns a view's (action, param) request into state changes
// and commands. Every mutation in dockmaster funnels through here.
//
//nolint:gocyclo,gocognit,funlen // flat action dispatch; splitting it hides the readonly gate
func (a *App) handleAction(action, param string) (tea.Model, tea.Cmd) {
	if action == "" {
		return a, nil
	}
	if a.readonly && mutating[action] {
		a.errFlash = "readonly mode — " + strings.ReplaceAll(strings.TrimPrefix(action, "confirm_"), "_", " ") + " refused"
		return a, nil
	}

	switch action {
	// ---- navigation / drill-ins ----------------------------------------

	case "logs":
		name := a.containerName(param)
		a.setView(style.ViewLogs, views.NewLogsView(a.client, param, name).Configure(a.logTail, a.logShowTime))
		a.pushView(style.ViewLogs)
		return a, a.viewMap[style.ViewLogs].Init()

	case "inspect_container":
		return a.openInspect(views.InspectContainer, param, a.containerName(param))
	case "inspect_image":
		return a.openInspect(views.InspectImage, param, a.imageRef(param))
	case "inspect_volume":
		return a.openInspect(views.InspectVolume, param, param)
	case "inspect_network":
		return a.openInspect(views.InspectNetwork, param, a.networkName(param))

	case "layers":
		a.setView(style.ViewLayers, views.NewLayersView(a.client, param, a.imageRef(param)))
		a.pushView(style.ViewLayers)
		return a, a.viewMap[style.ViewLayers].Init()

	case "contexts":
		ctxName := ""
		if a.client != nil {
			ctxName = a.client.ContextName
		}
		a.setView(style.ViewContexts, views.NewContextsView(ctxName))
		a.pushView(style.ViewContexts)
		return a, a.viewMap[style.ViewContexts].Init()

	case "project_containers":
		// Drilling into a compose project is a filtered containers view —
		// projects exist only as a label, so there is nothing else to show.
		cmd := a.switchView(style.ViewContainers)
		if v := typedView[*views.ContainersView](a, style.ViewContainers); v != nil {
			if !v.ShowAll() {
				cmd = tea.Batch(cmd, v.ToggleAll())
			}
		}
		a.filter = param
		a.setActiveFilter(param)
		a.flash = "filtered to project " + param
		return a, cmd

	case "switch_context":
		name, host, _ := strings.Cut(param, "\x00")
		a.flash = "connecting to " + name + "..."
		return a, doSwitchContext(name, host)

	// ---- view toggles ---------------------------------------------------

	case "toggle_all":
		switch v := a.activeView().(type) {
		case *views.ContainersView:
			a.showAll = !v.ShowAll()
			return a, v.ToggleAll()
		case *views.ImagesView:
			return a, v.ToggleAll()
		case *views.NodeView:
			v.ToggleAll()
		}
		return a, nil

	case "toggle_stats":
		if v := typedView[*views.ContainersView](a, style.ViewContainers); v != nil {
			v.ToggleStats()
			a.statsOn = v.StatsEnabled()
			a.flash = "cpu/mem poll " + onOff(a.statsOn)
		}
		return a, nil

	case "toggle_size":
		if v := typedView[*views.VolumesView](a, style.ViewVolumes); v != nil {
			a.flash = "volume sizes " + onOff(!v.SizeEnabled()) + " (this walks each volume — it is slow)"
			return a, v.ToggleSize()
		}
		return a, nil

	case "toggle_follow":
		if v := typedView[*views.LogsView](a, style.ViewLogs); v != nil {
			v.ToggleFollow()
		}
		return a, nil

	case "toggle_timestamps":
		if v := typedView[*views.LogsView](a, style.ViewLogs); v != nil {
			return a, v.ToggleTimestamps()
		}
		return a, nil
	case "log_range", "log_wrap", "log_clear", "log_copy", "log_save":
		return a, a.logAction(action, param)
	case "fullscreen":
		a.setFullscreen(!a.fullscreen)
		return a, nil

	// ---- container lifecycle -------------------------------------------

	case "start":
		return a, a.run("started", a.containerName(param), func(ctx context.Context) error {
			return a.client.StartContainer(ctx, param)
		})
	case "stop":
		return a, a.run("stopped", a.containerName(param), func(ctx context.Context) error {
			return a.client.StopContainer(ctx, param, stopTimeout)
		})
	case "restart":
		return a, a.run("restarted", a.containerName(param), func(ctx context.Context) error {
			return a.client.RestartContainer(ctx, param, stopTimeout)
		})
	case "kill":
		a.openConfirm("kill", param,
			fmt.Sprintf("SIGKILL %s?", a.containerName(param)))
		return a, nil
	case "pause":
		return a, a.run("paused", a.containerName(param), func(ctx context.Context) error {
			return a.client.PauseContainer(ctx, param)
		})
	case "unpause":
		return a, a.run("unpaused", a.containerName(param), func(ctx context.Context) error {
			return a.client.UnpauseContainer(ctx, param)
		})

	case "exec":
		return a, a.execShell(param)

	// ---- Kubernetes nodes: the containers inside a kind/k3d node -------

	case "run_image":
		return a.openRunForm(param)
	case "run_create":
		spec, err := views.DecodeRunSpec(param)
		if err != nil {
			a.errFlash = err.Error()
			return a, nil
		}
		return a, a.runImage(spec)

	case "health":
		return a.openReport(views.NewHealthView(a.client, param, a.containerName(param)))
	case "diff":
		return a.openReport(views.NewDiffView(a.client, param, a.containerName(param)))
	case "stats":
		return a.openReport(views.NewStatsView(a.client, param, a.containerName(param)))
	case "top":
		a.setView(style.ViewTop, views.NewTopView(a.client, param, a.containerName(param)))
		a.pushView(style.ViewTop)
		return a, a.viewMap[style.ViewTop].Init()

	case "not_a_node":
		a.errFlash = param + " is not a kind/k3d node — c opens the containers inside a ⎈ node"
		return a, nil
	case "node_containers":
		node, _, name := splitNodeParam(param)
		a.setView(style.ViewNode, views.NewNodeView(a.client, node, name))
		a.pushView(style.ViewNode)
		return a, a.viewMap[style.ViewNode].Init()
	case "node_logs":
		node, id, name := splitNodeParam(param)
		a.setView(style.ViewLogs, views.NewNodeLogsView(a.client, node, id, name).Configure(a.logTail, a.logShowTime))
		a.pushView(style.ViewLogs)
		return a, a.viewMap[style.ViewLogs].Init()
	case "node_inspect":
		node, id, name := splitNodeParam(param)
		client := a.client
		a.setView(style.ViewInspect, views.NewInspectFetchView(name, func(ctx context.Context) ([]byte, error) {
			return client.NodeInspect(ctx, node, id)
		}))
		a.pushView(style.ViewInspect)
		return a, a.viewMap[style.ViewInspect].Init()
	case "node_shell":
		node, id, _ := splitNodeParam(param)
		return a, a.nodeShell(node, id)
	case "confirm_node_remove":
		_, _, name := splitNodeParam(param)
		a.openConfirm("node_remove", param, fmt.Sprintf("remove exited container %s?", name))
		return a, nil
	case "node_remove_running":
		_, _, name := splitNodeParam(param)
		a.errFlash = name + " is running — the kubelet owns it and would restart it; delete the pod instead"
		return a, nil

	// ---- destructive: ask first ----------------------------------------

	case "confirm_remove_container":
		a.openConfirm("remove_container", param,
			fmt.Sprintf("remove container %s?", a.containerName(param)))
		return a, nil
	case "confirm_remove_image":
		a.openConfirm("remove_image", param, fmt.Sprintf("remove image %s?", param))
		return a, nil
	case "confirm_remove_volume":
		a.openConfirm("remove_volume", param,
			fmt.Sprintf("remove volume %s? data in it is gone for good", param))
		return a, nil
	case "confirm_remove_network":
		_, name, _ := strings.Cut(param, "\x00")
		a.openConfirm("remove_network", param, fmt.Sprintf("remove network %s?", name))
		return a, nil
	case "confirm_prune_images":
		a.openConfirm("prune_images", "", "prune all dangling images?")
		return a, nil
	case "confirm_prune_volumes":
		a.openConfirm("prune_volumes", "",
			"prune every unused volume? this deletes their data")
		return a, nil
	case "confirm_prune_networks":
		a.openConfirm("prune_networks", "", "prune all unused networks?")
		return a, nil
	case "pod_inspect":
		return a, a.podInspect(param)
	case "pod_start":
		return a, a.podRun(param, "start", "started")
	case "pod_stop":
		return a, a.podRun(param, "stop", "stopped")
	case "pod_restart":
		return a, a.podRun(param, "restart", "restarted")
	case "confirm_pod_rm":
		_, _, name := views.SplitPodKey(param)
		a.openConfirm("pod_rm", param, fmt.Sprintf("remove pod %s and every container in it?", name))
		return a, nil
	case "portforward":
		return a, a.promptForward(param)
	case "open_published":
		return a, a.openPublished(param)
	case "open_url":
		return a, a.openURL(param)
	case "confirm_stop_forward":
		_, label, _ := strings.Cut(param, "\x00")
		a.openConfirm("stop_forward", param, "stop forwarding "+label+"?")
		return a, nil
	case "confirm_prune_all":
		a.openConfirm("prune_all", "",
			"prune all? stopped containers, unused networks, dangling images and build cache — volumes are kept")
		return a, nil
	case "confirm_prune_all_volumes":
		a.openConfirm("prune_all_volumes", "",
			"prune all INCLUDING VOLUMES? stopped containers, unused networks, dangling images, build cache, "+
				"and every unused volume, named ones too — their data is gone")
		return a, nil
	case "confirm_prune_cache":
		a.openConfirm("prune_cache", "", "prune the build cache? cache no image still uses is removed")
		return a, nil
	case "confirm_prune_containers":
		a.openConfirm("prune_containers", "", "remove every stopped container?")
		return a, nil

	case "refuse_builtin_network":
		a.errFlash = param + " is a built-in network — docker will not remove it"
		return a, nil

	// ---- compose projects ----------------------------------------------

	case "project_busy":
		name, op, _ := strings.Cut(param, "\x00")
		a.errFlash = fmt.Sprintf("%s is already %s — wait for it to finish", name, op)
		return a, nil
	case "compose_up":
		return a, a.composeUp(param)
	case "compose_restart":
		return a, a.composeRestart(param)
	case "compose_pull":
		return a, a.composePull(param)
	case "confirm_compose_down":
		a.confirmComposeDown(param)
		return a, nil
	case "start_project":
		return a, a.runProject(param, "started", func(ctx context.Context, id string) error {
			return a.client.StartContainer(ctx, id)
		})
	case "confirm_stop_project":
		a.openConfirm("stop_project", param, fmt.Sprintf("stop every container in %s?", param))
		return a, nil
	case "confirm_remove_project":
		a.openConfirm("remove_project", param,
			fmt.Sprintf("force-remove every container in %s?", param))
		return a, nil

	case "pull":
		return a, a.run("pulled", param, func(ctx context.Context) error {
			return a.client.PullImage(ctx, param)
		})

	// ---- colima profiles -----------------------------------------------

	case "runtime_busy":
		provider, rest, _ := strings.Cut(param, "\x00")
		name, op, _ := strings.Cut(rest, "\x00")
		a.errFlash = fmt.Sprintf("%s %s is already %s — wait for it to finish", provider, name, op)
		return a, nil
	case "runtime_connect":
		return a, a.runtimeConnect(param)
	case "runtime_shell":
		return a, a.runtimeShell(param)
	case "runtime_start":
		return a, a.runtimeStart(param)
	case "runtime_inspect":
		return a, a.runtimeInspect(param)
	case "runtime_new":
		return a, a.runtimeNew(param)
	case "runtime_edit":
		return a, a.runtimeEdit(param)
	case "runtime_create":
		return a, a.runtimeCreate(param)
	case "runtime_apply":
		return a, a.runtimeApply(param)
	case "confirm_runtime_stop", "confirm_runtime_restart", "confirm_runtime_delete":
		verb := strings.TrimPrefix(action, "confirm_runtime_")
		if q, ok := a.runtimeQuestion(verb, param); ok {
			a.openConfirm("runtime_"+verb, param, q)
		}
		return a, nil
	}

	return a, nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// openInspect pushes an inspect view for any object kind.
func (a *App) openInspect(kind views.InspectKind, id, name string) (tea.Model, tea.Cmd) {
	a.setView(style.ViewInspect, views.NewInspectView(a.client, kind, id, name))
	a.pushView(style.ViewInspect)
	return a, a.viewMap[style.ViewInspect].Init()
}

// run wraps a mutating daemon call in a tea.Cmd that reports back as an
// actionDoneMsg. Every lifecycle key lands here, so the flash text, the
// error surface, and the follow-up refresh are written once.
func (a *App) run(verb, subject string, fn func(context.Context) error) tea.Cmd {
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		return actionDoneMsg{verb: verb, subject: subject, err: fn(ctx)}
	}
}

// runProject applies a per-container operation to every container in a
// compose project. Failures are collected rather than aborting the sweep:
// stopping four of five containers and saying which one refused is more
// useful than stopping two and bailing.
func (a *App) runProject(project, verb string, fn func(context.Context, string) error) tea.Cmd {
	client := a.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()

		list, err := client.Containers(ctx, true)
		if err != nil {
			return actionDoneMsg{verb: verb, subject: project, err: err}
		}
		var failed []string
		n := 0
		for _, c := range list {
			if c.Project != project {
				continue
			}
			n++
			if err := fn(ctx, c.ID); err != nil {
				failed = append(failed, c.Name)
			}
		}
		if len(failed) > 0 {
			return actionDoneMsg{
				verb:    verb,
				subject: project,
				err:     fmt.Errorf("%s: %d of %d failed (%s)", project, len(failed), n, strings.Join(failed, ", ")),
			}
		}
		return actionDoneMsg{verb: fmt.Sprintf("%s %d containers in", verb, n), subject: project}
	}
}

// handleActionDone folds a mutation result into the UI and refetches.
func (a *App) handleActionDone(msg actionDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.errFlash = docker.FormatUserError(msg.err).Error()
	} else {
		a.flash = strings.TrimSpace(msg.verb + " " + msg.subject)
	}
	if msg.hasRefresh {
		if v := a.viewMap[msg.refresh]; v != nil {
			return a, v.Refresh()
		}
	}
	return a, a.refreshActiveView()
}

// openConfirm parks a destructive action behind the y/n bar. It returns
// nothing: the confirm widget surfaces no focus command, and the real work
// happens later in the dispatch closure.
func (a *App) openConfirm(action, param, question string) {
	pa := pendingAction{action: action, param: param}
	a.confirmDispatch = func(yes bool) (string, tea.Cmd) {
		if !yes {
			return "", nil
		}
		return "", a.executeConfirmed(pa)
	}
	a.confirm.Open(question)
}

// executeConfirmed runs an action the user just approved.
func (a *App) executeConfirmed(pa pendingAction) tea.Cmd { //nolint:gocyclo // flat action dispatch
	switch pa.action {
	case "kill":
		return a.run("killed", a.containerName(pa.param), func(ctx context.Context) error {
			return a.client.KillContainer(ctx, pa.param, "SIGKILL")
		})
	case "node_remove":
		node, id, name := splitNodeParam(pa.param)
		return a.run("removed", name, func(ctx context.Context) error {
			return a.client.NodeRemove(ctx, node, id)
		})
	case "remove_container":
		name := a.containerName(pa.param)
		return a.run("removed", name, func(ctx context.Context) error {
			// force: the user already confirmed, and refusing because the
			// container happens to be running would just make them stop it
			// and press the same key again.
			return a.client.RemoveContainer(ctx, pa.param, true, false)
		})
	case "remove_image":
		return a.run("removed image", pa.param, func(ctx context.Context) error {
			return a.client.RemoveImage(ctx, pa.param, false)
		})
	case "remove_volume":
		return a.run("removed volume", pa.param, func(ctx context.Context) error {
			return a.client.RemoveVolume(ctx, pa.param, false)
		})
	case "remove_network":
		id, name, _ := strings.Cut(pa.param, "\x00")
		return a.run("removed network", name, func(ctx context.Context) error {
			return a.client.RemoveNetwork(ctx, id, name)
		})
	case "prune_images":
		return a.runPrune("images", func(ctx context.Context) (int, uint64, error) {
			return a.client.PruneImages(ctx, true)
		})
	case "prune_volumes":
		return a.runPrune("volumes", func(ctx context.Context) (int, uint64, error) {
			return a.client.PruneVolumes(ctx)
		})
	case "prune_containers":
		return a.runPrune("containers", func(ctx context.Context) (int, uint64, error) {
			return a.client.PruneContainers(ctx)
		})
	case "prune_networks":
		return a.runPrune("networks", func(ctx context.Context) (int, uint64, error) {
			n, err := a.client.PruneNetworks(ctx)
			return n, 0, err
		})
	case "pod_rm":
		return a.podRun(pa.param, "rm", "removed")
	case "stop_forward":
		id, label, _ := strings.Cut(pa.param, "\x00")
		return a.run("stopped", "forwarding "+label, func(ctx context.Context) error {
			return a.client.StopPortForward(ctx, id)
		})
	case "prune_all":
		return a.runPruneAll(a.pruneAllSteps(false))
	case "prune_all_volumes":
		return a.runPruneAll(a.pruneAllSteps(true))
	case "prune_cache":
		return a.runPruneAll(pruneOnly(a.pruneAllSteps(false), "build cache"))
	case "stop_project":
		return a.runProject(pa.param, "stopped", func(ctx context.Context, id string) error {
			return a.client.StopContainer(ctx, id, stopTimeout)
		})
	case "remove_project":
		return a.runProject(pa.param, "removed", func(ctx context.Context, id string) error {
			return a.client.RemoveContainer(ctx, id, true, false)
		})
	case "compose_down":
		return a.composeDown(pa.param)
	case "runtime_stop", "runtime_restart", "runtime_delete":
		return a.runtimeConfirmed(strings.TrimPrefix(pa.action, "runtime_"), pa.param)
	case "runtime_apply":
		return a.runtimeApplyNow(pa.param)
	}
	return nil
}

// runPrune wraps a prune call, reporting what came back.
func (a *App) runPrune(kind string, fn func(context.Context) (int, uint64, error)) tea.Cmd {
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		n, reclaimed, err := fn(ctx)
		if err != nil {
			return actionDoneMsg{verb: "prune", subject: kind, err: err}
		}
		subject := fmt.Sprintf("%d %s", n, kind)
		if reclaimed > 0 {
			subject += fmt.Sprintf(
				", %s reclaimed",
				docker.HumanSize(int64(reclaimed)),
			) //nolint:gosec // daemon-reported byte count
		}
		return actionDoneMsg{verb: "pruned", subject: subject}
	}
}

// execShell drops the user into a shell inside a container.
//
// It shells out to the `docker` CLI rather than driving the SDK's
// hijacked-stream exec API. That is deliberate: an interactive session
// needs raw-mode TTY handling, window-resize propagation, and signal
// forwarding, all of which the CLI already does correctly, and
// tea.ExecProcess hands it the real terminal for the duration. Re-doing
// that against the hijacked stream is a large amount of code to get a
// worse shell.
// shellProbe tries bash and falls back to sh. Most images have only sh,
// and a hard-coded bash fails on every alpine container.
const shellProbe = `if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi`

func (a *App) execShell(id string) tea.Cmd {
	return a.dockerExecIt(id, "sh", "-c", shellProbe)
}

// nodeShell opens a shell in container id inside Kubernetes node node:
// docker exec into the node, then crictl exec into the container.
func (a *App) nodeShell(node, id string) tea.Cmd {
	return a.dockerExecIt(node, "crictl", "exec", "-it", id, "sh", "-c", shellProbe)
}

// openReport drills into a text report about a container — health, diff,
// stats — shown in the inspect view's plain-text mode.
func (a *App) openReport(v *views.InspectView) (tea.Model, tea.Cmd) {
	a.setView(style.ViewInspect, v)
	a.pushView(style.ViewInspect)
	return a, v.Init()
}

// splitNodeParam unpacks views.NodeParam.
func splitNodeParam(p string) (node, id, name string) {
	node, rest, _ := strings.Cut(p, "\x00")
	id, name, _ = strings.Cut(rest, "\x00")
	return node, id, name
}

// dockerExecIt runs `docker exec -it target argv...` in the foreground,
// against the daemon dockmaster is showing.
func (a *App) dockerExecIt(target string, argv ...string) tea.Cmd {
	bin, err := exec.LookPath("docker")
	if err != nil {
		a.errFlash = "exec needs the `docker` CLI on PATH"
		return nil
	}

	args := []string{"exec", "-it"}
	if host := a.dockerHostArg(); host != "" {
		args = append([]string{"--host", host}, args...)
	}
	args = append(append(args, target), argv...)

	// context.Background() on purpose: the shell's lifetime is the user's,
	// not the poll's. Canceling it would kill their session mid-command.
	// gosec G204: every argument is either a literal or a container ID
	// that came from the daemon's own listing.
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // see above

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		// A non-zero exit is the ordinary way to leave a shell (ctrl-d
		// after a failed command), so it is not surfaced as an error.
		var exitErr *exec.ExitError
		if err != nil && !asExitError(err, &exitErr) {
			return execDoneMsg{err: fmt.Errorf("exec: %w", err)}
		}
		return execDoneMsg{}
	})
}

func asExitError(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok { //nolint:errorlint // direct type check is the intent
		*target = e
		return true
	}
	return false
}

// dockerHostArg returns the endpoint to pass to the docker CLI so an exec
// lands on the same daemon dockmaster is showing. Without it, switching
// context inside dockmaster and then pressing `s` would exec against
// whatever context the *shell* has — a different machine, potentially.
func (a *App) dockerHostArg() string {
	if a.client == nil {
		return ""
	}
	return a.client.Host
}

// containerName resolves an ID to its name for flash text, falling back to
// the short ID.
func (a *App) containerName(id string) string {
	if v := typedView[*views.ContainersView](a, style.ViewContainers); v != nil {
		if name := v.NameFor(id); name != "" {
			return name
		}
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (a *App) imageRef(id string) string {
	if v := typedView[*views.ImagesView](a, style.ViewImages); v != nil {
		if ref := v.RefFor(id); ref != "" {
			return ref
		}
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (a *App) networkName(id string) string {
	if v := typedView[*views.NetworksView](a, style.ViewNetworks); v != nil {
		if name := v.NameFor(id); name != "" {
			return name
		}
	}
	return id
}
