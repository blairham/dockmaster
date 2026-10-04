// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadHotKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	if hk, err := LoadHotKeys(); err != nil || hk != nil {
		t.Fatalf("no file: %v %v", hk, err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, HotKeysFileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(
		"hotKeys:\n  fwd:\n    shortCut: Shift-0\n    description: Port forwards\n    command: pf\n    keepHistory: true\n",
	)
	if hk, err := LoadHotKeys(); err != nil || hk["fwd"].ShortCut != "Shift-0" || hk["fwd"].Command != "pf" {
		t.Errorf("file: %+v %v", hk, err)
	}
	// k9s's override decodes (#58): before it, a k9s file using it failed
	// to load at all.
	write("hotKeys:\n  fwd:\n    shortCut: Shift-0\n    command: pf\n    override: true\n")
	if hk, err := LoadHotKeys(); err != nil || !hk["fwd"].Override {
		t.Errorf("override: %+v %v", hk, err)
	}
	write("hotKeys:\n  fwd:\n    shortcut: Shift-0\n")
	if _, err := LoadHotKeys(); err == nil || !strings.Contains(err.Error(), "shortcut") {
		t.Errorf("a misspelled key was accepted: %v", err)
	}
}
