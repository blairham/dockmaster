// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// Builds run in the background (#121). The app owns every build it starts —
// its command, its output and how it ended — in a.builds; the output view
// is only a window onto one, so esc detaches and the build carries on.
// Stopping is x in that view, confirmed, and quitting with one running asks
// first: a build is a child process and dies with dockmaster.

// busyBuilding is what a project's row says while a compose build runs.
const busyBuilding = "building"

// actQuitApp is the confirmed quit, asked while a build runs.
const actQuitApp = "quit_app"

// maxKeptBuilds is how many finished builds are kept, with their output,
// beside the running ones; the oldest finished goes first.
const maxKeptBuilds = 20

// startBuild registers b, opens its output view and starts it. A form it
// was submitted from closes first, so esc goes back to where the user was.
func (a *App) startBuild(b *views.Build) tea.Cmd {
	switch a.view {
	case style.ViewBuildForm, style.ViewScaleForm, style.ViewComposeBuild:
		a.popView()
	}
	b.Context = a.contextName()
	a.builds = append(a.builds, b)
	a.pruneBuilds()
	if b.Project != "" {
		if pv := typedView[*views.ProjectsView](a, style.ViewProjects); pv != nil {
			pv.SetBusy(b.Project, busyBuilding)
		}
	}
	a.log.Info("build started", "build", b.Title, "context", b.Context)
	a.syncBuildsView()
	cmd := b.Start()
	a.attachBuild(b)
	a.flash = b.Title + "…"
	return cmd
}

// attachBuild opens the output view on b, showing everything it has
// printed so far. An output view already on screen is replaced rather than
// stacked, so esc goes back past both.
func (a *App) attachBuild(b *views.Build) {
	if a.view == style.ViewOutput {
		a.popView()
	}
	a.setView(style.ViewOutput, views.NewOutputView(b))
	a.pushView(style.ViewOutput)
}

// pruneBuilds drops the oldest finished builds past maxKeptBuilds. A
// running one is never dropped: its messages still need somewhere to go.
func (a *App) pruneBuilds() {
	finished := 0
	for _, b := range a.builds {
		if !b.Running() {
			finished++
		}
	}
	if finished <= maxKeptBuilds {
		return
	}
	kept := a.builds[:0]
	for _, b := range a.builds {
		if !b.Running() && finished > maxKeptBuilds {
			finished--
			continue
		}
		kept = append(kept, b)
	}
	a.builds = kept
}

// dropBuild forgets b; a view attached to it keeps what it showed.
func (a *App) dropBuild(b *views.Build) {
	for i, x := range a.builds {
		if x == b {
			a.builds = append(a.builds[:i], a.builds[i+1:]...)
			return
		}
	}
}

// buildByID finds a build by its ID, nil once it has been dropped.
func (a *App) buildByID(id int64) *views.Build {
	for _, b := range a.builds {
		if b.ID() == id {
			return b
		}
	}
	return nil
}

// runningBuilds are the builds still going, oldest first.
func (a *App) runningBuilds() []*views.Build {
	var out []*views.Build
	for _, b := range a.builds {
		if b.Running() && !b.Replay {
			out = append(out, b)
		}
	}
	return out
}

// projectBuild is the compose build running for a project on the context
// on screen, nil when none is.
func (a *App) projectBuild(project string) *views.Build {
	ctx := a.contextName()
	for _, b := range a.builds {
		if b.Running() && b.Project == project && b.Context == ctx {
			return b
		}
	}
	return nil
}

// markBuildsBusy marks every project with a compose build running on the
// context on screen. A context switch rebuilds the projects view, so it
// runs after one.
func (a *App) markBuildsBusy() {
	pv := typedView[*views.ProjectsView](a, style.ViewProjects)
	if pv == nil {
		return
	}
	for _, b := range a.runningBuilds() {
		if b.Project != "" && b.Context == a.contextName() {
			pv.SetBusy(b.Project, busyBuilding)
		}
	}
}

// handleOutputMsg folds a build's message into it, whichever view is
// showing — one dropped would end its drain and leave the command blocked
// on a full pipe — and hands the lines to the output view when that view
// is attached to the build.
func (a *App) handleOutputMsg(msg tea.Msg) tea.Cmd {
	var id int64
	switch m := msg.(type) {
	case views.OutputStartedMsg:
		id = m.Gen
	case views.OutputBatchMsg:
		id = m.Gen
	case views.OutputDoneMsg:
		id = m.Gen
	}
	b := a.buildByID(id)
	if b == nil {
		return nil
	}
	added, next, ended := b.Update(msg)
	if ov := typedView[*views.OutputView](a, style.ViewOutput); ov != nil && ov.Build() == b && len(added) > 0 {
		ov.Append(added)
	}
	if !ended {
		return next
	}
	return tea.Batch(next, a.finishBuild(b))
}

// finishBuild says how a build ended, in the flash and the log, clears its
// project's busy marker, and after a success runs what was asked to follow
// it — through handleAction like any other action, and only on the context
// it was built on.
func (a *App) finishBuild(b *views.Build) tea.Cmd {
	if b.Replay {
		a.dropBuild(b)
		return nil
	}
	defer a.syncBuildsView()
	if b.Project != "" && b.Context == a.contextName() {
		if pv := typedView[*views.ProjectsView](a, style.ViewProjects); pv != nil {
			pv.SetBusy(b.Project, "")
		}
	}
	a.pruneBuilds()
	err := b.Err()
	switch {
	case b.Canceled():
		a.flash = fmt.Sprintf("[%s] build stopped", b.Name)
		a.log.Info("build stopped", "build", b.Title)
		return nil
	case err != nil:
		a.errFlash = fmt.Sprintf("[%s] build failed", b.Name)
		if code := exitCode(err); code > 0 {
			a.errFlash += fmt.Sprintf(" (exit %d)", code)
		} else {
			a.errFlash += " — " + docker.FormatUserError(err).Error()
		}
		a.log.Warn("build failed", "build", b.Title, "exit", exitCode(err))
		a.loggedFlash = a.errFlash
		return nil
	}
	a.flash = fmt.Sprintf("[%s] build finished", b.Name)
	a.log.Info("build finished", "build", b.Title)
	if b.ThenAction == "" {
		return nil
	}
	if b.Context != a.contextName() {
		a.errFlash = fmt.Sprintf("[%s] build finished on %s — not running %s on %s",
			b.Name, b.Context, b.ThenAction, a.contextName())
		return nil
	}
	_, next := a.handleAction(b.ThenAction, b.ThenParam)
	return next
}

// buildByParam is the build an action's param names by ID.
func (a *App) buildByParam(param string) *views.Build {
	id, err := strconv.ParseInt(param, 10, 64)
	if err != nil {
		return nil
	}
	return a.buildByID(id)
}

// confirmStopBuild asks before stopping a running build: what it has
// done so far is lost.
func (a *App) confirmStopBuild(param string) {
	id, err := strconv.ParseInt(param, 10, 64)
	b := a.buildByID(id)
	if err != nil || b == nil || !b.Running() {
		a.errFlash = "that build is no longer running"
		return
	}
	a.openConfirm("stop_build", param, fmt.Sprintf("stop %s? its output so far is kept", b.Title))
}

// stopBuild stops a running build; its end arrives through its drain.
func (a *App) stopBuild(param string) {
	id, err := strconv.ParseInt(param, 10, 64)
	if b := a.buildByID(id); err == nil && b != nil && b.Running() {
		b.Stop()
		a.flash = "stopping " + b.Title + "…"
	}
}

// stopAllBuilds cancels every running build, and any log replay, at
// shutdown.
func (a *App) stopAllBuilds() {
	for _, b := range a.builds {
		b.Stop()
	}
}

// quit leaves dockmaster — after a confirm when a build is still running,
// since it dies with the app. force (:q!) skips the question; ctrl-c
// pressed again over the question answers it.
func (a *App) quit(force bool) tea.Cmd {
	running := len(a.runningBuilds())
	if running > 0 && !force && (!a.quitAsked || !a.confirm.Active()) {
		a.openConfirm(actQuitApp, "", fmt.Sprintf(
			"quit? %d build%s still running and stop%s with dockmaster",
			running, plural(running, " is", "s are"), plural(running, "s", ""),
		))
		a.quitAsked = true
		return nil
	}
	a.shutdown()
	return tea.Quit
}
