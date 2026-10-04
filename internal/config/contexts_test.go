// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/theme"
)

// TestParseContexts: the contexts: block decodes by context name, and a
// readOnly left out stays unset rather than false.
func TestParseContexts(t *testing.T) {
	got, err := Parse(strings.NewReader(`dockmaster:
  readOnly: true
  contexts:
    prod:
      skin: red
      readOnly: true
      defaultView: images
    dev:
      readOnly: false
    colima: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	prod, dev, colima := got.Contexts["prod"], got.Contexts["dev"], got.Contexts["colima"]
	if len(got.Contexts) != 3 || prod.Skin != "red" || prod.DefaultView != "images" ||
		prod.ReadOnly == nil || !*prod.ReadOnly {
		t.Errorf("prod: %+v (contexts %v)", prod, got.Contexts)
	}
	if dev.ReadOnly == nil || *dev.ReadOnly {
		t.Errorf("dev's readOnly: false did not decode as set and false: %v", dev.ReadOnly)
	}
	if colima.ReadOnly != nil || colima.Skin != "" {
		t.Errorf("an empty entry is not empty: %+v", colima)
	}
}

// TestParseContextsRejects: an unknown key under a context is an error, as
// anywhere else in the file, and so are a skin path and a nameless entry —
// each naming where.
func TestParseContextsRejects(t *testing.T) {
	for in, want := range map[string]string{
		"dockmaster:\n  contexts:\n    prod:\n      skn: red\n":       `unknown key "skn"`,
		"dockmaster:\n  contexts:\n    prod:\n      refreshRate: 2\n": `unknown key "refreshRate"`,
		"dockmaster:\n  contexts:\n    prod:\n      skin: ../red\n":   "contexts.prod.skin",
		"dockmaster:\n  contexts:\n    \"\":\n      skin: red\n":      "no context name",
		"dockmaster:\n  contexts:\n    - prod\n":                      "cannot unmarshal",
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want an error naming %s", in, err, want)
		}
	}
}

// TestContextThemes: each context's skin loads over the base, inverted
// with ui.invert; a missing one is an error naming the context; with
// DOCKMASTER_SKIN set, no context's skin is used — but a missing one is
// still an error.
func TestContextThemes(t *testing.T) {
	skinDir(t, map[string]string{"pink.yml": pinkSkin})
	c := Default()
	c.Contexts = map[string]ContextSettings{"prod": {Skin: "pink"}, "dev": {}}
	got, err := c.ContextThemes(theme.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["dev"]; ok || len(got) != 1 {
		t.Errorf("a context with no skin got a theme: %v", got)
	}
	if got["prod"].MenuKey != lipgloss.Color("#FF69B4") {
		t.Errorf("prod's skin not applied: %v", got["prod"].MenuKey)
	}

	c.UI.Invert = true
	inv, err := c.ContextThemes(theme.Default())
	if err != nil {
		t.Fatal(err)
	}
	if inv["prod"].Bg == got["prod"].Bg {
		t.Error("--invert did not apply over the context's skin")
	}

	t.Setenv(EnvSkin, "pink")
	if env, err := c.ContextThemes(theme.Default()); err != nil || len(env) != 0 {
		t.Errorf("with DOCKMASTER_SKIN set a context's skin was still used: %v %v", env, err)
	}

	c.Contexts["prod"] = ContextSettings{Skin: "nope"}
	for _, env := range []string{"", "pink"} {
		t.Setenv(EnvSkin, env)
		_, err := c.ContextThemes(theme.Default())
		if err == nil || !strings.Contains(err.Error(), "contexts.prod.skin") || !strings.Contains(err.Error(), `"nope"`) {
			t.Errorf("DOCKMASTER_SKIN=%q: missing skin error %v, want one naming prod and the skin", env, err)
		}
	}
}
