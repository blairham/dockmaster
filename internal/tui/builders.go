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

// cmdBuilders is the :builders command.
const cmdBuilders = "builders"

// builderSlowTimeout bounds the builder verbs that can take minutes: a
// start may pull a docker-container builder's BuildKit image or schedule a
// kubernetes pod, and a prune walks the whole cache. The rest take
// actionTimeout. These are CLI calls, not daemon requests, so they do not
// go through RequestContext.
const builderSlowTimeout = 5 * time.Minute

// builderCreateDoneMsg reports a builder created from the form, or why not.
type builderCreateDoneMsg struct {
	err  error
	name string
}

// newBuildx is the buildx handle for the daemon on screen; a.buildxRun
// stands in for the CLI in tests.
func (a *App) newBuildx() (*docker.Buildx, error) {
	endpoint := a.client.EndpointArgs()
	if a.buildxRun != nil {
		return docker.NewBuildxWithRunner(a.buildxRun, endpoint), nil
	}
	return docker.NewBuildx(endpoint)
}

// openBuilders opens :builders. Without the CLI the view says so.
func (a *App) openBuilders() (tea.Model, tea.Cmd) {
	var list views.ListBuildersFunc
	if bx, err := a.newBuildx(); err == nil {
		list = bx.Builders
	}
	a.setView(style.ViewBuilders, views.NewBuildersView(list))
	a.pushView(style.ViewBuilders)
	return a, a.viewMap[style.ViewBuilders].Init()
}

// inspectBuilder shows `buildx inspect` in the inspect view. It reads, so
// --readonly allows it.
func (a *App) inspectBuilder(name string) (tea.Model, tea.Cmd) {
	bx, err := a.newBuildx()
	if err != nil {
		a.errFlash = err.Error()
		return a, nil
	}
	v := views.NewInspectFetchView(name+" builder", func(ctx context.Context) ([]byte, error) {
		return bx.Inspect(ctx, name)
	})
	v.SetPlain()
	return a.openReport(v)
}

// builderVerb runs one buildx verb on a builder in the background and
// refreshes the builders view after.
func (a *App) builderVerb(action, name string) tea.Cmd {
	bx, err := a.newBuildx()
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	var (
		run     func(context.Context, string) error
		verb    string
		timeout = actionTimeout
	)
	switch action {
	case views.ActStartBuilder:
		run, verb, timeout = bx.Start, "started builder", builderSlowTimeout
		a.flash = "starting builder " + name + "…"
	case views.ActStopBuilder:
		run, verb = bx.Stop, "stopped builder"
	case views.ActUseBuilder:
		run, verb = bx.Use, "now building with"
	case "remove_builder":
		run, verb = bx.Remove, "removed builder"
	default:
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return actionDoneMsg{
			verb: verb, subject: name, err: run(ctx, name),
			refresh: style.ViewBuilders, hasRefresh: true,
		}
	}
}

// confirmRemoveBuilder asks before `buildx rm`, refusing a docker context's
// own builder first: buildx will not remove it, and the reason is better
// said here than as buildx's error after a yes.
func (a *App) confirmRemoveBuilder(name string) {
	if bv := typedView[*views.BuildersView](a, style.ViewBuilders); bv != nil {
		if b, ok := bv.Builder(name); ok && b.ContextBuilder() {
			a.errFlash = fmt.Sprintf(
				"%s is docker context %s's own builder — buildx cannot remove it (remove the context in :ctx)",
				name, name,
			)
			return
		}
	}
	a.openConfirm("remove_builder", name, fmt.Sprintf(
		"remove builder %s? its BuildKit node and its build cache go with it", name,
	))
}

// pruneBuilder is `buildx prune --builder <name>`, with the total it freed
// in the flash.
func (a *App) pruneBuilder(name string) tea.Cmd {
	bx, err := a.newBuildx()
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	a.flash = "pruning builder " + name + "'s cache…"
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), builderSlowTimeout)
		defer cancel()
		total, err := bx.Prune(ctx, name)
		subject := name
		if total = strings.Join(strings.Fields(total), " "); total != "" {
			subject += " — " + total
		}
		return actionDoneMsg{
			verb: "pruned the build cache of", subject: subject, err: err,
			refresh: style.ViewBuilders, hasRefresh: true,
		}
	}
}

// openBuilderForm opens the create form over the builders view.
func (a *App) openBuilderForm() {
	a.setView(style.ViewBuilderForm, views.NewBuilderForm())
	a.pushView(style.ViewBuilderForm)
}

// createBuilder runs `buildx create` from the form; the form stays up until
// buildx answers, so a refusal lands in it.
func (a *App) createBuilder(param string) tea.Cmd {
	spec, err := views.DecodeBuilderSpec(param)
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	bx, err := a.newBuildx()
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	a.flash = "creating builder…"
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		name, err := bx.Create(ctx, spec)
		return builderCreateDoneMsg{name: name, err: err}
	}
}

// handleBuilderCreateDone shows a failure in the form, or on success
// returns to the builders view and lists it again.
func (a *App) handleBuilderCreateDone(msg builderCreateDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		err := docker.FormatUserError(msg.err).Error()
		if f := typedView[*views.BuilderFormView](a, style.ViewBuilderForm); f != nil && a.view == style.ViewBuilderForm {
			f.SetError(err)
			a.flash = ""
		} else {
			a.errFlash = err
		}
		return a, nil
	}
	if a.view == style.ViewBuilderForm {
		a.popView()
	}
	a.flash = "created builder " + msg.name
	if v := a.viewMap[style.ViewBuilders]; v != nil {
		return a, v.Refresh()
	}
	return a, nil
}
