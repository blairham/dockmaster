package docker

import (
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func TestHealthReport(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	cfg := &container.HealthConfig{Test: []string{"CMD-SHELL", "curl -f http://localhost/ || exit 1"}, Retries: 5}
	st := &container.Health{
		Status: container.Unhealthy, FailingStreak: 2,
		Log: []*container.HealthcheckResult{
			{Start: t0, End: t0.Add(time.Second), ExitCode: 0, Output: "ok"},
			nil,
			{Start: t0.Add(30 * time.Second), ExitCode: 1, Output: "connection refused"},
		},
	}
	r := healthReport(cfg, st, true)
	if r.Check == nil || r.Check.Retries != 5 || r.Status != "unhealthy" || r.FailingStreak != 2 || !r.Running {
		t.Fatalf("report = %+v", r)
	}
	if len(r.Probes) != 2 || r.Probes[0].Output != "connection refused" || r.Probes[1].Output != "ok" {
		t.Errorf("probes not newest first, nil kept, or lost: %+v", r.Probes)
	}

	if r := healthReport(nil, nil, true); r.Check != nil || r.Disabled {
		t.Errorf("no healthcheck: %+v", r)
	}
	if r := healthReport(&container.HealthConfig{Test: []string{"NONE"}}, nil, true); !r.Disabled || r.Check != nil {
		t.Errorf("NONE: %+v", r)
	}
	// An empty Test inherits the image's check: there is none to report.
	if r := healthReport(&container.HealthConfig{}, nil, true); r.Check != nil || r.Disabled {
		t.Errorf("empty test: %+v", r)
	}
}
