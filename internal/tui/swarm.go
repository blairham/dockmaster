// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// The swarm actions executeConfirmed runs, once asked.
const (
	actServiceScale    = "service_scale_confirmed"
	actRemoveService   = "remove_service"
	actDrainNode       = "drain_node"
	actRemoveSwarmNode = "remove_swarm_node"
	actRemoveSecret    = "remove_secret"
	actRemoveConfig    = "remove_config"
)

// handleSwarmRefresh hands a swarm listing to its own view whichever view
// is showing: one dropped on another view would leave its single-flight
// guard set, and the view would never poll again.
func (a *App) handleSwarmRefresh(msg views.SwarmRefreshMsg) (tea.Model, tea.Cmd) {
	if !a.splashActive && a.view == msg.Kind.ViewType() {
		a.loading = false
	}
	if v := typedView[*views.SwarmView](a, msg.Kind.ViewType()); v != nil {
		return a, v.Update(msg)
	}
	return a, nil
}

// swarmAction carries out one of the swarm views' actions; ok is false for
// an action that is not one.
//
//nolint:gocyclo // flat action dispatch, one case per swarm action
func (a *App) swarmAction(action, param string) (tea.Cmd, bool) {
	switch action {
	case views.ActSwarmInspect:
		p := views.SplitSwarmParam(param, 3)
		_, cmd := a.openSwarmInspect(p[0], p[1], p[2])
		return cmd, true
	case views.ActSwarmLogs:
		p := views.SplitSwarmParam(param, 3)
		return a.openLogs(views.NewSwarmLogsView(a.client, p[0], p[1], p[2])), true
	case views.ActSwarmTasks:
		p := views.SplitSwarmParam(param, 3)
		return a.showSwarmTasks(p[0], p[1], p[2]), true
	case views.ActToggleTaskHistory:
		if param == "true" {
			a.flash = "tasks: showing history"
		} else {
			a.flash = "tasks: live only"
		}
		return nil, true
	case views.ActNotScalable:
		p := views.SplitSwarmParam(param, 2)
		a.errFlash = fmt.Sprintf("%s is a %s service — only a replicated service has a count to scale", p[0], p[1])
		return nil, true
	case views.ActServiceScaleForm:
		p := views.SplitSwarmParam(param, 3)
		n, err := strconv.Atoi(p[2])
		if err != nil {
			a.errFlash = "cannot scale " + p[1] + ": no replica count in " + strconv.Quote(p[2])
			return nil, true
		}
		a.setView(style.ViewScaleForm, views.NewServiceScaleForm(p[0], p[1], n))
		a.pushView(style.ViewScaleForm)
		return nil, true
	case views.ActServiceScale:
		return a.submitServiceScale(param), true
	case views.ActServiceRestart:
		p := views.SplitSwarmParam(param, 2)
		return a.swarmRun("restarting", p[1], style.ViewServices, func(ctx context.Context) error {
			return a.client.RestartService(ctx, p[0])
		}), true
	case views.ActNodeAvailability:
		p := views.SplitSwarmParam(param, 3)
		return a.swarmRun("set to "+p[2], p[1], style.ViewSwarmNodes, func(ctx context.Context) error {
			return a.client.SetNodeAvailability(ctx, p[0], p[2])
		}), true
	}
	return nil, a.askSwarm(action, param)
}

// askSwarm opens the confirm for a swarm action that needs one; false for
// an action that does not.
func (a *App) askSwarm(action, param string) bool {
	switch action {
	case views.ActRemoveService:
		p := views.SplitSwarmParam(param, 2)
		a.openConfirm(actRemoveService, param, fmt.Sprintf(
			"remove service %s? every one of its tasks is stopped and removed", p[1],
		))
		return true
	case views.ActDrainNode:
		p := views.SplitSwarmParam(param, 2)
		a.openConfirm(actDrainNode, param, fmt.Sprintf(
			"drain node %s? its tasks are stopped there and rescheduled on other nodes", p[1],
		))
		return true
	case views.ActRemoveSwarmNode:
		p := views.SplitSwarmParam(param, 2)
		a.openConfirm(actRemoveSwarmNode, param, fmt.Sprintf(
			"remove node %s from the swarm? the manager refuses one that is still up — drain and leave it first", p[1],
		))
		return true
	case views.ActRemoveSecret:
		p := views.SplitSwarmParam(param, 2)
		a.openConfirm(actRemoveSecret, param, fmt.Sprintf(
			"remove secret %s? its value cannot be read back — keep a copy if it is needed", p[1],
		))
		return true
	case views.ActRemoveConfig:
		p := views.SplitSwarmParam(param, 2)
		a.openConfirm(actRemoveConfig, param, fmt.Sprintf("remove config %s?", p[1]))
		return true
	}
	return false
}

// swarmConfirmed runs a swarm action the user just approved; ok is false
// for one that is not.
func (a *App) swarmConfirmed(pa pendingAction) (tea.Cmd, bool) {
	p := views.SplitSwarmParam(pa.param, 2)
	id, name := p[0], p[1]
	switch pa.action {
	case actServiceScale:
		sp, err := views.DecodeScaleSpec(pa.param)
		if err != nil {
			a.errFlash = err.Error()
			return nil, true
		}
		return a.serviceScale(sp), true
	case actRemoveService:
		return a.swarmRun("removed service", name, style.ViewServices, func(ctx context.Context) error {
			return a.client.RemoveService(ctx, id)
		}), true
	case actDrainNode:
		return a.swarmRun("draining", name, style.ViewSwarmNodes, func(ctx context.Context) error {
			return a.client.SetNodeAvailability(ctx, id, "drain")
		}), true
	case actRemoveSwarmNode:
		return a.swarmRun("removed node", name, style.ViewSwarmNodes, func(ctx context.Context) error {
			return a.client.RemoveSwarmNode(ctx, id)
		}), true
	case actRemoveSecret:
		return a.swarmRun("removed secret", name, style.ViewSecrets, func(ctx context.Context) error {
			return a.client.RemoveSecret(ctx, id)
		}), true
	case actRemoveConfig:
		return a.swarmRun("removed config", name, style.ViewConfigs, func(ctx context.Context) error {
			return a.client.RemoveConfig(ctx, id)
		}), true
	}
	return nil, false
}

// swarmRun is run for a swarm change, refreshing the view it was made
// from after.
func (a *App) swarmRun(verb, subject string, refresh style.ViewType, fn func(context.Context) error) tea.Cmd {
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		return actionDoneMsg{verb: verb, subject: subject, err: fn(ctx), refresh: refresh, hasRefresh: true}
	}
}

// openSwarmInspect shows a service's, task's, node's, secret's or config's
// inspect JSON. It reads, so --readonly allows it.
func (a *App) openSwarmInspect(kind, id, name string) (tea.Model, tea.Cmd) {
	client := a.client
	inspect := map[string]func(context.Context, string) ([]byte, error){
		views.SwarmServices.Noun(): client.InspectService,
		views.SwarmTasks.Noun():    client.InspectTask,
		views.SwarmNodes.Noun():    client.InspectSwarmNode,
		views.SwarmSecrets.Noun():  client.InspectSecret,
		views.SwarmConfigs.Noun():  client.InspectConfig,
	}[kind]
	if inspect == nil {
		a.errFlash = "nothing to inspect for a " + kind
		return a, nil
	}
	v := views.NewInspectFetchView(name, func(ctx context.Context) ([]byte, error) {
		return inspect(ctx, id)
	})
	return a.openReport(v)
}

// showSwarmTasks opens the tasks view narrowed to one service's or one
// node's tasks, as a drill-in: esc goes back, and the narrowing lifts when
// it is left.
func (a *App) showSwarmTasks(by, id, name string) tea.Cmd {
	tv := typedView[*views.SwarmView](a, style.ViewTasks)
	if tv == nil {
		return nil
	}
	scope, label := docker.TaskScope{Service: id}, "of service "+name
	if by == views.SwarmByNode {
		scope, label = docker.TaskScope{Node: id}, "on node "+name
	}
	tv.SetScope(scope, label)
	a.pushView(style.ViewTasks)
	a.setActiveFilter("")
	return tv.Refresh()
}

// clearTaskScope lifts showSwarmTasks' narrowing.
func (a *App) clearTaskScope() {
	if tv := typedView[*views.SwarmView](a, style.ViewTasks); tv != nil {
		tv.ClearScope()
	}
}

// submitServiceScale takes a submitted service scale form: the form
// closes, and a scale down confirms, naming the count change.
func (a *App) submitServiceScale(param string) tea.Cmd {
	if a.view == style.ViewScaleForm {
		a.popView()
	}
	sp, err := views.DecodeScaleSpec(param)
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	if sp.Replicas < sp.Current {
		removed := sp.Current - sp.Replicas
		q := fmt.Sprintf("scale service %s from %d to %d? %d task%s stopped",
			sp.Service, sp.Current, sp.Replicas, removed, plural(removed, " is", "s are"))
		if sp.Replicas == 0 {
			q = fmt.Sprintf("scale service %s from %d to 0? every one of its tasks is stopped", sp.Service, sp.Current)
		}
		a.openConfirm(actServiceScale, param, q)
		return nil
	}
	return a.serviceScale(sp)
}

// serviceScale runs a swarm service scale.
func (a *App) serviceScale(sp views.ScaleSpec) tea.Cmd {
	n := uint64(max(sp.Replicas, 0))
	return a.swarmRun("scaled to "+strconv.Itoa(sp.Replicas), sp.Service, style.ViewServices,
		func(ctx context.Context) error { return a.client.ScaleService(ctx, sp.ServiceID, n) })
}
