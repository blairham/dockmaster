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

// JumpsFileName is the jumps file, beside config.yaml — k9s's jumps.yaml.
const JumpsFileName = "jumps.yaml"

// Jump is one entry under jumps:, keyed by the view whose enter it takes
// over: k9s's targetGVR is TargetView, and its labelSelector a -l filter
// over the target's labels. Filter is the / filter's other forms (a regex,
// or -f fuzzy), in place of k9s's fieldSelector. Whether it makes sense is
// the UI's to say (tui.Jumps).
type Jump struct {
	TargetView    string `yaml:"targetView"`
	LabelSelector string `yaml:"labelSelector"`
	Filter        string `yaml:"filter"`
}

// JumpsPath is the jumps file's path.
func JumpsPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, JumpsFileName), nil
}

// LoadJumps reads the jumps file:
//
//	jumps:
//	  volumes:
//	    targetView: containers
//	    labelSelector: com.docker.compose.project=$PROJECT
//
// No file is no jumps. An unknown key is an error.
func LoadJumps() (map[string]Jump, error) {
	path, err := JumpsPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path) //nolint:gosec // the user's own config directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // no file: no jumps
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only
	var file struct {
		Jumps map[string]Jump `yaml:"jumps"`
	}
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file.Jumps, nil
}
