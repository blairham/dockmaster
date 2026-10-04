// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package config loads dockmaster's config.yaml, shaped after k9s's: one
// top-level `dockmaster:` key, with `ui:` and `logger:` blocks under it.
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
const EnvDir = "DOCKMASTER_CONFIG_DIR"

// FileName is the config file inside the config directory.
const FileName = "config.yaml"

// Defaults. The refresh rate is three seconds rather than k9s's two:
// `docker ps` against a VM-backed daemon is measured in whole seconds (see
// docs/design/daemon-latency.md).
const (
	DefaultRefreshRate = 3
	DefaultLogTail     = 500
	// DefaultLogBuffer is k9s's logger.buffer: the lines a log view keeps.
	DefaultLogBuffer = 5000
	// DefaultLogSince is k9s's logger.sinceSeconds default, -1: tail.
	DefaultLogSince = -1
	// MaxLogTail caps logger.tail: the backlog is fetched in one request
	// and held in memory.
	MaxLogTail = 100_000
)

// File is the on-disk shape.
type File struct {
	Dockmaster Config `yaml:"dockmaster"`
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
//   - LiveViewAutoRefresh: refresh inspect views on the tick, as k9s's
//     liveViewAutoRefresh does its describe views; they keep the reader's
//     scroll position and filter.
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

	LiveViewAutoRefresh bool `yaml:"liveViewAutoRefresh"`
	// NoExitOnCtrlC makes ctrl-c do nothing; :q still quits (k9s's
	// noExitOnCtrlC).
	NoExitOnCtrlC bool `yaml:"noExitOnCtrlC"`
	// ScreenDumpDir is where ctrl-s saves go, and what :sd lists, instead
	// of the state directory (k9s's screenDumpDir). ~ is the home directory.
	ScreenDumpDir string `yaml:"screenDumpDir"`
	// Shell is what s opens in a container — zsh, ash, fish — when the
	// container has it; otherwise bash, then sh.
	Shell string `yaml:"shell"`
}

// UI is the header and chrome toggles, as k9s's `ui:` block.
type UI struct {
	// Skin names a skin file in SkinsDir, without its extension — k9s's
	// ui.skin. DOCKMASTER_SKIN overrides it.
	Skin       string `yaml:"skin"`
	Headless   bool   `yaml:"headless"`
	Logoless   bool   `yaml:"logoless"`
	Crumbsless bool   `yaml:"crumbsless"`
	Splashless bool   `yaml:"splashless"`
	// Invert turns the skin dark to light or light to dark (--invert).
	Invert bool `yaml:"invert"`
	// EnableMouse turns mouse reporting on: wheel scrolling, and the
	// terminal not selecting text on a plain drag. Off, a drag selects as
	// in any other program (k9s's ui.enableMouse).
	EnableMouse bool `yaml:"enableMouse"`
	// DefaultsToFullScreen opens log views fullscreen (k9s's
	// ui.defaultsToFullScreen).
	DefaultsToFullScreen bool `yaml:"defaultsToFullScreen"`
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
	// Buffer is how many lines a log view keeps; the oldest go first.
	Buffer int `yaml:"buffer"`
	// SinceSeconds is how far back a log view opens: -1 tails (the last
	// Tail lines), a positive number is that many seconds of log.
	SinceSeconds int `yaml:"sinceSeconds"`
	// ShowTime starts log views with timestamps on.
	ShowTime bool `yaml:"showTime"`
	// TextWrap starts log views with long lines wrapped.
	TextWrap bool `yaml:"textWrap"`
	// DisableAutoscroll starts log views paused at the end of the backlog
	// rather than following new lines.
	DisableAutoscroll bool `yaml:"disableAutoscroll"`
}

// Default is the configuration with no file.
func Default() Config {
	return Config{
		RefreshRate: DefaultRefreshRate,
		Logger:      Logger{Tail: DefaultLogTail, Buffer: DefaultLogBuffer, SinceSeconds: DefaultLogSince},
		UI:          UI{EnableMouse: true},
		Thresholds: Thresholds{
			CPU:    Threshold{Warn: 70, Critical: 90},
			Memory: Threshold{Warn: 70, Critical: 90},
		},
	}
}

// Dir is the config directory: $DOCKMASTER_CONFIG_DIR, else
// $XDG_CONFIG_HOME/dockmaster, else ~/.config/dockmaster.
func Dir() (string, error) {
	if d := os.Getenv(EnvDir); d != "" {
		return d, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "dockmaster"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating the config directory: %w", err)
	}
	return filepath.Join(home, ".config", "dockmaster"), nil
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
	file := File{Dockmaster: Default()}
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, errors.New(unknownField.ReplaceAllString(err.Error(), `unknown key "$1"`))
	}
	cfg := file.Dockmaster
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate rejects values dockmaster cannot run with.
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
	if strings.ContainsAny(c.UI.Skin, `/\`) {
		errs = append(errs, fmt.Sprintf("ui.skin is a skin's name in %s, not a path, got %q", SkinsDirName, c.UI.Skin))
	}
	if c.Logger.Buffer < c.Logger.Tail || c.Logger.Buffer > MaxLogTail {
		errs = append(errs, fmt.Sprintf("logger.buffer must be between logger.tail (%d) and %d, got %d",
			c.Logger.Tail, MaxLogTail, c.Logger.Buffer))
	}
	if c.Logger.SinceSeconds != -1 && c.Logger.SinceSeconds < 1 {
		errs = append(
			errs,
			fmt.Sprintf("logger.sinceSeconds is -1 (tail) or a number of seconds, got %d", c.Logger.SinceSeconds),
		)
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
// `dockmaster config init` and the docs.
const Sample = `# dockmaster config — see docs/design/config.md. Every key is optional; command-line
# flags override what is set here.
dockmaster:
  # Auto-refresh interval, in seconds (minimum 1) (-r).
  refreshRate: 3
  # How long one daemon request may take (--request-timeout), as 30s or 2m.
  # 0s keeps each request's own limit: 20s for a list, 5m for images.
  requestTimeout: 0s
  # Refuse every mutating action (--readonly).
  readOnly: false
  # Refresh inspect views on the tick, keeping their scroll and filter.
  liveViewAutoRefresh: false
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
  # ctrl-c does nothing; :q still quits.
  noExitOnCtrlC: false
  # Where ctrl-s saves go and :sd looks; empty is the state directory.
  screenDumpDir: ""
  # The shell s opens in a container when it has it; empty is bash, then sh.
  shell: ""
  ui:
    # A skin in skins/ beside this file, by name — a k9s skin works as is
    # (DOCKMASTER_SKIN overrides).
    skin: ""
    # Invert the skin, dark to light or light to dark (--invert).
    invert: false
    headless: false
    logoless: false
    crumbsless: false
    splashless: false
    # Mouse wheel scrolling. Off, a plain drag selects text as anywhere else.
    enableMouse: true
    # Open log views fullscreen.
    defaultsToFullScreen: false
  logger:
    # Lines of backlog a log view opens with.
    tail: 500
    # Lines a log view keeps; the oldest are dropped past it.
    buffer: 5000
    # How far back a log view opens: -1 tails, else that many seconds.
    sinceSeconds: -1
    # Start log views with timestamps on.
    showTime: false
    # Start log views with long lines wrapped.
    textWrap: false
    # Start log views paused rather than following new lines.
    disableAutoscroll: false
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

// StateDir is where dockmaster writes what it produces — saved logs:
// $XDG_STATE_HOME/dockmaster, else ~/.local/state/dockmaster, the XDG home
// k9s keeps its screen dumps under too.
func StateDir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "dockmaster"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating the state directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "dockmaster"), nil
}
