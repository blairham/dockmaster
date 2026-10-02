package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/theme"
)

// skinDir points the config directory at a temp dir and writes skins into
// its skins/.
func skinDir(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	t.Setenv(EnvSkin, "")
	if err := os.MkdirAll(filepath.Join(dir, SkinsDirName), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, SkinsDirName, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

const pinkSkin = "k9s:\n  frame:\n    menu:\n      keyColor: hotpink\n"

func TestSkinNamePrefersTheEnvironment(t *testing.T) {
	c := Default()
	c.UI.Skin = "file"
	t.Setenv(EnvSkin, "")
	if c.SkinName() != "file" {
		t.Errorf("ui.skin: %q", c.SkinName())
	}
	t.Setenv(EnvSkin, "env")
	if c.SkinName() != "env" {
		t.Errorf("DOCKMASTER_SKIN: %q", c.SkinName())
	}
}

func TestThemeLoadsTheSkin(t *testing.T) {
	skinDir(t, map[string]string{"pink.yml": pinkSkin})
	c := Default()
	c.UI.Skin = "pink"
	th, err := c.Theme(theme.Default())
	if err != nil {
		t.Fatal(err)
	}
	if th.MenuKey != lipgloss.Color("#FF69B4") || th.ShortcutKey.GetForeground() != lipgloss.Color("#FF69B4") {
		t.Errorf("menu key %v, shortcut key %v: skin not applied", th.MenuKey, th.ShortcutKey.GetForeground())
	}
}

func TestThemeInverts(t *testing.T) {
	skinDir(t, nil)
	c := Default()
	c.UI.Invert = true
	th, err := c.Theme(theme.Default())
	if err != nil {
		t.Fatal(err)
	}
	if th.Bg == theme.Default().Bg {
		t.Error("invert without a skin left the canvas as it was")
	}
}

func TestSkinErrors(t *testing.T) {
	skinDir(t, map[string]string{
		"empty.yaml": "dockmaster:\n  frame: {}\n",
		"bad.yaml":   "k9s:\n  frame:\n    menu:\n      keyColor: bluish\n",
	})
	for name, want := range map[string]string{
		"missing": `no skin named "missing"`,
		"empty":   "sets no colors under a top-level k9s: key",
		"bad":     "k9s.frame.menu.keyColor",
	} {
		c := Default()
		c.UI.Skin = name
		if _, err := c.Theme(theme.Default()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("skin %s: err %v, want %q", name, err, want)
		}
	}
	c := Default()
	c.UI.Skin = "../evil"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "not a path") {
		t.Errorf("a path as ui.skin: %v", err)
	}
}

func TestInvertFlag(t *testing.T) {
	c := ApplyFlags(Default(), map[string]bool{"invert": true}, FlagValues{Invert: true})
	if !c.UI.Invert {
		t.Error("--invert did not set ui.invert")
	}
}
