// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// The build history (#118): `:builds`, or 7, lists what the builder
// recorded — `docker buildx history` — with dockmaster's own running
// builds (#121) on top. enter replays a record's log into the output view,
// o inspects it, ctrl-d removes it, R opens the build form on it again,
// and :delete all removes every record at once.

// cmdBuilds is the :builds command.
const cmdBuilds = "builds"

// The build history's record removal, asked and confirmed.
const (
	actConfirmRemoveBuild = "confirm_remove_build"
	actRemoveBuild        = "remove_build"
)

// historyTimeout bounds a single history call other than the listing:
// an inspect, a remove.
const historyTimeout = 30 * time.Second

// rebuildMsg is a record's detail, read for R.
type rebuildMsg struct {
	err  error
	name string
	spec docker.BuildSpec
}

// buildHistory is the history of config's builder on the daemon on
// screen, through the docker CLI — or the test runner.
func (a *App) buildHistory() (*docker.BuildHistory, error) {
	var endpoint []string
	if a.client != nil {
		endpoint = a.client.EndpointArgs()
	}
	if a.historyRunner != nil {
		return docker.NewBuildHistoryWithRunner(a.historyRunner, endpoint, a.builder), nil
	}
	return docker.NewBuildHistory(endpoint, a.builder)
}

// newBuildsView makes the builds view over config's builder as it is now
// and registers it: a fresh one each time, so a builder changed by a
// config reload is the one read.
func (a *App) newBuildsView() *views.BuildsView {
	h, err := a.buildHistory()
	var src views.BuildHistorySource
	if err == nil {
		src = h
	}
	bv := views.NewBuildsView(src, err)
	bv.SetLocal(a.runningBuilds())
	a.setView(style.ViewBuilds, bv)
	return bv
}

// openBuilds pushes the builds view, as :builds does: esc comes back.
func (a *App) openBuilds() tea.Cmd {
	bv := a.newBuildsView()
	a.pushView(style.ViewBuilds)
	return bv.Init()
}

// switchBuilds is 7: the builds view as a top-level view, like the other
// digits' — the stack cleared, the visit in the view history.
func (a *App) switchBuilds() tea.Cmd {
	a.newBuildsView()
	return a.switchView(style.ViewBuilds)
}

// deleteAllBuilds removes every record of the builder — `buildx history
// rm --all` — once :delete all is confirmed, then lists again.
func (a *App) deleteAllBuilds() tea.Cmd {
	h, err := a.buildHistory()
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	return a.runDeleteAll("builds", h.RemoveAll)
}

// syncBuildsView hands the builds view dockmaster's running builds, after
// one starts or ends.
func (a *App) syncBuildsView() {
	if bv := typedView[*views.BuildsView](a, style.ViewBuilds); bv != nil {
		bv.SetLocal(a.runningBuilds())
	}
}

// splitRecord is a record action's param: its ID and name.
func splitRecord(param string) (id, name string) {
	id, name, _ = strings.Cut(param, "\x00")
	return id, name
}

// buildLogs replays a record's log — `buildx history logs` — into the
// output view, where it can be searched, wrapped and saved as a build's.
func (a *App) buildLogs(param string) tea.Cmd {
	id, name := splitRecord(param)
	h, err := a.buildHistory()
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	b := views.NewBuild("build log "+name, name, a.streamCLI(func(_ context.Context, bin string) docker.CommandSpec {
		return h.LogsCommand(bin, id)
	}))
	b.Replay = true
	a.builds = append(a.builds, b)
	cmd := b.Start()
	a.attachBuild(b)
	return cmd
}

// buildInspect shows a record as buildx describes it, in the inspect view.
func (a *App) buildInspect(param string) (tea.Model, tea.Cmd) {
	id, name := splitRecord(param)
	h, err := a.buildHistory()
	if err != nil {
		a.errFlash = err.Error()
		return a, nil
	}
	v := views.NewInspectFetchView(name+" build", func(ctx context.Context) ([]byte, error) {
		return h.Inspect(ctx, id)
	})
	v.SetPlain()
	return a.openReport(v)
}

// removeBuild deletes a record, then lists the history again.
func (a *App) removeBuild(param string) tea.Cmd {
	id, name := splitRecord(param)
	h, err := a.buildHistory()
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
		defer cancel()
		return actionDoneMsg{
			verb: "removed build record", subject: name, err: h.Remove(ctx, id),
			refresh: style.ViewBuilds, hasRefresh: true,
		}
	}
}

// rebuild reads a record's context, Dockerfile, target and args, for the
// build form.
func (a *App) rebuild(param string) tea.Cmd {
	id, name := splitRecord(param)
	h, err := a.buildHistory()
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	a.flash = "reading " + name + "…"
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
		defer cancel()
		d, err := h.Detail(ctx, id)
		if err != nil {
			return rebuildMsg{name: name, err: err}
		}
		spec, err := d.Spec()
		return rebuildMsg{name: name, spec: spec, err: err}
	}
}

// handleRebuild opens the build form on what the record built — #104's
// path, so the build runs as any other — or says why it cannot.
func (a *App) handleRebuild(msg rebuildMsg) {
	if msg.err != nil {
		a.errFlash = fmt.Sprintf("cannot build %s again: %s", msg.name, msg.err)
		return
	}
	if a.readonly {
		a.errFlash = "readonly mode — rebuild refused"
		return
	}
	a.setView(style.ViewBuildForm, views.NewBuildFormFrom(msg.spec))
	a.pushView(style.ViewBuildForm)
}

// buildsAction carries out the builds view's keys that change nothing.
func (a *App) buildsAction(action, param string) (tea.Model, tea.Cmd) {
	switch action {
	case "attach_build":
		if b := a.buildByParam(param); b != nil {
			a.attachBuild(b)
		}
	case "toggle_build_errors":
		if bv := typedView[*views.BuildsView](a, style.ViewBuilds); bv != nil {
			bv.ToggleErrors()
			a.flash = "failed builds only " + onOff(bv.ErrorsOnly())
		}
	case "build_still_running":
		a.errFlash = param + " is still running — x stops it"
	case "build_logs":
		cmd := a.buildLogs(param)
		return a, cmd
	case "build_inspect":
		return a.buildInspect(param)
	}
	return a, nil
}
