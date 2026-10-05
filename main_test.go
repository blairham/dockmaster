// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
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
	cfg.HostShell.Image = "registry.local/tools:1"
	behaviorOptions(cfg, &o)
	home, _ := os.UserHomeDir()
	if !o.NoMouse || !o.NoExitOnCtrlC || !o.LogWrap || !o.LogPaused || !o.LogFullscreen ||
		o.DumpDir != filepath.Join(home, "dumps") || o.Shell != "zsh" ||
		o.HostShellImage != "registry.local/tools:1" {
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

// TestLoadSettingsContexts: the contexts: block reaches the app's options
// with each skin loaded, a missing skin or bad defaultView stops startup
// naming the context, DOCKMASTER_SKIN beats a context's skin, and only a
// --readonly given on the command line forces read-only everywhere.
func TestLoadSettingsContexts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKMASTER_CONFIG_DIR", dir)
	t.Setenv("DOCKMASTER_SKIN", "")
	if err := os.MkdirAll(filepath.Join(dir, "skins"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skins", "red.yaml"),
		[]byte("k9s:\n  frame:\n    menu:\n      keyColor: red\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	writeCfg := func(body string) {
		t.Helper()
		if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCfg(
		"dockmaster:\n  contexts:\n    prod:\n      skin: red\n      readOnly: false\n      defaultView: images\n    dev: {}\n",
	)

	st, err := loadSettings(cfgPath, nil, config.FlagValues{})
	if err != nil {
		t.Fatal(err)
	}
	o := settingsOptions(st)
	prod, dev := o.Contexts["prod"], o.Contexts["dev"]
	if prod.Theme == nil || prod.Theme.MenuKey == style.DefaultBase().MenuKey || prod.ReadOnly == nil ||
		*prod.ReadOnly || prod.DefaultView != "images" {
		t.Errorf("prod did not arrive whole: %+v", prod)
	}
	if dev.Theme != nil || dev.ReadOnly != nil || len(o.Contexts) != 2 {
		t.Errorf("dev: %+v (contexts %v)", dev, o.Contexts)
	}
	if o.ForceReadOnly {
		t.Error("forced read-only with no --readonly")
	}

	if st, err = loadSettings(cfgPath, map[string]bool{"readonly": true}, config.FlagValues{ReadOnly: true}); err != nil ||
		!settingsOptions(st).ForceReadOnly {
		t.Errorf("--readonly did not force read-only: %v", err)
	}
	if st, err = loadSettings(cfgPath, map[string]bool{"readonly": true}, config.FlagValues{}); err != nil ||
		settingsOptions(st).ForceReadOnly {
		t.Errorf("--readonly=false forced read-only: %v", err)
	}

	t.Setenv("DOCKMASTER_SKIN", "red")
	if st, err = loadSettings(
		cfgPath,
		nil,
		config.FlagValues{},
	); err != nil ||
		settingsOptions(st).Contexts["prod"].Theme != nil {
		t.Errorf("DOCKMASTER_SKIN did not beat prod's skin: %v", err)
	}
	t.Setenv("DOCKMASTER_SKIN", "")

	for body, want := range map[string]string{
		"dockmaster:\n  contexts:\n    prod:\n      skin: blue\n":               "contexts.prod.skin",
		"dockmaster:\n  contexts:\n    prod:\n      defaultView: bogus\n":       "contexts.prod.defaultView",
		"dockmaster:\n  contexts:\n    prod:\n      defaultView: images @dev\n": "contexts.prod.defaultView",
	} {
		writeCfg(body)
		if _, err := loadSettings(cfgPath, nil, config.FlagValues{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want an error naming %s", body, err, want)
		}
	}
}
