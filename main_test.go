// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui"
)

// TestBehaviorOptions: each k9s behavior key reaches the app, the right way
// round — enableMouse is the one stated positively in config and
// negatively in the options.
func TestBehaviorOptions(t *testing.T) {
	var o tui.Options
	behaviorOptions(config.Default(), &o)
	if o.NoMouse || o.NoExitOnCtrlC || o.LogWrap || o.LogPaused || o.LogFullscreen || o.DumpDir != "" {
		t.Errorf("defaults changed behavior: %+v", o)
	}

	cfg := config.Default()
	cfg.UI.EnableMouse = false
	cfg.NoExitOnCtrlC = true
	cfg.Logger.TextWrap, cfg.Logger.DisableAutoscroll, cfg.UI.DefaultsToFullScreen = true, true, true
	cfg.ScreenDumpDir = "~/dumps"
	behaviorOptions(cfg, &o)
	home, _ := os.UserHomeDir()
	if !o.NoMouse || !o.NoExitOnCtrlC || !o.LogWrap || !o.LogPaused || !o.LogFullscreen ||
		o.DumpDir != filepath.Join(home, "dumps") {
		t.Errorf("set keys did not all arrive: %+v", o)
	}
}
