package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// NewHealthView shows container id's healthcheck: configuration, current
// streak, and the daemon's last few probe results with their output — the
// "why" behind an unhealthy badge, which `docker inspect` buries in JSON.
// It refetches with every refresh, so new probes appear as they run.
func NewHealthView(client *docker.Client, id, name string) *InspectView {
	v := NewInspectFetchView(name+" health", func(ctx context.Context) ([]byte, error) {
		r, err := client.Health(ctx, id)
		if err != nil {
			return nil, err
		}
		return []byte(strings.Join(FormatHealth(r, time.Now()), "\n")), nil
	})
	v.plain = true
	return v
}

// FormatHealth renders a health report as styled lines.
func FormatHealth(r docker.HealthReport, now time.Time) []string {
	label := func(s string) string { return style.Muted.Render(fmt.Sprintf("%-14s", s)) }
	switch {
	case r.Disabled:
		return []string{"", "  Healthcheck disabled (NONE) — the image's check is overridden, so docker reports no health."}
	case r.Check == nil:
		return []string{"", "  No healthcheck — the image defines none and none was set when the container was created."}
	}

	c := r.Check
	status := r.Status
	if status == "" {
		status = "—"
	}
	retries := withDefault(c.Retries, docker.DefaultHealthRetries)
	statusLine := "  " + label("Status") + style.HealthStyle(r.Status).Render(status)
	if r.FailingStreak > 0 {
		statusLine += style.Muted.Render(fmt.Sprintf("   failing streak %d of %s retries", r.FailingStreak, retries))
	}
	if !r.Running {
		statusLine += style.Muted.Render("   (not running — no probes run)")
	}

	lines := []string{
		"",
		statusLine,
		"  " + label("Check") + testCommand(c.Test),
		"  " + label("Interval") + durDefault(c.Interval, docker.DefaultHealthInterval) +
			style.Muted.Render("   timeout ") + durDefault(c.Timeout, docker.DefaultHealthTimeout) +
			style.Muted.Render("   retries ") + retries +
			style.Muted.Render("   start period ") + durDefault(c.StartPeriod, 0),
		"",
	}
	if len(r.Probes) == 0 {
		return append(lines, "  "+style.Muted.Render("No probes have run yet."))
	}
	lines = append(lines, "  "+style.Muted.Render("Recent probes, newest first"))
	for _, p := range r.Probes {
		lines = append(lines, probeLines(p, now)...)
	}
	return lines
}

// probeLines is one probe: when, how long, exit code, then its output
// indented beneath — probe output is usually the error message.
func probeLines(p docker.HealthProbe, now time.Time) []string {
	when := p.Start.Local().Format("15:04:05")
	if now.Sub(p.Start) >= 24*time.Hour {
		when = p.Start.Local().Format("Jan 02 15:04")
	}
	took := ""
	if !p.End.IsZero() && p.End.After(p.Start) {
		took = p.End.Sub(p.Start).Round(time.Millisecond).String()
	}
	head := fmt.Sprintf("  %s  %s  %s", when, exitText(p.ExitCode), style.Muted.Render(took))
	out := []string{"", head}
	text := strings.TrimRight(p.Output, "\n")
	if text == "" {
		return append(out, "      "+style.Muted.Render("(no output)"))
	}
	for _, l := range strings.Split(text, "\n") {
		out = append(out, "      "+sanitizeLogText(l))
	}
	return out
}

// exitText names a probe's exit code the way docker means it.
func exitText(code int) string {
	switch code {
	case 0:
		return style.Success.Render("exit 0 healthy  ")
	case 1:
		return lipgloss.NewStyle().Foreground(style.ColorRed).Render("exit 1 unhealthy")
	default:
		// 2 is reserved and counts as unhealthy; anything else means the
		// probe could not run at all (command not found, timed out).
		return lipgloss.NewStyle().Foreground(style.ColorOrange).Render(fmt.Sprintf("exit %d probe err", code))
	}
}

// testCommand renders a healthcheck's Test the way a Dockerfile spells it.
func testCommand(test []string) string {
	if len(test) == 0 {
		return ""
	}
	switch test[0] {
	case "CMD-SHELL":
		return strings.Join(test[1:], " ")
	case "CMD":
		return fmt.Sprintf("%q", test[1:])
	}
	return strings.Join(test, " ")
}

func durDefault(d, def time.Duration) string {
	if d > 0 {
		return d.String()
	}
	if def == 0 {
		return "0s"
	}
	return def.String() + style.Muted.Render(" (default)")
}

func withDefault(n, def int) string {
	if n > 0 {
		return fmt.Sprint(n)
	}
	return fmt.Sprint(def) + style.Muted.Render(" (default)")
}
