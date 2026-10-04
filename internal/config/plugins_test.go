// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestLoadPluginsInputsAndPipes: k9s's inputs and pipes decode into their
// shapes, and an unknown key inside an input is an error like any other.
func TestLoadPluginsInputsAndPipes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, PluginsFileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`plugins:
  grep:
    shortCut: Shift-G
    scopes: [containers]
    command: docker
    args: [logs, $NAME]
    pipes: ["grep -i error", "tail -n 20"]
    inputs:
      - name: pattern
        label: Pattern
        type: string
        required: true
      - name: level
        type: dropdown
        options: [info, warn]
        default: warn
`)
	ps, err := LoadPlugins()
	if err != nil {
		t.Fatal(err)
	}
	p := ps["grep"]
	if want := []string{"grep -i error", "tail -n 20"}; !reflect.DeepEqual(p.Pipes, want) {
		t.Errorf("pipes %q", p.Pipes)
	}
	want := []PluginInput{
		{Name: "pattern", Label: "Pattern", Type: "string", Required: true},
		{Name: "level", Type: "dropdown", Options: []string{"info", "warn"}, Default: "warn"},
	}
	if !reflect.DeepEqual(p.Inputs, want) {
		t.Errorf("inputs %+v, want %+v", p.Inputs, want)
	}
	write("plugins:\n  p:\n    inputs:\n      - name: a\n        choices: [x]\n")
	if _, err := LoadPlugins(); err == nil || !strings.Contains(err.Error(), "choices") {
		t.Errorf("an unknown input key was accepted: %v", err)
	}
}
