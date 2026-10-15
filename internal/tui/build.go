// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// The flashes for a copy or save with nothing on screen.
const (
	flashNothingToCopy = "nothing to copy"
	flashNothingToSave = "nothing to save"
)

// cmdBuild is the :build command.
const cmdBuild = "build"

// errNoDockerForStream is a streamed command's start failing for want of
// the docker CLI.
var errNoDockerForStream = errors.New("this needs the `docker` CLI on PATH")

// buildCommand is :build [path] (#104): the build form on a directory, or
// on a Dockerfile, whose directory is then the context. No path is the
// directory dockmaster was started in; ~ is the home directory, as in :dir.
//
// With no path while a build is running, it reattaches to the newest
// running build instead (#121): leaving a build's output leaves it running,
// and this is the way back to it.
func (a *App) buildCommand(arg string) (string, tea.Cmd) {
	if arg == "" {
		if b := a.latestRunningBuild(); b != nil {
			a.attachBuild(b)
			return "", nil
		}
	}
	path := arg
	if path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "no working directory: " + err.Error(), nil
		}
		path = wd
	}
	path = views.ResolvePath(path)
	st, err := os.Stat(path)
	if err != nil {
		return "not a directory or a Dockerfile: " + path, nil
	}
	dir, file := path, ""
	if !st.IsDir() {
		dir, file = filepath.Dir(path), filepath.Base(path)
	}
	_, cmd := a.handleAction("build_form", dir+"\x00"+file)
	return "", cmd
}

// openBuildForm opens the build form; param is the context directory, NUL,
// and the Dockerfile in it ("" for its own Dockerfile).
func (a *App) openBuildForm(param string) {
	dir, file, _ := strings.Cut(param, "\x00")
	a.setView(style.ViewBuildForm, views.NewBuildForm(dir, file))
	a.pushView(style.ViewBuildForm)
}

// runBuild runs a submitted build into an output view. The spec is checked
// again: the form checked it, but a directory can go away in between.
func (a *App) runBuild(spec docker.BuildSpec) tea.Cmd {
	if err := spec.Check(); err != nil {
		if f := typedView[*views.BuildFormView](a, style.ViewBuildForm); f != nil && a.view == style.ViewBuildForm {
			f.SetError(err.Error())
		} else {
			a.errFlash = "build: " + err.Error()
		}
		return nil
	}
	endpoint := a.client.EndpointArgs()
	detect := a.builderFn
	if detect == nil {
		detect = docker.DetectBuilder
	}
	start := a.streamCLI(func(ctx context.Context, bin string) docker.CommandSpec {
		return docker.BuildCommand(bin, endpoint, detect(ctx, bin), spec)
	})
	return a.startBuild(views.NewBuild("build "+spec.BuildTitle(), spec.BuildTitle(), start))
}

// streamCLI is the start of a streamed docker CLI command: argv builds the
// command from the CLI's path. It runs in the command's goroutine, so it
// must not read the App; take what it needs before.
func (a *App) streamCLI(argv func(ctx context.Context, bin string) docker.CommandSpec) views.OutputStartFunc {
	stream := a.streamFn
	fake := stream != nil
	if !fake {
		stream = docker.StreamCommand
	}
	return func(ctx context.Context) (*docker.CommandStream, error) {
		bin := "docker"
		if !fake {
			p, err := exec.LookPath("docker")
			if err != nil {
				return nil, errNoDockerForStream
			}
			bin = p
		}
		return stream(ctx, argv(ctx, bin))
	}
}

// outputAction carries out the output view's own keys: autoscroll, wrap,
// copy and save. None of them touches the command.
func (a *App) outputAction(action string) tea.Cmd {
	ov := typedView[*views.OutputView](a, style.ViewOutput)
	if ov == nil {
		return nil
	}
	switch action {
	case "output_follow":
		ov.ToggleFollow()
		a.flash = "autoscroll " + onOff(ov.Follow())
	case "output_wrap":
		ov.ToggleWrap()
		a.flash = "wrap " + onOff(ov.Wrap())
	case "output_copy":
		text, n := ov.PlainText()
		if n == 0 {
			a.errFlash = flashNothingToCopy
			return nil
		}
		a.flash = fmt.Sprintf("copied %d lines", n)
		return tea.SetClipboard(text)
	case "output_save":
		text, n := ov.PlainText()
		if n == 0 {
			a.errFlash = flashNothingToSave
			return nil
		}
		path, err := a.saveDump("logs", ov.Title(), text)
		if err != nil {
			a.errFlash = "saving " + ov.Title() + ": " + err.Error()
			return nil
		}
		a.flash = fmt.Sprintf("saved %d lines to %s", n, path)
	}
	return nil
}
