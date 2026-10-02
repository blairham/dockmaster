// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package docker wraps the Docker Engine API in the narrow, TUI-shaped
// surface dockmaster needs: flat value types with pre-formatted fields, and
// one method per thing a view can show or a keystroke can do.
//
// Nothing in here knows about bubbletea. Views call these from a tea.Cmd
// goroutine and wrap the result in a message.
package docker

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

// Compose label keys. Docker Compose stamps these onto every object it
// creates, and they are the only way to recover project grouping from the
// Engine API — there is no /projects endpoint.
const (
	LabelProject = "com.docker.compose.project"
	LabelService = "com.docker.compose.service"
	LabelNumber  = "com.docker.compose.container-number"

	// Where the project came from, as the Compose CLI recorded it at `up`:
	// comma-separated compose files, the project directory, and
	// comma-separated env files. Paths are on the machine that ran `up`.
	LabelConfigFiles = "com.docker.compose.project.config_files"
	LabelWorkingDir  = "com.docker.compose.project.working_dir"
	LabelEnvFiles    = "com.docker.compose.project.environment_file"
)

// Client is the Engine API handle plus the daemon metadata the info panel
// shows. Safe for concurrent use: the underlying http client is, and the
// stats cache below has its own lock.
type Client struct {
	api         *client.Client
	stats       map[string]Stats
	Host        string
	ContextName string
	Version     string
	APIVersion  string
	OSArch      string
	Name        string
	statsMu     sync.RWMutex
	// imageNames caches what an image ID is called (imageName); an ID
	// names one image for good, so an entry never goes stale.
	imageNames map[string]string
	imageMu    sync.Mutex
	// RequestTimeout, when non-zero, replaces every per-call deadline
	// passed to RequestContext (--request-timeout): longer for a daemon
	// that is slow but honest, shorter to fail fast on one that hangs.
	RequestTimeout time.Duration
}

// RequestContext is the context for one daemon request: def, or
// RequestTimeout when that is set. Safe on a nil client, which the
// headless tests use.
func (c *Client) RequestContext(def time.Duration) (context.Context, context.CancelFunc) {
	if c != nil && c.RequestTimeout > 0 {
		def = c.RequestTimeout
	}
	return context.WithTimeout(context.Background(), def)
}

// New dials the Docker daemon. host may be empty, in which case the
// standard environment resolution applies (DOCKER_HOST, then the default
// socket for the platform).
func New(host string) (*Client, error) {
	resolved, ctxName := ResolveHost(host)

	// API version negotiation is the client's default (and
	// WithAPIVersionNegotiation a deprecated no-op): the first request pings
	// the daemon and settles on the lower of its version and the client's.
	// DOCKER_API_VERSION, read by FromEnv, pins a version instead.
	opts := []client.Opt{client.FromEnv}
	if resolved != "" {
		opts = append(opts, client.WithHost(resolved))
	}
	api, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &Client{
		api:         api,
		Host:        api.DaemonHost(),
		ContextName: ctxName,
		stats:       make(map[string]Stats),
	}, nil
}

// API exposes the raw SDK client for the few call sites that need it
// (log streaming, exec). Prefer adding a method here over reaching through.
func (c *Client) API() *client.Client { return c.api }

// Negotiate pings the daemon and fills the metadata fields. Called once at
// startup; a failure here is fatal for the app since every view needs the
// daemon.
func (c *Client) Negotiate(ctx context.Context) error {
	res, err := c.api.Info(ctx, client.InfoOptions{})
	if err != nil {
		return fmt.Errorf("connecting to docker daemon at %s: %w", c.Host, err)
	}
	info := res.Info
	c.Version = info.ServerVersion
	c.Name = info.Name
	c.OSArch = info.OSType + "/" + info.Architecture
	c.APIVersion = c.api.ClientVersion()
	return nil
}

// Ping reports whether the daemon answers, without the cost of Info.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.api.Ping(ctx, client.PingOptions{})
	return err
}

// Close releases the underlying transport.
func (c *Client) Close() error {
	if c.api == nil {
		return nil
	}
	return c.api.Close()
}

// FormatUserError strips the SDK's wrapping down to something that fits on
// one status-bar line. The daemon's connection errors in particular are
// several sentences of shell advice that nobody can read in a 1-row bar.
func FormatUserError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case client.IsErrConnectionFailed(err):
		return fmt.Errorf("cannot reach the docker daemon — is it running?")
	case strings.Contains(msg, "permission denied"):
		return fmt.Errorf("permission denied talking to the docker socket")
	}
	// Collapse multi-line daemon errors to the first line.
	if i := strings.IndexByte(msg, '\n'); i > 0 {
		msg = msg[:i]
	}
	return fmt.Errorf("%s", msg)
}

// shortID trims a 64-hex content-addressable ID to the 12 chars docker
// itself displays. Handles the "sha256:" prefix the image endpoints use.
func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// since renders a duration the way docker's CLI does — one unit, no
// decimals, so the column stays narrow.
func since(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}

// defaultHost is the endpoint used when neither the environment nor the
// context store names one.
func defaultHost() string { return "unix:///var/run/docker.sock" }
