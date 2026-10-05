// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"go.yaml.in/yaml/v3"
)

// PluginsFileName is the plugins file, beside config.yaml — k9s's
// plugins.yaml.
const PluginsFileName = "plugins.yaml"

// Plugin is one entry under plugins:, in k9s's shape. Whether its inputs
// and pipes make sense is the UI's to say (tui.Plugins). OverwriteOutput
// has no effect — dockmaster does not capture a plugin's output.
type Plugin struct {
	Confirm         *bool         `yaml:"confirm"`
	ShortCut        string        `yaml:"shortCut"`
	Description     string        `yaml:"description"`
	Command         string        `yaml:"command"`
	Scopes          []string      `yaml:"scopes"`
	Args            []string      `yaml:"args"`
	Pipes           []string      `yaml:"pipes"`
	Inputs          []PluginInput `yaml:"inputs"`
	Background      bool          `yaml:"background"`
	Dangerous       bool          `yaml:"dangerous"`
	Override        bool          `yaml:"override"`
	OverwriteOutput bool          `yaml:"overwriteOutput"`
}

// PluginInput is one field of the form a plugin with inputs shows before
// it runs, in k9s's shape: Type is string, number, bool or dropdown, and a
// dropdown picks from Options.
type PluginInput struct {
	Name     string   `yaml:"name"`
	Label    string   `yaml:"label"`
	Type     string   `yaml:"type"`
	Default  string   `yaml:"default"`
	Options  []string `yaml:"options"`
	Required bool     `yaml:"required"`
}

// PluginsPath is the plugins file's path.
func PluginsPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, PluginsFileName), nil
}

// PluginsDirName is the directory of plugin snippet files beside
// config.yaml — k9s's plugins/ (v0.40.9).
const PluginsDirName = "plugins"

// PluginsDirPath is the plugin snippets directory's path.
func PluginsDirPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, PluginsDirName), nil
}

// LoadPlugins reads the plugins file, as k9s's:
//
//	plugins:
//	  dive:
//	    shortCut: Shift-D
//	    description: Dive image
//	    scopes: [images]
//	    command: dive
//	    args: [$IMAGE]
//
// and then every *.yaml (or *.yml) in plugins/ beside it, in name order,
// each in the same shape, merged into one set. A name defined in two files
// is an error naming both. No file is no plugins. An unknown key is an
// error; whether a plugin makes sense is the UI's to say.
func LoadPlugins() (map[string]Plugin, error) {
	path, err := PluginsPath()
	if err != nil {
		return nil, err
	}
	dir, err := PluginsDirPath()
	if err != nil {
		return nil, err
	}
	snippets, err := pluginSnippets(dir)
	if err != nil {
		return nil, err
	}
	var (
		out  map[string]Plugin
		from = map[string]string{}
	)
	for _, p := range append([]string{path}, snippets...) {
		entries, err := readPluginsFile(p)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(entries))
		for name := range entries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if prev, ok := from[name]; ok {
				return nil, fmt.Errorf("plugin %q is defined twice: in %s and in %s", name, prev, p)
			}
			if out == nil {
				out = map[string]Plugin{}
			}
			from[name] = p
			out[name] = entries[name]
		}
	}
	return out, nil
}

// pluginSnippets lists the plugin files in dir, in name order: regular
// files ending .yaml or .yml, not those in subdirectories. No directory is
// no snippets.
func pluginSnippets(dir string) ([]string, error) {
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var out []string
	for _, de := range des {
		ext := filepath.Ext(de.Name())
		if de.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		out = append(out, filepath.Join(dir, de.Name()))
	}
	sort.Strings(out)
	return out, nil
}

// readPluginsFile decodes one file of plugins: entries, strictly. A
// missing file has none.
func readPluginsFile(path string) (map[string]Plugin, error) {
	f, err := os.Open(path) //nolint:gosec // the user's own config directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // no file: no plugins
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only
	var file struct {
		Plugins map[string]Plugin `yaml:"plugins"`
	}
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file.Plugins, nil
}
