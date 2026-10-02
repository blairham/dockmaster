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

	"go.yaml.in/yaml/v3"
)

// PluginsFileName is the plugins file, beside config.yaml — k9s's
// plugins.yaml.
const PluginsFileName = "plugins.yaml"

// Plugin is one entry under plugins:, in k9s's shape. Pipes and Inputs are
// decoded so a k9s file loads, and refused by the UI: dockmaster runs
// neither, and ignoring them would run a different command than the one
// written. OverwriteOutput has no effect — dockmaster does not capture a
// plugin's output.
type Plugin struct {
	Confirm         *bool    `yaml:"confirm"`
	ShortCut        string   `yaml:"shortCut"`
	Description     string   `yaml:"description"`
	Command         string   `yaml:"command"`
	Scopes          []string `yaml:"scopes"`
	Args            []string `yaml:"args"`
	Pipes           []string `yaml:"pipes"`
	Inputs          []any    `yaml:"inputs"`
	Background      bool     `yaml:"background"`
	Dangerous       bool     `yaml:"dangerous"`
	Override        bool     `yaml:"override"`
	OverwriteOutput bool     `yaml:"overwriteOutput"`
}

// PluginsPath is the plugins file's path.
func PluginsPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, PluginsFileName), nil
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
// No file is no plugins. An unknown key is an error; whether a plugin
// makes sense is the UI's to say.
func LoadPlugins() (map[string]Plugin, error) {
	path, err := PluginsPath()
	if err != nil {
		return nil, err
	}
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
