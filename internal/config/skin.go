package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/blairham/tuikit/theme"
	"go.yaml.in/yaml/v3"
)

// EnvSkin names a skin, over ui.skin — k9s's K9S_SKIN.
const EnvSkin = "DOCKMASTER_SKIN"

// SkinsDirName is the skins directory, inside the config directory.
const SkinsDirName = "skins"

// SkinName is the skin to load: $DOCKMASTER_SKIN, else ui.skin. "" is
// none.
func (c Config) SkinName() string {
	if s := os.Getenv(EnvSkin); s != "" {
		return s
	}
	return c.UI.Skin
}

// LoadSkin reads the skin called name from the skins directory —
// <name>.yaml or <name>.yml — and returns it with its path. A skin file is
// k9s's: its colors under a top-level "k9s:" key, so a stock k9s skin
// works unchanged. A file with no colors under "k9s:" is an error rather
// than a skin that silently changes nothing.
func LoadSkin(name string) (theme.Skin, string, error) {
	dir, err := Dir()
	if err != nil {
		return theme.Skin{}, "", err
	}
	dir = filepath.Join(dir, SkinsDirName)
	for _, ext := range []string{".yaml", ".yml"} {
		path := filepath.Join(dir, name+ext)
		b, rerr := os.ReadFile(path) //nolint:gosec // the user's own skins directory
		if errors.Is(rerr, os.ErrNotExist) {
			continue
		}
		if rerr != nil {
			return theme.Skin{}, path, fmt.Errorf("reading skin %s: %w", path, rerr)
		}
		var file struct {
			K9s theme.Skin `yaml:"k9s"`
		}
		if uerr := yaml.Unmarshal(b, &file); uerr != nil {
			return theme.Skin{}, path, fmt.Errorf("skin %s: %w", path, uerr)
		}
		if reflect.ValueOf(file.K9s).IsZero() {
			return theme.Skin{}, path, fmt.Errorf("skin %s sets no colors under a top-level k9s: key", path)
		}
		return file.K9s, path, nil
	}
	return theme.Skin{}, "", fmt.Errorf("no skin named %q in %s", name, dir)
}

// Theme is base with the configured skin over it, inverted when asked.
func (c Config) Theme(base theme.Theme) (theme.Theme, error) {
	t := base
	if name := c.SkinName(); name != "" {
		s, path, err := LoadSkin(name)
		if err != nil {
			return base, err
		}
		if t, err = t.WithSkin(s); err != nil {
			return base, fmt.Errorf("skin %s: %w", path, err)
		}
	}
	if c.UI.Invert {
		t = t.Inverted()
	}
	return t, nil
}
