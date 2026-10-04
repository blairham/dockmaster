// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui"
	"github.com/blairham/dockmaster/internal/tui/style"
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
	cfg.Shell = "zsh"
	behaviorOptions(cfg, &o)
	home, _ := os.UserHomeDir()
	if !o.NoMouse || !o.NoExitOnCtrlC || !o.LogWrap || !o.LogPaused || !o.LogFullscreen ||
		o.DumpDir != filepath.Join(home, "dumps") || o.Shell != "zsh" {
		t.Errorf("set keys did not all arrive: %+v", o)
	}
}

// TestLoadSettingsTwice: a reload reads exactly as startup does. An
// inverted skin loaded again after the first was applied comes out the
// same — not inverted twice — and a file startup would refuse is refused.
func TestLoadSettingsTwice(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKMASTER_CONFIG_DIR", dir)
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("dockmaster:\n  ui:\n    invert: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := style.Base()
	t.Cleanup(func() { style.SetBase(base) })

	first, err := loadSettings(cfgPath, nil, config.FlagValues{})
	if err != nil {
		t.Fatal(err)
	}
	style.SetBase(first.theme)
	second, err := loadSettings(cfgPath, nil, config.FlagValues{})
	if err != nil {
		t.Fatal(err)
	}
	if second.theme.Bg != first.theme.Bg || second.theme.Value != first.theme.Value {
		t.Error(
			"loading the inverted skin again inverted it back: a reload must lay the skin over the default, not over itself",
		)
	}

	if err := os.WriteFile(cfgPath, []byte("dockmaster:\n  bogus: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSettings(cfgPath, nil, config.FlagValues{}); err == nil {
		t.Error("a reload accepted a key startup refuses")
	}
}
