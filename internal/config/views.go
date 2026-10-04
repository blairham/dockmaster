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

// ViewsFileName is the column layout file, beside config.yaml — k9s's
// views.yaml.
const ViewsFileName = "views.yaml"

// ViewColumns is one view's entry under views:, in k9s's shape: the
// columns to show, in order, and the column it opens sorted by, as
// COLUMN:asc or COLUMN:desc.
type ViewColumns struct {
	SortColumn string   `yaml:"sortColumn"`
	Columns    []string `yaml:"columns"`
}

// ViewsPath is the views file's path.
func ViewsPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, ViewsFileName), nil
}

// LoadViews reads the views file, as k9s's:
//
//	views:
//	  containers:
//	    columns: [NAME, STATE, IMAGE, AGE]
//	    sortColumn: AGE:desc
//
// No file is no layouts. An unknown key is an error; whether a view or a
// column exists is the UI's to say.
func LoadViews() (map[string]ViewColumns, error) {
	path, err := ViewsPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path) //nolint:gosec // the user's own config directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // no file: no layouts
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only
	var file struct {
		Views map[string]ViewColumns `yaml:"views"`
	}
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file.Views, nil
}
