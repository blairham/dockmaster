// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// RemoveContext removes a context from the CLI's context store with
// `docker context rm <name>`, the CLI's own code, so its files — the meta
// and any TLS material — go the way the CLI expects. The name is one
// argument and no shell sees it. Which contexts may be removed (not
// `default`, not one in use) is the caller's to decide; the CLI refuses
// the current one too.
func RemoveContext(ctx context.Context, name string) error {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return errors.New("removing a context needs the `docker` CLI on PATH")
	}
	cmd := exec.CommandContext(ctx, bin, "context", "rm", name) //nolint:gosec // fixed binary and verb
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("docker context rm %s: %w", name, ctx.Err())
	}
	if line := lastMeaningfulLine(string(out)); line != "" {
		return fmt.Errorf("docker context rm %s: %s", name, strings.TrimSpace(line))
	}
	return fmt.Errorf("docker context rm %s: %w", name, err)
}
