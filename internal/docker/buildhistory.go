// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

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
	"sort"
	"strings"
	"time"
)

// Build history (#118) is BuildKit's record of every build a builder ran —
// what Docker Desktop's Builds tab shows — read through `docker buildx
// history`, as #104 runs `buildx build`: the CLI is the one stable
// interface to it, and buildx keeps it per builder.

// MinBuildxHistory is the first buildx with `history ls --format json`.
// `history inspect --format json`, which the error column and a rebuild
// read, came in v0.21.0; without it those say so and the list still works.
const MinBuildxHistory = "v0.20.0"

// ErrNoBuildxHistory is a buildx too old for `history`, or none at all.
var ErrNoBuildxHistory = errors.New("build history needs buildx " + MinBuildxHistory +
	" or later (`docker buildx history`)")

// Record statuses, as `history ls` prints them. A canceled build is an
// Error to buildx too.
const (
	BuildRunning   = "Running"
	BuildCompleted = "Completed"
	BuildError     = "Error"
)

// BuildRecord is one line of `docker buildx history ls --format json`.
type BuildRecord struct {
	Created   time.Time `json:"created_at"`
	Completed time.Time `json:"completed_at"`
	// Ref is the full reference — builder/node/id; ID is its last part,
	// what inspect, logs and rm take.
	Ref            string `json:"ref"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	TotalSteps     int    `json:"total_steps"`
	CompletedSteps int    `json:"completed_steps"`
	CachedSteps    int    `json:"cached_steps"`
}

// ID is the record's own ID, the last part of its ref.
func (r BuildRecord) ID() string { return r.Ref[strings.LastIndex(r.Ref, "/")+1:] }

// Duration is how long the build ran, or has run by now when it still is.
func (r BuildRecord) Duration(now time.Time) time.Duration {
	end := r.Completed
	if end.IsZero() {
		end = now
	}
	if r.Created.IsZero() || end.Before(r.Created) {
		return 0
	}
	return end.Sub(r.Created)
}

// CachedPercent is the share of completed steps served from the cache.
func (r BuildRecord) CachedPercent() int {
	if r.CompletedSteps == 0 {
		return 0
	}
	return r.CachedSteps * 100 / r.CompletedSteps
}

// ParseBuildHistory reads `history ls --format json`, one record per line,
// newest first.
func ParseBuildHistory(data []byte) ([]BuildRecord, error) {
	var out []BuildRecord
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r BuildRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("build history: %w", err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("build history: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// BuildDetail is what a rebuild and the error column need from `history
// inspect --format json`.
type BuildDetail struct {
	Error *struct {
		Message string `json:"Message"`
		Name    string `json:"Name"`
	} `json:"Error"`
	Name       string `json:"Name"`
	Context    string `json:"Context"`
	Dockerfile string `json:"Dockerfile"`
	Target     string `json:"Target"`
	BuildArgs  []struct {
		Name  string `json:"Name"`
		Value string `json:"Value"`
	} `json:"BuildArgs"`
}

// ParseBuildDetail reads `history inspect --format json`.
func ParseBuildDetail(data []byte) (BuildDetail, error) {
	var d BuildDetail
	if err := json.Unmarshal(data, &d); err != nil {
		return BuildDetail{}, fmt.Errorf("build record: %w", err)
	}
	return d, nil
}

// ErrorLine is a failed build's reason on one line — the step and its
// message — "" for a build that did not fail.
func (d BuildDetail) ErrorLine() string {
	if d.Error == nil {
		return ""
	}
	msg := strings.Join(strings.Fields(d.Error.Message), " ")
	if d.Error.Name != "" {
		return d.Error.Name + ": " + msg
	}
	return msg
}

// Spec is the build again, for a rebuild: the same context, Dockerfile,
// target and build args, untagged — the record does not keep the tag. A
// context that is not a directory on this machine — a git URL, stdin, a
// build run elsewhere — cannot be built again from here.
func (d BuildDetail) Spec() (BuildSpec, error) {
	if d.Context == "" || !filepath.IsAbs(d.Context) {
		return BuildSpec{}, fmt.Errorf("its context %q is not a directory on this machine", d.Context)
	}
	if st, err := os.Stat(d.Context); err != nil || !st.IsDir() {
		return BuildSpec{}, fmt.Errorf("its context %s is not on this machine any more", d.Context)
	}
	spec := BuildSpec{Context: d.Context, Target: d.Target}
	if d.Dockerfile != "" && d.Dockerfile != "Dockerfile" {
		spec.Dockerfile = d.Dockerfile
	}
	for _, a := range d.BuildArgs {
		spec.BuildArgs = append(spec.BuildArgs, a.Name+"="+a.Value)
	}
	return spec, nil
}

// HistoryRunner runs the docker CLI with args and env added to the
// environment, returning its stdout: execHistory, or a fake in tests.
type HistoryRunner func(ctx context.Context, env []string, args ...string) ([]byte, error)

// BuildHistory reads and prunes a builder's history through the docker CLI
// pointed at one daemon.
type BuildHistory struct {
	run      HistoryRunner
	builder  string
	endpoint []string
}

// NewBuildHistory is the history of builder ("" for the one buildx picks
// for the endpoint) through the docker CLI on PATH.
func NewBuildHistory(endpoint []string, builder string) (*BuildHistory, error) {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return nil, errors.New("build history needs the `docker` CLI on PATH")
	}
	return NewBuildHistoryWithRunner(execHistory(bin), endpoint, builder), nil
}

// NewBuildHistoryWithRunner builds a BuildHistory over an arbitrary runner.
func NewBuildHistoryWithRunner(run HistoryRunner, endpoint []string, builder string) *BuildHistory {
	return &BuildHistory{run: run, endpoint: endpoint, builder: builder}
}

// args is `<endpoint> buildx history <verb> [--builder b] rest…`. Every
// ref goes after the flags; buildx refs never start with -.
func (h *BuildHistory) args(verb string, rest ...string) []string {
	args := append(append([]string{}, h.endpoint...), "buildx", "history", verb)
	if h.builder != "" {
		args = append(args, "--builder="+h.builder)
	}
	return append(args, rest...)
}

func (h *BuildHistory) exec(ctx context.Context, verb string, rest ...string) ([]byte, error) {
	out, err := h.run(ctx, endpointEnv(h.endpoint), h.args(verb, rest...)...)
	if err != nil && missingHistory(err) {
		return nil, ErrNoBuildxHistory
	}
	return out, err
}

// List is every record the builder keeps, newest first.
func (h *BuildHistory) List(ctx context.Context) ([]BuildRecord, error) {
	out, err := h.exec(ctx, "ls", "--format=json")
	if err != nil {
		return nil, err
	}
	return ParseBuildHistory(out)
}

// Inspect is the record as buildx describes it to a person: source,
// Dockerfile, args, platforms, outputs, the error.
func (h *BuildHistory) Inspect(ctx context.Context, id string) ([]byte, error) {
	return h.exec(ctx, "inspect", id)
}

// Detail is the record as JSON, for a rebuild and the error column.
func (h *BuildHistory) Detail(ctx context.Context, id string) (BuildDetail, error) {
	out, err := h.exec(ctx, "inspect", "--format=json", id)
	if err != nil {
		return BuildDetail{}, err
	}
	return ParseBuildDetail(out)
}

// LogsCommand replays a record's build log, to stream into a log view.
func (h *BuildHistory) LogsCommand(bin, id string) CommandSpec {
	return CommandSpec{
		Args: append([]string{bin}, h.args("logs", id)...),
		Env:  endpointEnv(h.endpoint),
	}
}

// Remove deletes a record.
func (h *BuildHistory) Remove(ctx context.Context, id string) error {
	_, err := h.exec(ctx, "rm", id)
	return err
}

// RemoveAll deletes every record the builder keeps — `rm --all` — and
// reports how many there were: the listing is read first, since buildx
// says nothing about what it removed.
func (h *BuildHistory) RemoveAll(ctx context.Context) (RemoveAllResult, error) {
	recs, err := h.List(ctx)
	if err != nil {
		return RemoveAllResult{}, err
	}
	if len(recs) == 0 {
		return RemoveAllResult{}, nil
	}
	if _, err := h.exec(ctx, "rm", "--all"); err != nil {
		return RemoveAllResult{}, err
	}
	return RemoveAllResult{Removed: len(recs)}, nil
}

// HistoryError is a failed `buildx history` call.
type HistoryError struct {
	Err    error
	Stderr string
}

func (e *HistoryError) Error() string {
	if line := lastMeaningfulLine(e.Stderr); line != "" {
		return "buildx: " + strings.TrimPrefix(line, "ERROR: ")
	}
	return fmt.Sprintf("buildx: %v", e.Err)
}

func (e *HistoryError) Unwrap() error { return e.Err }

// missingHistory reports a CLI that has no `buildx history`: no buildx
// plugin, or one older than MinBuildxHistory, which reads `history` as an
// unknown command and `--format` as an unknown flag.
func missingHistory(err error) bool {
	var he *HistoryError
	if !errors.As(err, &he) {
		return false
	}
	s := he.Stderr
	return strings.Contains(s, "unknown command") ||
		strings.Contains(s, "is not a docker command") ||
		strings.Contains(s, "unknown flag: --format")
}

func execHistory(bin string) HistoryRunner {
	return func(ctx context.Context, env []string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary; refs from buildx's own listing
		if len(env) > 0 {
			cmd.Env = append(os.Environ(), env...)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("buildx history: %w", ctx.Err())
			}
			return nil, &HistoryError{Stderr: stderr.String(), Err: err}
		}
		return stdout.Bytes(), nil
	}
}
