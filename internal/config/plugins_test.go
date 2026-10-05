// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

// TestLoadPluginsDirectory: every *.yaml and *.yml in plugins/ is read in
// plugins.yaml's shape and merged with it (k9s v0.40.9's snippet files);
// other files and subdirectories are not read, and plugins/ alone, with no
// plugins.yaml, is enough.
func TestLoadPluginsDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	snip := filepath.Join(dir, PluginsDirName)
	if err := os.MkdirAll(filepath.Join(snip, "nested.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	put := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entry := func(name string) string {
		return "plugins:\n  " + name + ":\n    shortCut: F2\n    scopes: [all]\n    command: " + name + "\n"
	}
	put(filepath.Join(snip, "dive.yaml"), entry("dive"))
	put(filepath.Join(snip, "ctop.yml"), entry("ctop"))
	put(filepath.Join(snip, "notes.txt"), "not: [yaml")
	put(filepath.Join(snip, "empty.yaml"), "")
	ps, err := LoadPlugins()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps["dive"].Command != "dive" || ps["ctop"].Command != "ctop" {
		t.Fatalf("snippets alone: %+v", ps)
	}
	put(filepath.Join(dir, PluginsFileName), entry("lazy"))
	ps, err = LoadPlugins()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(ps))
	for name := range ps {
		got = append(got, name)
	}
	slices.Sort(got)
	if want := []string{"ctop", "dive", "lazy"}; !reflect.DeepEqual(got, want) {
		t.Errorf("merged plugins %v, want %v", got, want)
	}
}

// TestLoadPluginsDirectoryConflicts: a name defined in two files is an
// error naming both — plugins.yaml and a snippet, or two snippets — and a
// bad key in a snippet names that snippet.
func TestLoadPluginsDirectoryConflicts(t *testing.T) {
	entry := "plugins:\n  dive:\n    shortCut: F2\n    scopes: [all]\n    command: dive\n"
	cases := []struct {
		files map[string]string
		want  []string
	}{
		{
			files: map[string]string{PluginsFileName: entry, "plugins/dive.yaml": entry},
			want:  []string{`plugin "dive" is defined twice`, PluginsFileName, filepath.Join("plugins", "dive.yaml")},
		},
		{
			files: map[string]string{"plugins/a.yaml": entry, "plugins/b.yml": entry},
			want: []string{
				`plugin "dive" is defined twice`, filepath.Join("plugins", "a.yaml"), filepath.Join("plugins", "b.yml"),
			},
		},
		{
			files: map[string]string{"plugins/bad.yaml": "plugins:\n  x:\n    shortcut: F2\n"},
			want:  []string{filepath.Join("plugins", "bad.yaml"), "shortcut"},
		},
		{
			// k9s's one-plugin-per-file shape is not this one: refused, not
			// read as nothing.
			files: map[string]string{"plugins/single.yaml": "shortCut: F2\ncommand: dive\n"},
			want:  []string{filepath.Join("plugins", "single.yaml"), "shortCut"},
		},
	}
	for i, tc := range cases {
		dir := t.TempDir()
		t.Setenv(EnvDir, dir)
		if err := os.MkdirAll(filepath.Join(dir, PluginsDirName), 0o700); err != nil {
			t.Fatal(err)
		}
		for name, body := range tc.files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		_, err := LoadPlugins()
		if err == nil {
			t.Errorf("case %d: loaded", i)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("case %d: error %q does not name %q", i, err, w)
			}
		}
	}
}
