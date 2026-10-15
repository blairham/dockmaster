// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CommandStream is a running CLI command's output, line by line: stdout and
// stderr interleaved as a terminal would show them, since a build writes
// its progress to stderr and a compose build its result to stdout.
//
// Lines closes when the command's output ends; Done then receives exactly
// one value — nil for a zero exit, otherwise why it failed — and closes.
type CommandStream struct {
	Lines <-chan string
	Done  <-chan error
}

// CommandSpec is one command to stream: an argv run with no shell, from
// Dir when set, with Env added to the environment dockmaster was started
// with.
type CommandSpec struct {
	Dir  string
	Args []string
	Env  []string
}

// StreamFunc starts spec and streams its output: StreamCommand, or a fake
// in tests, which never run docker.
type StreamFunc func(ctx context.Context, spec CommandSpec) (*CommandStream, error)

// maxStreamLine is the longest output line kept whole. A build that prints
// a minified bundle on one line is split rather than failing the scan.
const maxStreamLine = 1 << 20

// StreamCommand runs spec through exec — no shell — and streams its
// combined output. Canceling ctx kills the command; Done then reports the
// cancellation rather than the kill's exit status.
func StreamCommand(ctx context.Context, spec CommandSpec) (*CommandStream, error) {
	if len(spec.Args) == 0 {
		return nil, fmt.Errorf("nothing to run")
	}
	cmd := exec.CommandContext(ctx, spec.Args[0], spec.Args[1:]...) //nolint:gosec // argv built by the caller, no shell
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	pr, pw := io.Pipe()
	// One writer for both: exec copies each into it from its own goroutine
	// only when they differ, so the same writer keeps their order.
	cmd.Stdout, cmd.Stderr = pw, pw
	// A child that keeps the output open after the command exits (a
	// buildx helper) must not hold Wait for ever.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		_ = pr.Close() //nolint:errcheck // never read
		_ = pw.Close() //nolint:errcheck // never written
		return nil, err
	}
	lines := make(chan string, 256)
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		_ = pw.Close() //nolint:errcheck // ends the reader below
		done <- err
		close(done)
	}()
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), maxStreamLine)
		sc.Split(scanTermLines)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				// Nobody is reading any more: drain, so the command is
				// not left blocked on a full pipe while it dies.
				_, _ = io.Copy(io.Discard, pr) //nolint:errcheck // discarding
				return
			}
		}
		// Past a line too long to keep, the rest still has to be read.
		_, _ = io.Copy(io.Discard, pr) //nolint:errcheck // discarding
	}()
	return &CommandStream{Lines: lines, Done: done}, nil
}

// scanTermLines splits on \n, and on a lone \r too: a progress bar redraws
// its line with \r, and each redraw is a line worth showing in a log.
func scanTermLines(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		switch b {
		case '\n':
			return i + 1, trimCR(data[:i]), nil
		case '\r':
			if i+1 < len(data) && data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
			if i+1 < len(data) || atEOF {
				return i + 1, data[:i], nil
			}
			// A \r at the end of the buffer may be half of \r\n.
			return 0, nil, nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func trimCR(b []byte) []byte { return []byte(strings.TrimSuffix(string(b), "\r")) }
