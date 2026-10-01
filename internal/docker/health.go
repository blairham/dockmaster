package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
)

// HealthCheck is a container's healthcheck configuration. Zero durations
// and retries mean the daemon's defaults, which HealthCheck reports
// separately so the view can say "(default)" rather than "0s".
type HealthCheck struct {
	Test        []string
	Interval    time.Duration
	Timeout     time.Duration
	StartPeriod time.Duration
	Retries     int
}

// The daemon's healthcheck defaults, applied when a field is zero.
const (
	DefaultHealthInterval = 30 * time.Second
	DefaultHealthTimeout  = 30 * time.Second
	DefaultHealthRetries  = 3
)

// HealthProbe is one run of the healthcheck.
type HealthProbe struct {
	Start    time.Time
	End      time.Time
	Output   string
	ExitCode int // 0 healthy, 1 unhealthy, anything else: the probe itself failed to run
}

// HealthReport is what `docker inspect` buries under .Config.Healthcheck
// and .State.Health, gathered for one container.
type HealthReport struct {
	// Check is nil when the container has no healthcheck at all.
	Check *HealthCheck
	// Status is starting|healthy|unhealthy, or "" with no healthcheck.
	Status string
	// Probes are the daemon's retained results, newest first. The daemon
	// keeps the last five.
	Probes        []HealthProbe
	FailingStreak int
	// Disabled is a healthcheck turned off with NONE, typically to
	// override one the image defines.
	Disabled bool
	Running  bool
}

// Health reads a container's healthcheck configuration and recent probes.
func (c *Client) Health(ctx context.Context, id string) (HealthReport, error) {
	insp, err := c.api.ContainerInspect(ctx, id)
	if err != nil {
		return HealthReport{}, fmt.Errorf("inspecting %s: %w", shortID(id), err)
	}
	var cfg *container.HealthConfig
	if insp.Config != nil {
		cfg = insp.Config.Healthcheck
	}
	var st *container.Health
	running := false
	if insp.ContainerJSONBase != nil && insp.State != nil {
		st = insp.State.Health
		running = insp.State.Running
	}
	return healthReport(cfg, st, running), nil
}

func healthReport(cfg *container.HealthConfig, st *container.Health, running bool) HealthReport {
	r := HealthReport{Running: running}
	if cfg != nil && len(cfg.Test) > 0 {
		if cfg.Test[0] == "NONE" {
			r.Disabled = true
		} else {
			r.Check = &HealthCheck{
				Test:        cfg.Test,
				Interval:    cfg.Interval,
				Timeout:     cfg.Timeout,
				StartPeriod: cfg.StartPeriod,
				Retries:     cfg.Retries,
			}
		}
	}
	if st != nil {
		r.Status = st.Status
		r.FailingStreak = st.FailingStreak
		// The daemon keeps them oldest first; the view wants the latest on top.
		for i := len(st.Log) - 1; i >= 0; i-- {
			p := st.Log[i]
			if p == nil {
				continue
			}
			r.Probes = append(r.Probes, HealthProbe{Start: p.Start, End: p.End, Output: p.Output, ExitCode: p.ExitCode})
		}
	}
	return r
}
