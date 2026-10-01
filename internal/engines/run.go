package engines

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs a program and returns its stdout; on failure the error
// carries the last line of stderr. Swappable so tests never touch a VM.
type Runner func(ctx context.Context, bin string, args ...string) ([]byte, error)

// ExecRunner runs real programs.
func ExecRunner(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed runtime CLIs; args are verbs and machine names
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s %s: %w", bin, strings.Join(args, " "), ctx.Err())
		}
		if line := lastLine(stderr.String()); line != "" {
			return nil, fmt.Errorf("%s: %s", bin, line)
		}
		return nil, fmt.Errorf("%s %s: %w", bin, strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
