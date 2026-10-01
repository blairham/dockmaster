package engines

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Single-engine provider names.
const (
	DockerDesktopName  = "docker-desktop"
	RancherDesktopName = "rancher-desktop"
	OrbStackName       = "orbstack"
)

// Single drives a runtime that manages exactly one engine through an app —
// Docker Desktop, Rancher Desktop, OrbStack. Each registers a docker
// context, and whether its daemon answers on that endpoint is the one
// status check that means the same thing for all three.
type Single struct {
	Run          Runner
	Ping         func(ctx context.Context, host string) bool
	ParseStatus  func([]byte) (string, bool, error)
	SocketExists func(path string) bool
	Engine       string
	Context      string
	Host         string
	StartC       []string
	StopC        []string
	StatusC      []string
}

// socketGone reports whether Host is a unix socket that is not on disk.
func (s Single) socketGone() bool {
	path, ok := strings.CutPrefix(s.Host, "unix://")
	return ok && s.SocketExists != nil && !s.SocketExists(path)
}

// Name implements Provider.
func (s Single) Name() string { return s.Engine }

// Caps implements Provider: start, stop, restart and connect only.
func (Single) Caps() Caps { return Caps{Single: true} }

// List implements Provider: the one engine, with the runtime's own status
// when it reports one, else running when its daemon answers.
func (s Single) List(ctx context.Context) ([]Machine, error) {
	if s.socketGone() {
		return []Machine{s.machine("Stopped", false)}, nil
	}
	status, up, ok := s.ownStatus(ctx)
	if !ok {
		up = s.Host != "" && s.Ping != nil && s.Ping(ctx, s.Host)
		status = "Stopped"
		if up {
			status = "Running"
		}
	}
	return []Machine{s.machine(status, up)}, nil
}

func (s Single) machine(status string, up bool) Machine {
	return Machine{
		Provider: s.Engine, Name: s.Engine, Status: status, Running: up,
		Context: s.Context, Host: s.Host, Runtime: "docker",
	}
}

func (s Single) run(ctx context.Context, argv []string) error {
	if len(argv) == 0 {
		return ErrUnsupported
	}
	_, err := s.Run(ctx, argv[0], argv[1:]...)
	return err
}

// Start implements Provider.
func (s Single) Start(ctx context.Context, _ string) error { return s.run(ctx, s.StartC) }

// Stop implements Provider.
func (s Single) Stop(ctx context.Context, _ string) error { return s.run(ctx, s.StopC) }

// Restart implements Provider.
func (s Single) Restart(ctx context.Context, name string) error {
	if err := s.Stop(ctx, name); err != nil {
		return err
	}
	return s.Start(ctx, name)
}

// Delete implements Provider: an app is uninstalled, not deleted from here.
func (Single) Delete(context.Context, string) error { return ErrUnsupported }

// Create implements Provider.
func (Single) Create(context.Context, string, Config) error { return ErrUnsupported }

// Edit implements Provider.
func (Single) Edit(context.Context, string, Config, bool) error { return ErrUnsupported }

// Inspect implements Provider: the runtime's own status report where it has
// one, else what dockmaster knows.
func (s Single) Inspect(ctx context.Context, _ string) ([]byte, error) {
	if len(s.StatusC) > 0 && s.Run != nil {
		if out, err := s.Run(ctx, s.StatusC[0], s.StatusC[1:]...); err == nil {
			return out, nil
		}
	}
	ms, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(ms[0])
}

// ShellCommand implements Provider.
func (Single) ShellCommand(string) []string { return nil }

func (s Single) ownStatus(ctx context.Context) (string, bool, bool) {
	if len(s.StatusC) == 0 || s.ParseStatus == nil || s.Run == nil {
		return "", false, false
	}
	out, err := s.Run(ctx, s.StatusC[0], s.StatusC[1:]...)
	if err != nil {
		// Docker Desktop's status exits non-zero when it is not running.
		return "Stopped", false, true
	}
	status, up, err := s.ParseStatus(out)
	if err != nil {
		return "", false, false
	}
	return status, up, true
}

// DockerDesktopStatus reads `docker desktop status --format json`. "paused"
// is Resource Saver: the engine is up and wakes on the next request, so it
// counts as running.
func DockerDesktopStatus(out []byte) (string, bool, error) {
	var st struct{ Status string }
	if err := json.Unmarshal(out, &st); err != nil {
		return "", false, err
	}
	s := strings.ToLower(st.Status)
	return titleCase(s), s == "running" || s == "paused", nil
}

// OrbStackStatus reads `orb status`: "Running" or "Stopped".
func OrbStackStatus(out []byte) (string, bool, error) {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", false, errNoStatus
	}
	return s, strings.EqualFold(s, "running"), nil
}

var errNoStatus = errors.New("no status reported")

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
