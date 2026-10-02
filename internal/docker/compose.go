// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ComposeRunner executes the docker CLI with args and returns stdout. On a
// non-zero exit it returns a *ComposeError carrying stderr. Swappable so
// tests never touch a daemon.
type ComposeRunner func(ctx context.Context, args ...string) ([]byte, error)

// Compose drives `docker compose` for a project dockmaster found by its
// labels.
//
// There is no Engine API for Compose: `up`, `down` and `pull` are client-side
// operations over a compose file, so the only faithful way to run them is
// the Compose CLI against the same files, directory and env files the
// project was brought up with. The labels on its containers record all
// three.
type Compose struct {
	run  ComposeRunner
	host string
}

// ErrNoDockerCLI is returned when the docker CLI is not on PATH.
var ErrNoDockerCLI = errors.New("compose actions need the `docker` CLI on PATH")

// NewCompose finds the docker CLI. host is the endpoint dockmaster is
// connected to; every invocation passes it as --host so compose acts on
// the daemon on screen, not whatever context the shell has.
func NewCompose(host string) (*Compose, error) {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return nil, ErrNoDockerCLI
	}
	return NewComposeWithRunner(composeExec(bin), host), nil
}

// NewComposeWithRunner builds a Compose over an arbitrary runner.
func NewComposeWithRunner(run ComposeRunner, host string) *Compose {
	return &Compose{run: run, host: host}
}

func composeExec(bin string) ComposeRunner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary; args from compose labels
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("docker compose: %w", ctx.Err())
			}
			return nil, &ComposeError{Stderr: stderr.String(), Err: err}
		}
		return stdout.Bytes(), nil
	}
}

// ComposeError is a failed compose invocation.
type ComposeError struct {
	Err    error
	Stderr string
}

func (e *ComposeError) Error() string {
	if line := lastMeaningfulLine(e.Stderr); line != "" {
		return "compose: " + line
	}
	return fmt.Sprintf("compose: %v", e.Err)
}

func (e *ComposeError) Unwrap() error { return e.Err }

// lastMeaningfulLine is the last non-blank line of compose's stderr, which
// is where it puts the reason. Progress lines above it are noise in a flash.
func lastMeaningfulLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// ComposeArgs is the project-selecting part of a compose invocation: the
// name, the directory, every compose file and env file the labels record.
func (p Project) ComposeArgs() []string {
	args := []string{"-p", p.Name}
	if p.WorkingDir != "" {
		args = append(args, "--project-directory", p.WorkingDir)
	}
	for _, f := range p.ConfigFiles {
		args = append(args, "-f", f)
	}
	for _, f := range p.EnvFiles {
		args = append(args, "--env-file", f)
	}
	return args
}

// MissingFile reports why compose cannot be run for p from this machine:
// no compose files recorded, or the first one that does not exist here. ""
// means every file is present. A daemon on another host, or a checkout that
// has moved since `up`, is the usual reason.
func (p Project) MissingFile() string {
	if len(p.ConfigFiles) == 0 {
		return "no compose file recorded on its containers"
	}
	for _, f := range append(append([]string{}, p.ConfigFiles...), p.EnvFiles...) {
		if _, err := os.Stat(f); err != nil {
			return f + " is not on this machine"
		}
	}
	return ""
}

func (c *Compose) do(ctx context.Context, p Project, verb ...string) error {
	args := []string{}
	if c.host != "" {
		args = append(args, "--host", c.host)
	}
	args = append(args, "compose")
	args = append(args, p.ComposeArgs()...)
	args = append(args, verb...)
	_, err := c.run(ctx, args...)
	return err
}

// Up creates or recreates the project's containers, detached.
func (c *Compose) Up(ctx context.Context, p Project) error { return c.do(ctx, p, "up", "-d") }

// Down stops and removes the project's containers and networks. Volumes
// are kept: removing them is `down -v`, which dockmaster does not offer.
func (c *Compose) Down(ctx context.Context, p Project) error { return c.do(ctx, p, "down") }

// Restart restarts the project's services.
func (c *Compose) Restart(ctx context.Context, p Project) error { return c.do(ctx, p, "restart") }

// Pull pulls the images the project's services use.
func (c *Compose) Pull(ctx context.Context, p Project) error { return c.do(ctx, p, "pull") }
