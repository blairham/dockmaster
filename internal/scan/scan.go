// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package scan runs an image vulnerability scanner — grype or trivy,
// whichever is installed — and reads its findings into one shape (#11).
//
// It shells out rather than embedding a scanner: that keeps dockmaster small,
// and lets the user pin the scanner, and its vulnerability database, on
// their own terms. It imports no bubbletea.
package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Severity orders findings, worst last so a larger value is worse.
type Severity int

// Severities, in the scanners' common vocabulary.
const (
	Unknown Severity = iota
	Negligible
	Low
	Medium
	High
	Critical
)

// ParseSeverity reads either scanner's spelling: grype writes "High",
// trivy "HIGH".
func ParseSeverity(s string) Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return Critical
	case "high":
		return High
	case "medium":
		return Medium
	case "low":
		return Low
	case "negligible":
		return Negligible
	default:
		return Unknown
	}
}

func (s Severity) String() string {
	switch s {
	case Critical:
		return "critical"
	case High:
		return "high"
	case Medium:
		return "medium"
	case Low:
		return "low"
	case Negligible:
		return "negligible"
	default:
		return "unknown"
	}
}

// Vuln is one finding: a vulnerability in one installed package.
type Vuln struct {
	ID        string
	Package   string
	Installed string
	FixedIn   string // "" when no fixed version is known
	Title     string
	Severity  Severity
}

// Scanner is an installed scanner.
type Scanner struct {
	Name string // "grype" or "trivy"
	Path string
}

// Scanners are the ones scan knows, in the order Detect prefers them.
var Scanners = []string{"grype", "trivy"}

// Detect finds the first installed scanner, preferring grype.
func Detect() (Scanner, error) {
	for _, name := range Scanners {
		if p, err := exec.LookPath(name); err == nil {
			return Scanner{Name: name, Path: p}, nil
		}
	}
	return Scanner{}, errors.New(
		"no scanner found — install grype (brew install grype) or trivy (brew install trivy)")
}

// Command builds the scan of image ref, reading the image from the daemon at
// dockerHost — the one dockmaster is showing, not whatever the user's shell
// points at. dockerHost "" leaves the environment's own.
func Command(ctx context.Context, s Scanner, ref, dockerHost string) *exec.Cmd {
	var args []string
	switch s.Name {
	case "trivy":
		args = []string{"image", "--quiet", "--format", "json", "--scanners", "vuln", ref}
	default:
		args = []string{"docker:" + ref, "-o", "json", "-q"}
	}
	cmd := exec.CommandContext(
		ctx,
		s.Path,
		args...) //nolint:gosec // a scanner the user installed, on a ref from the daemon
	cmd.Env = os.Environ()
	if dockerHost != "" {
		cmd.Env = append(cmd.Env, "DOCKER_HOST="+dockerHost)
	}
	return cmd
}

// Timeout bounds one scan. The first run of either scanner downloads its
// vulnerability database, which takes minutes on a slow link.
const Timeout = 15 * time.Minute

// Run scans ref and returns its findings, worst first.
func Run(ctx context.Context, s Scanner, ref, dockerHost string) ([]Vuln, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := Command(ctx, s, ref, dockerHost)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		return nil, fmt.Errorf("%s %s: %w: %s", s.Name, ref, err, msg)
	}
	return Parse(s.Name, stdout.Bytes())
}

// Parse reads a scanner's JSON report, worst first, then by ID.
func Parse(scanner string, data []byte) ([]Vuln, error) {
	var out []Vuln
	var err error
	switch scanner {
	case "trivy":
		out, err = parseTrivy(data)
	default:
		out, err = parseGrype(data)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s's report: %w", scanner, err)
	}
	slices.SortStableFunc(out, func(a, b Vuln) int {
		if a.Severity != b.Severity {
			return int(b.Severity) - int(a.Severity)
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func parseGrype(data []byte) ([]Vuln, error) {
	var r struct {
		Matches []struct {
			Artifact struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"artifact"`
			Vulnerability struct {
				ID          string `json:"id"`
				Severity    string `json:"severity"`
				Description string `json:"description"`
				Fix         struct {
					Versions []string `json:"versions"`
				} `json:"fix"`
			} `json:"vulnerability"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	out := make([]Vuln, 0, len(r.Matches))
	for _, m := range r.Matches {
		out = append(out, Vuln{
			ID:        m.Vulnerability.ID,
			Severity:  ParseSeverity(m.Vulnerability.Severity),
			Package:   m.Artifact.Name,
			Installed: m.Artifact.Version,
			FixedIn:   strings.Join(m.Vulnerability.Fix.Versions, ", "),
			Title:     firstLine(m.Vulnerability.Description),
		})
	}
	return out, nil
}

func parseTrivy(data []byte) ([]Vuln, error) {
	var r struct {
		Results []struct {
			Vulnerabilities []struct {
				VulnerabilityID  string `json:"VulnerabilityID"`
				Severity         string `json:"Severity"`
				PkgName          string `json:"PkgName"`
				InstalledVersion string `json:"InstalledVersion"`
				FixedVersion     string `json:"FixedVersion"`
				Title            string `json:"Title"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	var out []Vuln
	for _, res := range r.Results {
		for _, v := range res.Vulnerabilities {
			out = append(out, Vuln{
				ID:        v.VulnerabilityID,
				Severity:  ParseSeverity(v.Severity),
				Package:   v.PkgName,
				Installed: v.InstalledVersion,
				FixedIn:   v.FixedVersion,
				Title:     firstLine(v.Title),
			})
		}
	}
	return out, nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// Counts is the number of findings at each severity, for a summary.
func Counts(vs []Vuln) map[Severity]int {
	c := map[Severity]int{}
	for _, v := range vs {
		c[v.Severity]++
	}
	return c
}
