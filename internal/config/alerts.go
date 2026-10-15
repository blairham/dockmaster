// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"time"
)

// Alerts is the `alerts:` block (#109): what dockmaster flashes when a
// container turns unhealthy, is OOM-killed, dies with a non-zero exit or
// restart-loops, in whatever view is on screen.
//
//   - Enabled watches the daemon's events for them while dockmaster runs.
//   - Bell rings the terminal bell with each one as well.
//   - RestartLoop is how many restarts within how long is a loop.
type Alerts struct {
	RestartLoop RestartLoop `yaml:"restartLoop"`
	Enabled     bool        `yaml:"enabled"`
	Bell        bool        `yaml:"bell"`
}

// RestartLoop is the restart-loop threshold: Count crash restarts within
// Window. A restart someone asked for (docker restart, R) never counts.
type RestartLoop struct {
	Window time.Duration `yaml:"window"`
	Count  int           `yaml:"count"`
}

// Defaults for alerts.restartLoop: three crash restarts in five minutes.
const (
	DefaultRestartLoopCount  = 3
	DefaultRestartLoopWindow = 5 * time.Minute
)

// ContextAlerts is one context's alerts: entry, over the top-level one
// (#55's per-context settings): a key left out follows the top level.
// Under restartLoop, a count or window left out (zero) does too.
type ContextAlerts struct {
	Enabled     *bool        `yaml:"enabled"`
	Bell        *bool        `yaml:"bell"`
	RestartLoop *RestartLoop `yaml:"restartLoop"`
}

// With is a with context c's alerts entry laid over it; nil is a as is.
func (a Alerts) With(c *ContextAlerts) Alerts {
	if c == nil {
		return a
	}
	if c.Enabled != nil {
		a.Enabled = *c.Enabled
	}
	if c.Bell != nil {
		a.Bell = *c.Bell
	}
	if rl := c.RestartLoop; rl != nil {
		if rl.Count != 0 {
			a.RestartLoop.Count = rl.Count
		}
		if rl.Window != 0 {
			a.RestartLoop.Window = rl.Window
		}
	}
	return a
}

// validateRestartLoop checks a restartLoop block named where; with partial
// set, a zero count or window is one left out.
func validateRestartLoop(where string, rl RestartLoop, partial bool) []string {
	var errs []string
	if (!partial || rl.Count != 0) && rl.Count < 2 {
		errs = append(errs, fmt.Sprintf("%s.count must be at least 2 — one restart is not a loop, got %d", where, rl.Count))
	}
	if (!partial || rl.Window != 0) && rl.Window < time.Second {
		errs = append(errs, fmt.Sprintf("%s.window must be at least 1s, got %v", where, rl.Window))
	}
	return errs
}
