package views

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/blairham/dockyard/internal/docker"
)

var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plainLines(lines []string) string {
	return ansiSGR.ReplaceAllString(strings.Join(lines, "\n"), "")
}

func TestFormatHealth(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 1, 0, 0, time.Local)
	r := docker.HealthReport{
		Running: true, Status: "unhealthy", FailingStreak: 2,
		Check: &docker.HealthCheck{Test: []string{"CMD-SHELL", "curl -f http://localhost/ || exit 1"}, Interval: 10 * time.Second},
		Probes: []docker.HealthProbe{
			{Start: now.Add(-10 * time.Second), End: now.Add(-9 * time.Second), ExitCode: 1, Output: "curl: (7) Failed to connect\nretrying\n"},
			{Start: now.Add(-20 * time.Second), ExitCode: 0},
		},
	}
	out := plainLines(FormatHealth(r, now))
	for _, want := range []string{
		"unhealthy", "failing streak 2 of 3 (default) retries",
		"curl -f http://localhost/ || exit 1",
		"10s", "timeout 30s (default)",
		"exit 1 unhealthy", "curl: (7) Failed to connect", "retrying",
		"exit 0 healthy", "(no output)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("health report is missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "exit 1 unhealthy") > strings.Index(out, "exit 0 healthy") {
		t.Error("probes are not newest first")
	}

	if out := plainLines(FormatHealth(docker.HealthReport{}, now)); !strings.Contains(out, "No healthcheck") {
		t.Errorf("no healthcheck: %q", out)
	}
	if out := plainLines(FormatHealth(docker.HealthReport{Disabled: true}, now)); !strings.Contains(out, "disabled (NONE)") {
		t.Errorf("disabled: %q", out)
	}
	cmd := plainLines(FormatHealth(docker.HealthReport{Check: &docker.HealthCheck{Test: []string{"CMD", "pg_isready", "-U", "app"}}}, now))
	if !strings.Contains(cmd, `["pg_isready" "-U" "app"]`) || !strings.Contains(cmd, "No probes have run yet") {
		t.Errorf("CMD form / no probes:\n%s", cmd)
	}
}
