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

// HotKeysFileName is the hotkeys file, beside config.yaml — k9s's
// hotkeys.yaml.
const HotKeysFileName = "hotkeys.yaml"

// HotKey is one entry under hotKeys:, in k9s's shape. KeepHistory is
// accepted for k9s's files and has no effect: every view switch is
// already in the history.
type HotKey struct {
	ShortCut    string `yaml:"shortCut"`
	Description string `yaml:"description"`
	Command     string `yaml:"command"`
	KeepHistory bool   `yaml:"keepHistory"`
}

// HotKeysPath is the hotkeys file's path.
func HotKeysPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, HotKeysFileName), nil
}

// LoadHotKeys reads the hotkeys file, as k9s's:
//
//	hotKeys:
//	  forwards:
//	    shortCut: Shift-0
//	    description: Port forwards
//	    command: pf
//
// No file is no hotkeys. An unknown key is an error; whether a shortcut or
// command makes sense is the UI's to say.
func LoadHotKeys() (map[string]HotKey, error) {
	path, err := HotKeysPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path) //nolint:gosec // the user's own config directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // no file: no hotkeys
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only
	var file struct {
		HotKeys map[string]HotKey `yaml:"hotKeys"`
	}
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file.HotKeys, nil
}
