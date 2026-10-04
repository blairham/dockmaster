// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// composeTimeout bounds a compose call. `up` pulls and builds what it has
// to; a cold one on a slow daemon runs for minutes.
const composeTimeout = 10 * time.Minute

// composeDoneMsg reports the outcome of a compose call.
type composeDoneMsg struct {
	err     error
	project string
	verb    string
}

// newCompose is the compose CLI handle for the daemon on screen. Tests set
// composeRunner to keep `docker` out of the loop.
func (a *App) newCompose() (*docker.Compose, error) {
	if a.composeRunner != nil {
		return docker.NewComposeWithRunner(a.composeRunner, hostOf(a.client)), nil
	}
	return docker.NewCompose(hostOf(a.client))
}

// composeProject looks a project up in the projects view's last listing.
func (a *App) composeProject(name string) (docker.Project, bool) {
	pv := typedView[*views.ProjectsView](a, style.ViewProjects)
	if pv == nil {
		return docker.Project{}, false
	}
	return pv.Project(name)
}

// composeRun marks a project busy and runs a compose verb against it in the
// background. busy is the present participle the row shows meanwhile.
func (a *App) composeRun(p docker.Project, busy, verb string,
	fn func(*docker.Compose, context.Context, docker.Project) error,
) tea.Cmd {
	c, err := a.newCompose()
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	if pv := typedView[*views.ProjectsView](a, style.ViewProjects); pv != nil {
		pv.SetBusy(p.Name, busy)
	}
	a.flash = fmt.Sprintf("%s %s…", busy, p.Name)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
		defer cancel()
		return composeDoneMsg{project: p.Name, verb: verb, err: fn(c, ctx, p)}
	}
}

func (a *App) handleComposeDone(msg composeDoneMsg) (tea.Model, tea.Cmd) {
	if pv := typedView[*views.ProjectsView](a, style.ViewProjects); pv != nil {
		pv.SetBusy(msg.project, "")
	}
	if msg.err != nil {
		a.errFlash = msg.err.Error()
	} else {
		a.flash = msg.verb + " " + msg.project
	}
	return a, a.refreshActiveView()
}

// composeUp brings a project up with `docker compose up -d`, which creates
// what is missing and recreates what changed — not just starting the
// containers that happen to exist. Without the compose files on this
// machine it falls back to starting those containers, and says so.
func (a *App) composeUp(name string) tea.Cmd {
	p, ok := a.composeProject(name)
	if !ok {
		return nil
	}
	if missing := p.MissingFile(); missing != "" {
		cmd := a.runProject(name, "started", func(ctx context.Context, id string) error {
			return a.client.StartContainer(ctx, id)
		})
		a.flash = "compose files unavailable (" + missing + ") — starting the existing containers"
		return cmd
	}
	return a.composeRun(p, "starting", "up", (*docker.Compose).Up)
}

func (a *App) composeRestart(name string) tea.Cmd {
	p, ok := a.composeProject(name)
	if !ok {
		return nil
	}
	if missing := p.MissingFile(); missing != "" {
		a.flash = "compose files unavailable (" + missing + ") — restarting the existing containers"
		return a.runProject(name, "restarted", func(ctx context.Context, id string) error {
			return a.client.RestartContainer(ctx, id, stopTimeout)
		})
	}
	return a.composeRun(p, "restarting", "restarted", (*docker.Compose).Restart)
}

func (a *App) composePull(name string) tea.Cmd {
	p, ok := a.composeProject(name)
	if !ok {
		return nil
	}
	if missing := p.MissingFile(); missing != "" {
		a.errFlash = "cannot pull " + name + ": " + missing
		return nil
	}
	return a.composeRun(p, "pulling", "pulled", (*docker.Compose).Pull)
}

// confirmComposeDown asks before `compose down`, or — without the compose
// files — before force-removing the containers one by one, which is all
// that is possible then.
func (a *App) confirmComposeDown(name string) {
	p, ok := a.composeProject(name)
	if !ok {
		return
	}
	if missing := p.MissingFile(); missing != "" {
		a.openConfirm("remove_project", name, fmt.Sprintf(
			"force-remove every container in %s? (compose files unavailable: %s)", name, missing,
		))
		return
	}
	a.openConfirm("compose_down", name, fmt.Sprintf(
		"compose down %s? its containers and networks are removed — volumes are kept", name,
	))
}

func (a *App) composeDown(name string) tea.Cmd {
	p, ok := a.composeProject(name)
	if !ok {
		return nil
	}
	return a.composeRun(p, "removing", "took down", (*docker.Compose).Down)
}

// composeEditedMsg reports the editor closing on a project's compose files.
type composeEditedMsg struct {
	// file, when set, is a compose file edited from :dir: a change brings
	// it up by the file, there being no project to look it up by yet.
	file    string
	err     error
	project string
	changed bool
}

// editorArgv is the user's editor as an argv: $VISUAL, then $EDITOR, then
// vi, split on spaces so a value like "code --wait" works.
func editorArgv() []string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(os.Getenv(env)); len(f) > 0 {
			return f
		}
	}
	return []string{"vi"}
}

// fileState fingerprints files' contents, so an edit that saves nothing
// new — or quits without saving — reads as no change. A file that cannot
// be read fingerprints as its error, which differs from any content.
func fileState(files []string) string {
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // compose files the project's labels record
		if err != nil {
			b = []byte("\x00" + err.Error())
		}
		h.Write([]byte(f + "\x00" + strconv.Itoa(len(b)) + "\x00"))
		h.Write(b)
	}
	return string(h.Sum(nil))
}

// inTerminal hands the terminal to cmd until it exits, as tea.ExecProcess
// does; tests swap it.
func (a *App) inTerminal(cmd *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
	if a.execProcess != nil {
		return a.execProcess(cmd, fn)
	}
	return tea.ExecProcess(cmd, fn)
}

// composeEdit opens a project's compose files in the user's editor and,
// when they come back changed, runs `compose up -d` so the edit takes
// effect — `kubectl edit` for a compose project. Files saved unchanged, or
// an editor that exits non-zero (vim's :cq), leave the project alone.
func (a *App) composeEdit(name string) tea.Cmd {
	p, ok := a.composeProject(name)
	if !ok {
		return nil
	}
	if missing := p.MissingFile(); missing != "" {
		a.errFlash = "cannot edit " + name + ": " + missing
		return nil
	}
	argv := editorArgv()
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		a.errFlash = "editor " + argv[0] + " is not on PATH — set $EDITOR"
		return nil
	}
	files := append([]string{}, p.ConfigFiles...)
	before := fileState(files)
	// context.Background(): the editing session is the user's, not a timeout's.
	cmd := exec.CommandContext(
		context.Background(),
		bin,
		append(argv[1:], files...)...,
	) //nolint:gosec // the user's own editor on the project's own files
	return a.inTerminal(cmd, func(err error) tea.Msg {
		return composeEditedMsg{project: name, err: err, changed: fileState(files) != before}
	})
}

func (a *App) handleComposeEdited(msg composeEditedMsg) (tea.Model, tea.Cmd) {
	// The editor had the terminal; take the screen back whatever happened.
	var exitErr *exec.ExitError
	switch {
	case errors.As(msg.err, &exitErr):
		a.errFlash = fmt.Sprintf("editor exited %d — %s left as is", exitErr.ExitCode(), msg.project)
	case msg.err != nil:
		a.errFlash = "editor: " + msg.err.Error()
	case !msg.changed:
		a.flash = "no changes — " + msg.project + " left as is"
	case msg.file != "":
		return a, tea.Batch(a.composeFileUp(msg.file), tea.ClearScreen)
	default:
		return a, tea.Batch(a.composeUp(msg.project), tea.ClearScreen)
	}
	return a, tea.Batch(a.refreshActiveView(), tea.ClearScreen)
}

// fileProject is the project a compose file describes, before any of its
// containers exist: run from the file's directory, named by compose itself.
func fileProject(file string) docker.Project {
	return docker.Project{WorkingDir: filepath.Dir(file), ConfigFiles: []string{file}}
}

// composeFileUp runs `docker compose -f file up -d` for a file found with
// :dir (#15), so a project can be started before it has any containers.
func (a *App) composeFileUp(file string) tea.Cmd {
	c, err := a.newCompose()
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	name := filepath.Base(filepath.Dir(file)) + "/" + filepath.Base(file)
	a.flash = "starting " + name + "…"
	p := fileProject(file)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
		defer cancel()
		return composeDoneMsg{project: name, verb: "up", err: c.Up(ctx, p)}
	}
}

// composeFileEdit opens a compose file found with :dir in the user's editor
// and, when it comes back changed, brings it up — composeEdit for a project
// that may not have run yet.
func (a *App) composeFileEdit(file string) tea.Cmd {
	argv := editorArgv()
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		a.errFlash = "editor " + argv[0] + " is not on PATH — set $EDITOR"
		return nil
	}
	before := fileState([]string{file})
	cmd := exec.CommandContext(
		context.Background(),
		bin,
		append(argv[1:], file)...) //nolint:gosec // the user's own editor on a file they chose
	return a.inTerminal(cmd, func(err error) tea.Msg {
		return composeEditedMsg{
			project: filepath.Base(file),
			file:    file,
			err:     err,
			changed: fileState([]string{file}) != before,
		}
	})
}
