// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"

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
	"pause": true, "unpause": true, "exec": true, "attach": true,
	"confirm_remove_container": true, "confirm_remove_image": true,
	"confirm_remove_volume": true, "confirm_remove_network": true,
	"confirm_prune_images": true, "confirm_prune_volumes": true,
	"confirm_prune_networks": true, "confirm_prune_containers": true,
	"start_project": true, "confirm_stop_project": true,
	"confirm_remove_project": true, "pull": true,
	"runtime_start": true, "runtime_shell": true,
	"confirm_runtime_stop": true, "confirm_runtime_restart": true, "confirm_runtime_delete": true,
	"runtime_new": true, "runtime_edit": true, "runtime_create": true, "runtime_apply": true,
	"compose_up": true, "compose_edit": true, "compose_restart": true, "compose_pull": true, "confirm_compose_down": true,
	"scale_form": true, "compose_scale": true,
	"confirm_prune_all": true, "confirm_prune_all_volumes": true, "confirm_prune_cache": true,
	"confirm_delete_all_images": true, "confirm_delete_all_volumes": true,
	"portforward": true, "confirm_stop_forward": true,
	"pod_start": true, "pod_stop": true, "pod_restart": true, "confirm_pod_rm": true,
	// confirm_host_shell is a root shell on the daemon's host; its confirmed
	// run re-checks readonly itself, since a reload can turn it on meanwhile.
	"node_shell": true, "confirm_node_remove": true, "confirm_host_shell": true,
	"run_image": true, "run_create": true, "copy_into": true,
	"confirm_runtime_k8s": true, "edit_form": true, "edit_apply": true,
	// Not daemon state, but a delete all the same: readonly means hands off.
	"confirm_remove_dump": true,
	"compose_up_file":     true, "compose_edit_file": true, "confirm_compose_down_file": true,
	// The context store, not the daemon — but a removal all the same.
	"confirm_remove_context": true,
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
		return a, a.openLogs(views.NewLogsView(a.client, param, a.containerName(param)))

	case "project_logs":
		return a, a.openLogs(views.NewProjectLogsView(a.client, param))

	case "inspect_container":
		return a.openInspect(views.InspectContainer, param, a.containerName(param))
	case "inspect_image":
		return a.openInspect(views.InspectImage, param, a.imageRef(param))
	case "inspect_volume":
		return a.openInspect(views.InspectVolume, param, param)
	case "browse_volume":
		a.setView(style.ViewVolumeBrowse, views.NewVolumeBrowseView(a.client, param))
		a.pushView(style.ViewVolumeBrowse)
		return a, a.viewMap[style.ViewVolumeBrowse].Init()
	case "volume_dir":
		if v := typedView[*views.VolumeBrowseView](a, style.ViewVolumeBrowse); v != nil {
			return a, v.Open(param)
		}
		return a, nil
	case "volume_file":
		volume, file, _ := strings.Cut(param, "\x00")
		return a.openReport(views.NewVolumeFileView(a.client, volume, file))
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

	case "dumps":
		root, err := a.dumpRoot()
		if err != nil {
			a.errFlash = err.Error()
			return a, nil
		}
		a.setView(style.ViewDumps, views.NewDumpsView(root))
		a.pushView(style.ViewDumps)
		return a, a.viewMap[style.ViewDumps].Init()
	case "xray":
		xv := views.NewXrayView(a.client)
		xv.SetRoot(param)
		a.setView(style.ViewXray, xv)
		a.pushView(style.ViewXray)
		return a, a.viewMap[style.ViewXray].Init()
	case "xray_nav":
		return a, nil
	case "run_command":
		return a, a.runCommand(param)
	case "palette":
		cmd := a.commandBar.OpenWith(param)
		a.resizeActiveView()
		return a, cmd
	case "dir":
		a.setView(style.ViewDir, views.NewDirView(param))
		a.pushView(style.ViewDir)
		return a, a.viewMap[style.ViewDir].Init()
	case "dir_open":
		if v := typedView[*views.DirView](a, style.ViewDir); v != nil {
			return a, v.Open(param)
		}
		return a, nil
	case "dir_file":
		return a.openReport(views.NewDumpFileView(param))
	case "not_compose":
		a.errFlash = param + " is not a compose file — compose.yaml, docker-compose.yml and their variants are"
		return a, nil
	case "compose_up_file":
		return a, a.composeFileUp(param)
	case "compose_edit_file":
		return a, a.composeFileEdit(param)
	case "confirm_compose_down_file":
		a.openConfirm("compose_down_file", param, fmt.Sprintf(
			"compose down %s? its containers and networks are removed — volumes are kept", param,
		))
		return a, nil
	case "scan_image":
		sv := views.NewScanView(param, a.dockerHostArg())
		sv.SetImageID(a.scanImageID(param))
		a.setView(style.ViewScan, sv)
		a.pushView(style.ViewScan)
		return a, a.viewMap[style.ViewScan].Init()
	case "vuln_detail":
		report := param
		v := views.NewInspectFetchView("vulnerability", func(context.Context) ([]byte, error) {
			return []byte(report), nil
		})
		v.SetPlain()
		return a.openReport(v)
	case "lint":
		a.setView(style.ViewLint, views.NewLintView(a.client))
		a.pushView(style.ViewLint)
		return a, a.viewMap[style.ViewLint].Init()
	case "lint_detail":
		lv := typedView[*views.LintView](a, style.ViewLint)
		if lv == nil {
			return a, nil
		}
		r, ok := lv.Result(param)
		if !ok {
			return a, nil
		}
		report := views.LintReport(r)
		v := views.NewInspectFetchView(r.Container.Name+" lint", func(context.Context) ([]byte, error) {
			return []byte(report), nil
		})
		v.SetPlain()
		return a.openReport(v)
	case "open_dump":
		return a.openReport(views.NewDumpFileView(param))

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

	case "used_by":
		return a, a.showUsedBy(param)
	case "jump_project":
		cmd := a.switchView(style.ViewProjects)
		a.filter = "^" + regexp.QuoteMeta(param) + "$"
		a.setActiveFilter(a.filter)
		a.flash = "project " + param
		return a, cmd
	case "no_project":
		a.errFlash = param + " is not part of a compose project"
		return a, nil

	case "switch_context":
		name, host, _ := strings.Cut(param, "\x00")
		a.flash = "connecting to " + name + "..."
		return a, doSwitchContext(name, host)
	case "confirm_remove_context":
		a.confirmRemoveContext(param)
		return a, nil

	// ---- view toggles ---------------------------------------------------

	case "toggle_event_faults":
		if v := typedView[*views.EventsView](a, style.ViewEvents); v != nil {
			v.ToggleFaults()
			a.flash = "faults only " + onOff(v.Faults())
		}
		return a, nil
	case "toggle_wide":
		if v := typedView[*views.ContainersView](a, style.ViewContainers); v != nil {
			v.ToggleWide()
			a.flash = "wide columns " + onOff(v.Wide())
		}
		return a, nil
	case "toggle_faults":
		if v := typedView[*views.ContainersView](a, style.ViewContainers); v != nil {
			cmd := v.ToggleFaults()
			a.flash = "faults only " + onOff(v.ShowFaults())
			return a, cmd
		}
		return a, nil
	case "toggle_kube":
		if v := typedView[*views.ContainersView](a, style.ViewContainers); v != nil {
			v.ToggleKube()
			a.flash = "Kubernetes containers " + map[bool]string{true: "shown", false: "hidden"}[v.ShowKube()]
		}
		return a, nil
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
	case "log_range", "log_wrap", "log_clear", "log_copy", "log_save", "log_mark":
		return a, a.logAction(action, param)
	case "inspect_copy", "inspect_save", "inspect_auto", "search_next", "search_prev":
		return a, a.inspectAction(action)
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
	case "attach":
		return a, a.attach(param)
	case "not_running":
		a.errFlash = param + " is not running — start it (u) to attach"
		return a, nil

	// ---- Kubernetes nodes: the containers inside a kind/k3d node -------

	case "plugin_inputs":
		return a, a.submitPluginInputs(param)

	case "edit_form":
		return a, a.loadEditForm(param)
	case "edit_apply":
		e, err := views.DecodeEditApply(param)
		if err != nil {
			a.errFlash = err.Error()
			return a, nil
		}
		return a, a.applyEdit(e)

	case "copy_form":
		return a.openCopyForm(param)
	case "copy_from", "copy_into":
		spec, err := views.DecodeCopySpec(param)
		if err != nil {
			a.errFlash = err.Error()
			return a, nil
		}
		return a, a.runCopy(spec)

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

	case "registry_not_node":
		a.errFlash = param + " is the kind clusters' local registry, not a node — push to it at localhost:" +
			docker.DefaultRegistry.Port + "; nodes pull from it by name"
		return a, nil
	case "not_a_node":
		a.errFlash = param + " is not a kind/k3d node — c opens the containers inside a ⎈ node"
		return a, nil
	case "node_containers":
		node, _, name := splitNodeParam(param)
		nv := views.NewNodeView(a.client, node, name)
		nv.SetRegistry(a.registryLine(node))
		a.setView(style.ViewNode, nv)
		a.pushView(style.ViewNode)
		return a, a.viewMap[style.ViewNode].Init()
	case "node_logs":
		node, id, name := splitNodeParam(param)
		return a, a.openLogs(views.NewNodeLogsView(a.client, node, id, name))
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
	case "confirm_remove_dump":
		a.openConfirm("remove_dump", param, fmt.Sprintf("delete %s?", filepath.Base(param)))
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
	case "show_forwards":
		return a, a.showForwards(param)
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
	case "confirm_delete_all_images":
		a.confirmDeleteAll("images")
		return a, nil
	case "confirm_delete_all_volumes":
		a.confirmDeleteAll("volumes")
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
	case "compose_edit":
		return a, a.composeEdit(param)
	case "compose_restart":
		return a, a.composeRestart(param)
	case "compose_pull":
		return a, a.composePull(param)
	case "confirm_compose_down":
		a.confirmComposeDown(param)
		return a, nil
	case "scale_form":
		a.openScaleForm(param)
		return a, nil
	case "compose_scale":
		return a, a.submitScale(param)
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
	case "confirm_host_shell":
		a.confirmHostShell()
		return a, nil
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
	case "confirm_runtime_k8s":
		if q, op, ok := a.kubeQuestion(param); ok {
			// op goes first: a machine key itself holds a \x00.
			a.openConfirm("runtime_k8s", op.encode()+"\x00"+param, q)
		}
		return a, nil

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

// openConfirm parks a destructive action behind the confirm dialog. It
// returns nothing: the dialog surfaces no focus command, and the real work
// happens later in the dispatch closure.
func (a *App) openConfirm(action, param, question string) {
	pa := pendingAction{action: action, param: param}
	a.confirmDispatch = func(yes bool) (string, tea.Cmd) {
		if !yes {
			return "", nil
		}
		return "", a.executeConfirmed(pa)
	}
	a.ask(confirmTitle(action), question)
}

// ask opens the confirm dialog — k9s's popup over the table, focus on
// Cancel so a stray enter never runs the action; y answers yes directly.
// a.confirmDispatch must already be set.
func (a *App) ask(title, question string) {
	a.confirm.Open(question, chrome.ModalOpts{Title: title})
}

// confirmTitle names the dialog for an action, the way k9s heads its own
// with <Delete> or <Kill>.
func confirmTitle(action string) string {
	switch action {
	case "kill":
		return "Kill"
	case "host_shell":
		return "Host Shell"
	case "compose_down", "compose_down_file":
		return "Compose Down"
	case "compose_scale":
		return "Scale"
	case "runtime_k8s":
		return "Kubernetes"
	case "stop_forward":
		return "Stop Forward"
	case "stop_project":
		return "Stop"
	case "remove_dump":
		return "Delete"
	case "pod_rm", "node_remove":
		return "Remove"
	}
	switch {
	case strings.HasPrefix(action, "prune"):
		return "Prune"
	case strings.HasPrefix(action, "delete_all"):
		return "Delete"
	case strings.HasPrefix(action, "remove"):
		return "Remove"
	case action == "runtime_stop", action == "runtime_restart", action == "runtime_delete":
		v := strings.TrimPrefix(action, "runtime_")
		return strings.ToUpper(v[:1]) + v[1:]
	}
	return "Confirm"
}

// executeConfirmed runs an action the user just approved.
//
//nolint:gocyclo,funlen // flat action dispatch: one case per confirmed action, long by count not depth
func (a *App) executeConfirmed(pa pendingAction) tea.Cmd {
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
	case "remove_dump":
		root, err := a.dumpRoot()
		if err != nil {
			a.errFlash = err.Error()
			return nil
		}
		return removeDump(root, pa.param)
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
	case "delete_all_images":
		return a.runDeleteAll("images", a.client.RemoveAllImages)
	case "delete_all_volumes":
		return a.runDeleteAll("volumes", a.client.RemoveAllVolumes)
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
	case "compose_down_file":
		return a.composeFileDown(pa.param)
	case "remove_context":
		return a.removeContext(pa.param)
	case "compose_scale":
		sp, err := views.DecodeScaleSpec(pa.param)
		if err != nil {
			a.errFlash = err.Error()
			return nil
		}
		return a.composeScale(sp)
	case "host_shell":
		return a.hostShell(pa.param)
	case "runtime_stop", "runtime_restart", "runtime_delete":
		return a.runtimeConfirmed(strings.TrimPrefix(pa.action, "runtime_"), pa.param)
	case "runtime_apply":
		return a.runtimeApplyNow(pa.param)
	case "runtime_k8s":
		op, key := decodeKubeOp(pa.param)
		return a.runtimeKube(key, op)
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

// preferredShellProbe runs the configured shell when the container has it,
// and falls back to shellProbe's bash-then-sh when it does not. The shell's
// name arrives as $0, so it is never parsed as part of the script.
const preferredShellProbe = `if command -v "$0" >/dev/null 2>&1; then exec "$0"; fi; ` + shellProbe

func (a *App) execShell(id string) tea.Cmd {
	if a.shell != "" {
		return a.dockerExecIt(id, "sh", "-c", preferredShellProbe, a.shell)
	}
	return a.dockerExecIt(id, "sh", "-c", shellProbe)
}

// attachDetachHint is printed before docker takes the terminal, because the
// way out depends on the container: one started with a terminal (-it)
// detaches on ctrl-p ctrl-q, and its process reads ctrl-c as a keystroke;
// one without — most compose services — never sees the detach keys, and
// with signals not proxied, ctrl-c just ends the attach.
const attachDetachHint = "attached — ctrl-p ctrl-q detaches (a container with a terminal); " +
	"without one, ctrl-c detaches and the container keeps running"

// attach connects the terminal to a running container's main process,
// docker attach, as k9s's a does (#16), never forwarding a signal to it. It
// goes through the docker CLI with
// --host, as s does, and through a one-line sh that prints the detach keys
// first; the arguments reach docker as positional parameters, so no shell
// ever parses them.
func (a *App) attach(id string) tea.Cmd {
	bin, err := exec.LookPath("docker")
	if err != nil {
		a.errFlash = "attach needs the `docker` CLI on PATH"
		return nil
	}
	args := []string{"-c", `printf '%s\n' "$0"; exec "$@"`, attachDetachHint, bin}
	args = append(args, a.client.EndpointArgs()...)
	// --sig-proxy=false: by default docker attach forwards a ctrl-c to the
	// container as SIGINT, which stops most services; off, it only ends
	// the attach.
	args = append(args, "attach", "--sig-proxy=false", id)
	// context.Background() on purpose, as in dockerExecIt: the session is
	// the user's, not the poll's.
	cmd := exec.CommandContext(
		context.Background(),
		"/bin/sh",
		args..., //nolint:gosec // fixed script; id comes from the daemon's listing
	)
	return a.inTerminal(cmd, func(err error) tea.Msg {
		// Detaching, and the container exiting under ctrl-c, both end the
		// session with a non-zero status; neither is an error worth a flash.
		var exitErr *exec.ExitError
		if err != nil && !asExitError(err, &exitErr) {
			return execDoneMsg{err: fmt.Errorf("attach: %w", err)}
		}
		return execDoneMsg{}
	})
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

	args := append(a.client.EndpointArgs(), "exec", "-it")
	args = append(append(args, target), argv...)

	// context.Background() on purpose: the shell's lifetime is the user's,
	// not the poll's. Canceling it would kill their session mid-command.
	// gosec G204: every argument is either a literal or a container ID
	// that came from the daemon's own listing.
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // see above

	return a.inTerminal(cmd, func(err error) tea.Msg {
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
	if v := typedView[*views.XrayView](a, style.ViewXray); v != nil {
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

// removeDump deletes a file :sd listed. It refuses anything outside the
// dump directories, so a confirm can only ever delete a save.
func removeDump(root, path string) tea.Cmd {
	return func() tea.Msg {
		name := filepath.Base(path)
		var err error
		if !views.IsDumpPath(root, path) {
			err = fmt.Errorf("%s is not a saved dump", path)
		}
		if err == nil {
			err = os.Remove(path)
		}
		return actionDoneMsg{verb: "deleted", subject: name, err: err}
	}
}

// showUsedBy opens the containers that use an image, a volume or a network
// (#8): every one, stopped included, since a stopped container still holds
// what it uses. It is a drill-in, so esc goes back to the list it came from.
func (a *App) showUsedBy(param string) tea.Cmd {
	kind, key, name := splitUsedBy(param)
	var match func(docker.Container) bool
	switch kind {
	case "image":
		match = func(c docker.Container) bool { return c.UsesImage(key) }
	case "volume":
		match = func(c docker.Container) bool { return c.UsesVolume(key) }
	case "network":
		match = func(c docker.Container) bool { return c.OnNetwork(key) }
	default:
		return nil
	}
	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	if cv == nil {
		return nil
	}
	cv.SetScope("using "+kind+" "+name, match)
	var cmds []tea.Cmd
	if !cv.ShowAll() {
		cmds = append(cmds, cv.ToggleAll())
	}
	a.pushView(style.ViewContainers)
	a.setActiveFilter("")
	cmds = append(cmds, cv.Refresh())
	return tea.Batch(cmds...)
}

func splitUsedBy(p string) (kind, key, name string) {
	kind, rest, _ := strings.Cut(p, "\x00")
	key, name, _ = strings.Cut(rest, "\x00")
	return kind, key, name
}

// confirmRemoveContext asks before `docker context rm`, refusing first the
// contexts that must not go: docker's built-in default, the one dockmaster
// is connected through, and the CLI's current one, which the CLI itself
// refuses without -f.
func (a *App) confirmRemoveContext(name string) {
	switch {
	case name == "default":
		a.errFlash = "default is docker's built-in context — it cannot be removed"
		return
	case a.client != nil && a.client.ContextName == name:
		a.errFlash = "dockmaster is connected through " + name + " — switch to another context before removing it"
		return
	}
	for _, c := range docker.Contexts() {
		if c.Name == name && c.Current {
			a.errFlash = name + " is the docker CLI's current context — `docker context use` another before removing it"
			return
		}
	}
	a.openConfirm("remove_context", name, fmt.Sprintf(
		"remove docker context %s? its endpoint and TLS files leave the context store", name,
	))
}

// removeContext runs `docker context rm` in the background and refreshes
// the contexts view after, whichever view is showing by then.
func (a *App) removeContext(name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDoneMsg{
			verb: "removed context", subject: name, err: docker.RemoveContext(ctx, name),
			refresh: style.ViewContexts, hasRefresh: true,
		}
	}
}
