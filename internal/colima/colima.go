// Package colima drives the colima CLI: list profiles, start, stop,
// restart and delete them, and map a profile to the docker endpoint it
// serves.
//
// It shells out rather than talking to lima directly. colima's own state —
// which profiles exist, how a VM is provisioned, how its docker context is
// wired — lives in colima, and the CLI is the only stable interface to it.
//
// Like internal/docker, nothing here imports bubbletea.
package colima

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Runner executes the colima binary with args and returns its stdout. On a
// non-zero exit it returns an error carrying stderr. Swappable so tests
// never start a VM.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Client is a handle on the colima CLI.
type Client struct {
	run  Runner
	home string
}

// ErrNotInstalled is returned by New when colima is not on PATH.
var ErrNotInstalled = errors.New("colima is not on PATH")

// New finds the colima binary.
func New() (*Client, error) {
	bin, err := exec.LookPath("colima")
	if err != nil {
		return nil, ErrNotInstalled
	}
	return NewWithRunner(execRunner(bin), Home()), nil
}

// NewWithRunner builds a Client over an arbitrary runner and colima home.
func NewWithRunner(run Runner, home string) *Client {
	return &Client{run: run, home: home}
}

func execRunner(bin string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary; args are verbs and profile names
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("colima %s: %w", strings.Join(args, " "), ctx.Err())
			}
			return nil, &Error{Args: args, Stderr: stderr.String(), Err: err}
		}
		return stdout.Bytes(), nil
	}
}

// Error is a failed colima invocation.
type Error struct {
	Err    error
	Stderr string
	Args   []string
}

func (e *Error) Error() string {
	if msg := FatalMessage(e.Stderr); msg != "" {
		return "colima: " + msg
	}
	return fmt.Sprintf("colima %s: %v", strings.Join(e.Args, " "), e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// fatalMsg matches the msg="..." field of colima's logrus output.
var fatalMsg = regexp.MustCompile(`msg="((?:[^"\\]|\\.)*)"`)

// FatalMessage extracts the human message from colima's stderr: the msg of
// the last logrus line that has one. colima reports every failure as
//
//	time="..." level=fatal msg="colima [profile=x] is not running"
//
// and the timestamp and level are noise in a one-line flash.
func FatalMessage(stderr string) string {
	matches := fatalMsg.FindAllStringSubmatch(stderr, -1)
	if len(matches) == 0 {
		return strings.TrimSpace(lastLine(stderr))
	}
	raw := matches[len(matches)-1][1]
	if s, err := strconv.Unquote(`"` + raw + `"`); err == nil {
		return s
	}
	return raw
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Profile is one colima instance, as `colima list --json` reports it.
type Profile struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Arch    string `json:"arch"`
	Runtime string `json:"runtime"`
	Address string `json:"address"`
	CPUs    int    `json:"cpus"`
	Memory  int64  `json:"memory"`
	Disk    int64  `json:"disk"`
}

// Running reports whether the VM is up.
func (p Profile) Running() bool { return strings.EqualFold(p.Status, "Running") }

// List returns every profile. `colima list --json` prints one JSON object
// per line, not an array.
func (c *Client) List(ctx context.Context) ([]Profile, error) {
	out, err := c.run(ctx, "list", "--json")
	if err != nil {
		return nil, err
	}
	return parseList(out)
}

func parseList(out []byte) ([]Profile, error) {
	var profiles []Profile
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var p Profile
		if err := json.Unmarshal(line, &p); err != nil {
			return nil, fmt.Errorf("parsing colima list: %w", err)
		}
		profiles = append(profiles, p)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading colima list: %w", err)
	}
	return profiles, nil
}

// Start boots a profile with whatever configuration it was created with.
func (c *Client) Start(ctx context.Context, profile string) error {
	_, err := c.run(ctx, "start", profile)
	return err
}

// Config is the resource shape of a profile: the subset of `colima start`
// flags dockyard can create a profile with or change on an existing one.
// Zero fields are left to colima's defaults (on create) or to the profile's
// saved configuration (on edit).
type Config struct {
	Runtime   string
	MemoryGiB float64
	CPUs      int
	DiskGiB   int
}

// Args renders the config as `colima start` flags.
func (c Config) Args() []string {
	var args []string
	if c.CPUs > 0 {
		args = append(args, "--cpus", strconv.Itoa(c.CPUs))
	}
	if c.MemoryGiB > 0 {
		args = append(args, "--memory", strconv.FormatFloat(c.MemoryGiB, 'f', -1, 64))
	}
	if c.DiskGiB > 0 {
		args = append(args, "--disk", strconv.Itoa(c.DiskGiB))
	}
	if c.Runtime != "" {
		args = append(args, "--runtime", c.Runtime)
	}
	return args
}

// Runtimes are the container runtimes colima can provision.
var Runtimes = []string{"docker", "containerd", "incus"}

// profileName is what colima accepts as a profile: it becomes a directory
// under the colima home and a docker context name.
var profileName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// ValidName reports why name cannot be a new profile, or "" if it can.
func ValidName(name string, existing []Profile) string {
	if name == "" {
		return "a profile needs a name"
	}
	if !profileName.MatchString(name) {
		return "letters, digits, - and _ only, starting with a letter or digit"
	}
	for _, p := range existing {
		if strings.EqualFold(p.Name, name) {
			return "a profile named " + p.Name + " already exists"
		}
	}
	return ""
}

// StartWith starts a profile with explicit resources. For a profile that
// does not exist yet this creates it; for an existing stopped one the flags
// replace its saved configuration (colima's --save-config defaults on), so
// it is also how a profile is resized. colima reads resources only at start,
// which is why a running profile has to be stopped first.
func (c *Client) StartWith(ctx context.Context, profile string, cfg Config) error {
	_, err := c.run(ctx, append([]string{"start", profile}, cfg.Args()...)...)
	return err
}

// Status is `colima status --json` for a profile; it fails when the profile
// is not running.
func (c *Client) Status(ctx context.Context, profile string) ([]byte, error) {
	return c.run(ctx, "status", "--json", "--profile", profile)
}

// Stop shuts a profile's VM down. Its state persists for the next start.
func (c *Client) Stop(ctx context.Context, profile string) error {
	_, err := c.run(ctx, "stop", profile)
	return err
}

// Restart stops and starts a profile.
func (c *Client) Restart(ctx context.Context, profile string) error {
	_, err := c.run(ctx, "restart", profile)
	return err
}

// Delete destroys a profile's VM. --force skips colima's own y/n prompt,
// which has no terminal to ask on; dockyard confirms before calling this.
func (c *Client) Delete(ctx context.Context, profile string) error {
	_, err := c.run(ctx, "delete", "--force", profile)
	return err
}

// SSHArgs is the argv tail for an interactive shell in a profile's VM.
func SSHArgs(profile string) []string { return []string{"ssh", "--profile", profile} }

// Home is colima's state directory: $COLIMA_HOME, else ~/.colima.
func Home() string {
	if h := os.Getenv("COLIMA_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".colima")
}

// DockerHost is the docker endpoint a profile serves. colima puts each
// profile's socket at <home>/<profile>/docker.sock whether or not the VM is
// running, so this works for a stopped profile too — which is the case that
// matters, since that is when the docker context store cannot be dialed.
func (c *Client) DockerHost(profile string) string {
	return "unix://" + filepath.Join(c.home, profile, "docker.sock")
}

// ContextName is the docker context colima registers for a profile:
// "colima" for the default profile, "colima-<name>" for the rest.
func ContextName(profile string) string {
	if profile == "" || profile == "default" {
		return "colima"
	}
	return "colima-" + profile
}

// ProfileForHost reports which profile serves a docker endpoint, if any.
// It is the inverse of DockerHost, and how dockyard knows that the daemon
// it cannot reach is a colima VM it could start.
func (c *Client) ProfileForHost(host string) (string, bool) {
	return ProfileForHost(c.home, host)
}

// ProfileForHost is [Client.ProfileForHost] against an explicit colima home.
func ProfileForHost(home, host string) (string, bool) {
	path, ok := strings.CutPrefix(host, "unix://")
	if !ok || home == "" {
		return "", false
	}
	path = filepath.Clean(path)
	if filepath.Base(path) != "docker.sock" {
		return "", false
	}
	dir := filepath.Dir(path)
	if filepath.Dir(dir) != filepath.Clean(home) {
		return "", false
	}
	profile := filepath.Base(dir)
	if profile == "" || strings.HasPrefix(profile, "_") {
		return "", false
	}
	return profile, true
}

// GiB converts a byte count from `colima list` to GiB.
func GiB(n int64) float64 { return float64(n) / (1 << 30) }
