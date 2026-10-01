// Package config loads dockyard's config.yaml, shaped after k9s's: one
// top-level `dockyard:` key, with `ui:` and `logger:` blocks under it.
//
// Precedence is defaults, then the file, then any flag set on the command
// line — main applies flags over what Load returns. A missing file is not
// an error; an unknown key is, because a typo that is silently ignored
// reads exactly like a setting that took effect.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// EnvDir names the environment variable that overrides the config
// directory, as K9S_CONFIG_DIR does for k9s.
const EnvDir = "DOCKYARD_CONFIG_DIR"

// FileName is the config file inside the config directory.
const FileName = "config.yaml"

// Defaults. The refresh rate is three seconds rather than k9s's two:
// `docker ps` against a VM-backed daemon is measured in whole seconds (see
// docs/design/daemon-latency.md).
const (
	DefaultRefreshRate = 3
	DefaultLogTail     = 500
	// MaxLogTail caps logger.tail: the backlog is fetched in one request
	// and held in memory.
	MaxLogTail = 100_000
)

// File is the on-disk shape.
type File struct {
	Dockyard Config `yaml:"dockyard"`
}

// Config is everything config.yaml can set.
//
// Field comments sit in this block rather than on each field: the linter's
// fieldalignment fix reorders fields and drops their comments with them.
//
//   - DefaultView: the view to open on, by palette name, as -c.
//   - Context: the docker context to use when neither --host nor
//     --context is given.
//   - RequestTimeout: when non-zero, replaces every daemon request's own
//     deadline (20s for a list, 5m for images). A Go duration: 30s, 2m.
//   - RefreshRate: the auto-refresh interval in seconds.
//   - ShowAll: start with stopped containers listed (docker ps -a).
//   - NoStats: disable the CPU/MEM poll.
type Config struct {
	DefaultView    string        `yaml:"defaultView"`
	Context        string        `yaml:"context"`
	Logger         Logger        `yaml:"logger"`
	Thresholds     Thresholds    `yaml:"thresholds"`
	RequestTimeout time.Duration `yaml:"requestTimeout"`
	RefreshRate    int           `yaml:"refreshRate"`
	UI             UI            `yaml:"ui"`
	ReadOnly       bool          `yaml:"readOnly"`
	ShowAll        bool          `yaml:"showAll"`
	NoStats        bool          `yaml:"noStats"`
}

// UI is the header and chrome toggles, as k9s's `ui:` block.
type UI struct {
	Headless   bool `yaml:"headless"`
	Logoless   bool `yaml:"logoless"`
	Crumbsless bool `yaml:"crumbsless"`
	Splashless bool `yaml:"splashless"`
}

// Thresholds colour the containers view's CPU% and MEM columns, as k9s's
// `thresholds:` block: orange at warn, red at critical, both percentages.
// CPU is judged as a share of the CPUs the container can use, memory as a
// share of its limit (the host's memory when it has none).
type Thresholds struct {
	CPU    Threshold `yaml:"cpu"`
	Memory Threshold `yaml:"memory"`
}

// Threshold is one resource's warn and critical percentages.
type Threshold struct {
	Warn     int `yaml:"warn"`
	Critical int `yaml:"critical"`
}

// Logger is the logs view, as k9s's `logger:` block.
type Logger struct {
	// Tail is how many lines of backlog a log view opens with.
	Tail int `yaml:"tail"`
	// ShowTime starts log views with timestamps on.
	ShowTime bool `yaml:"showTime"`
}

// Default is the configuration with no file.
func Default() Config {
	return Config{
		RefreshRate: DefaultRefreshRate,
		Logger:      Logger{Tail: DefaultLogTail},
		Thresholds: Thresholds{
			CPU:    Threshold{Warn: 70, Critical: 90},
			Memory: Threshold{Warn: 70, Critical: 90},
		},
	}
}

// Dir is the config directory: $DOCKYARD_CONFIG_DIR, else
// $XDG_CONFIG_HOME/dockyard, else ~/.config/dockyard.
func Dir() (string, error) {
	if d := os.Getenv(EnvDir); d != "" {
		return d, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "dockyard"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating the config directory: %w", err)
	}
	return filepath.Join(home, ".config", "dockyard"), nil
}

// Path is the config file's path.
func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, FileName), nil
}

// Load reads the config file at path. A file that does not exist yields
// the defaults.
func Load(path string) (Config, error) {
	f, err := os.Open(path) //nolint:gosec // the path is the user's own config location
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only
	cfg, err := Parse(f)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// unknownField matches the decoder's complaint about a key the schema
// does not have, which names a Go type the user has never heard of.
var unknownField = regexp.MustCompile(`field (\S+) not found in type \S+`)

// Parse decodes a config file over the defaults and validates it.
func Parse(r io.Reader) (Config, error) {
	file := File{Dockyard: Default()}
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, errors.New(unknownField.ReplaceAllString(err.Error(), `unknown key "$1"`))
	}
	cfg := file.Dockyard
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate rejects values dockyard cannot run with.
func (c Config) Validate() error {
	var errs []string
	if c.RefreshRate < 1 {
		errs = append(errs, fmt.Sprintf("refreshRate must be at least 1 second, got %d", c.RefreshRate))
	}
	if c.RequestTimeout < 0 {
		errs = append(errs, fmt.Sprintf("requestTimeout cannot be negative, got %v", c.RequestTimeout))
	}
	for _, t := range []struct {
		name string
		t    Threshold
	}{{name: "cpu", t: c.Thresholds.CPU}, {name: "memory", t: c.Thresholds.Memory}} {
		if t.t.Warn < 1 || t.t.Critical > 100 || t.t.Warn >= t.t.Critical {
			errs = append(errs, fmt.Sprintf("thresholds.%s needs 1 <= warn < critical <= 100, got warn %d, critical %d",
				t.name, t.t.Warn, t.t.Critical))
		}
	}
	if c.Logger.Tail < 1 || c.Logger.Tail > MaxLogTail {
		errs = append(errs, fmt.Sprintf("logger.tail must be between 1 and %d, got %d", MaxLogTail, c.Logger.Tail))
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Sample is a commented config.yaml holding the defaults, for
// `dockyard config init` and the docs.
const Sample = `# dockyard config — see docs/design/config.md. Every key is optional; command-line
# flags override what is set here.
dockyard:
  # Auto-refresh interval, in seconds (minimum 1) (-r).
  refreshRate: 3
  # How long one daemon request may take (--request-timeout), as 30s or 2m.
  # 0s keeps each request's own limit: 20s for a list, 5m for images.
  requestTimeout: 0s
  # Refuse every mutating action (--readonly).
  readOnly: false
  # View to open on (-c): containers, images, volumes, networks, projects,
  # runtimes, events, ...
  defaultView: containers
  # Docker context to use when neither --host nor --context is given.
  # Empty follows DOCKER_HOST / DOCKER_CONTEXT / the CLI's current context.
  context: ""
  # Start with stopped containers listed (--all).
  showAll: false
  # Disable the CPU/MEM poll, one request per running container (--no-stats).
  noStats: false
  ui:
    headless: false
    logoless: false
    crumbsless: false
    splashless: false
  logger:
    # Lines of backlog a log view opens with.
    tail: 500
    # Start log views with timestamps on.
    showTime: false
  # CPU% and MEM turn orange at warn and red at critical (percent). CPU is
  # a share of the CPUs the container can use; memory of its limit, or of
  # the host's memory when it has none.
  thresholds:
    cpu:
      warn: 70
      critical: 90
    memory:
      warn: 70
      critical: 90
`
