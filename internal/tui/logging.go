// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"os/exec"
)

// What reaches the log file (#60), and what deliberately does not: error
// flashes, plugin and hotkey runs, context switches — never a plugin's
// arguments, environment or output, nor the values typed into its inputs
// form, only their names. A plugin's arguments are expanded from those
// values and the row's, and its output may echo them.

// logFlash writes an error flash to the log once: Update calls it after
// every message, so no path that sets errFlash can miss it, and a flash
// still showing on the next message is not written again.
func (a *App) logFlash() {
	if a.errFlash == "" || a.errFlash == a.loggedFlash {
		return
	}
	a.loggedFlash = a.errFlash
	a.log.Error("flash", "error", a.errFlash, "view", a.viewName())
}

// logContextSwitch records a switch to another docker context. A failed
// one's error is the flash, logged by logFlash.
func (a *App) logContextSwitch(msg switchContextMsg) {
	from := ""
	if a.client != nil {
		from = a.client.ContextName
	}
	if msg.err != nil {
		a.log.Warn("context switch failed", "from", from, "to", msg.name)
		return
	}
	host := ""
	if msg.client != nil {
		host = msg.client.Host
	}
	a.log.Info("context switch", "from", from, "to", msg.name, "endpoint", host)
}

// logPluginRun records a plugin starting: its name, command and the
// names of its inputs — never the arguments, which are expanded from the
// inputs' values, nor its environment.
func (a *App) logPluginRun(p Plugin) {
	attrs := []any{"plugin", p.Name, "command", p.Command, "background", p.Background}
	if len(p.Pipes) > 0 {
		attrs = append(attrs, "pipes", len(p.Pipes))
	}
	if len(p.Inputs) > 0 {
		names := make([]string, 0, len(p.Inputs))
		for _, in := range p.Inputs {
			names = append(names, in.Name)
		}
		attrs = append(attrs, "inputs", names)
	}
	a.log.Info("plugin run", attrs...)
}

// logPluginDone records a plugin's end: a background run's exit status, a
// failure's reason. reason is the status alone, never the output the
// flash adds to it; the caller marks that flash logged.
func (a *App) logPluginDone(msg pluginDoneMsg, reason string) {
	attrs := []any{"plugin", msg.name}
	if msg.background {
		attrs = append(attrs, "exit", exitCode(msg.err))
	}
	if msg.err != nil {
		a.log.Warn("plugin failed", append(attrs, "reason", reason)...)
		return
	}
	a.log.Info("plugin done", attrs...)
}

// exitCode is a command's exit status: 0 for success, -1 when it did not
// exit on its own (not found, killed, timed out).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// logHotKey records a hotkey run: its key and the command it types.
func (a *App) logHotKey(hk HotKey) {
	a.log.Info("hotkey", "key", hk.Label, "command", hk.Command)
}
