// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/engines"
)

// A shell on the daemon's host (#59), k9s's node shell: a privileged
// helper in the host's PID namespace nsenters PID 1 and runs the host's sh
// (docker.HostShellArgs). It is root on the machine the daemon runs on —
// the VM under Docker Desktop, OrbStack or Rancher Desktop, the server
// behind a remote context — so it is in mutating (readonly refuses it),
// always confirms, and names the daemon and the image in the confirm.
//
// Two ways in: :hostshell, for the daemon on screen, which covers remote
// contexts and a plain dockerd; and s on a single-engine runtime row, where
// s is "a shell on this machine" and those runtimes have no ssh of their
// own. Colima and Podman rows keep their ssh.

// hostShellImageName is the helper image: config's, else the default.
func (a *App) hostShellImageName() string {
	if a.hostShellImage != "" {
		return a.hostShellImage
	}
	return config.DefaultHostShellImage
}

// hostShellQuestion is the confirm for a root shell on label's host.
func hostShellQuestion(label, image string) string {
	return fmt.Sprintf("open a ROOT shell on the host of %s? a privileged %s helper (pulled if missing) "+
		"enters the host's namespaces; every command runs as root there", label, image)
}

// daemonLabel names the daemon on screen: its context and endpoint.
func daemonLabel(c *docker.Client) string {
	if c.ContextName != "" && c.ContextName != c.Host {
		return c.ContextName + " (" + c.Host + ")"
	}
	return c.Host
}

// confirmHostShell is :hostshell: a root shell on the host of the daemon
// dockmaster is showing, through the docker CLI with its endpoint flags.
func (a *App) confirmHostShell() {
	switch {
	case a.client == nil:
		a.errFlash = "no daemon to open a host shell on"
		return
	case a.client.Rootless:
		a.errFlash = daemonLabel(a.client) + " runs rootless — a container there cannot enter its host's " +
			"namespaces; for a Podman machine, s in the runtimes view opens a shell on the VM"
		return
	}
	a.openHostShellConfirm(daemonLabel(a.client), a.client.EndpointArgs())
}

// runtimeHostShell is s on a single-engine runtime row: a root shell on
// the VM under that runtime's daemon, reached at the endpoint the runtime
// serves, whichever daemon dockmaster is showing.
func (a *App) runtimeHostShell(p engines.Provider, m engines.Machine) {
	switch {
	case !m.Running:
		a.errFlash = m.Name + " is stopped — <u> starts it"
		return
	case m.Host == "":
		a.errFlash = "no docker endpoint known for " + m.Name
		return
	}
	a.openHostShellConfirm(runtimeLabel(p, m.Name), []string{"--host", m.Host})
}

// openHostShellConfirm parks the host shell behind the y/n bar. The image
// and the endpoint are fixed now, so a reload while the bar is up cannot
// change what yes runs from what the question named.
func (a *App) openHostShellConfirm(label string, endpoint []string) {
	image := a.hostShellImageName()
	if err := docker.ValidHostShellImage(image); err != nil {
		a.errFlash = err.Error()
		return
	}
	a.openConfirm("host_shell", strings.Join(append([]string{image}, endpoint...), "\x00"),
		hostShellQuestion(label, image))
}

// hostShell runs the confirmed helper in the foreground: docker, the
// endpoint flags, then HostShellArgs, every one a separate argument that no
// shell parses.
func (a *App) hostShell(param string) tea.Cmd {
	if a.readonly {
		// A reload can turn readonly on while the confirm is up.
		a.errFlash = "readonly mode — host shell refused"
		return nil
	}
	parts := strings.Split(param, "\x00")
	argv, err := docker.HostShellArgs(parts[0])
	if err != nil {
		a.errFlash = err.Error()
		return nil
	}
	bin, err := exec.LookPath("docker")
	if err != nil {
		a.errFlash = "a host shell needs the `docker` CLI on PATH"
		return nil
	}
	args := append(parts[1:], argv...)
	// context.Background(): the session is the user's, not a timeout's.
	cmd := exec.CommandContext(
		context.Background(),
		bin,
		args...,
	) //nolint:gosec // fixed argv; the image is one positional argument, refused if it is a flag
	return a.inTerminal(cmd, func(err error) tea.Msg {
		// Leaving a shell after a failed command exits non-zero; that is
		// not an error worth a flash.
		var exitErr *exec.ExitError
		if err != nil && !asExitError(err, &exitErr) {
			return execDoneMsg{err: fmt.Errorf("host shell: %w", err)}
		}
		return execDoneMsg{}
	})
}
