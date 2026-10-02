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

// AliasesFileName is the aliases file, beside config.yaml — k9s's
// aliases.yaml.
const AliasesFileName = "aliases.yaml"

// AliasesPath is the aliases file's path.
func AliasesPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, AliasesFileName), nil
}

// LoadAliases reads the aliases file: each name under `aliases:` maps to
// the `:` command it stands for, as k9s's does —
//
//	aliases:
//	  pg: containers /postgres
//
// No file is no aliases. An unknown key is an error, like config.yaml's;
// whether a command makes sense is the UI's to say.
func LoadAliases() (map[string]string, error) {
	path, err := AliasesPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path) //nolint:gosec // the user's own config directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // no file: no aliases
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only
	var file struct {
		Aliases map[string]string `yaml:"aliases"`
	}
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file.Aliases, nil
}
