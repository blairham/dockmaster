// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// LogLine is one line of container output, tagged with the stream it came
// from so the view can color stderr differently.
type LogLine struct {
	Time   time.Time
	Text   string
	Stderr bool
}

// LogStream is a live tail of a container's output. Lines arrives
// asynchronously; Err carries a terminal error (the container went away,
// the daemon dropped the connection) exactly once, after which Lines
// closes. Canceling the context passed to StreamLogs shuts it all down.
type LogStream struct {
	Lines <-chan LogLine
	Err   <-chan error
}

// StreamLogs opens a follow-mode log tail. tail is how many historical
// lines to fetch first ("all" for everything); timestamps prefixes each
// line with the daemon's receive time.
//
// The stream is demultiplexed according to the container's TTY setting:
// a TTY container writes raw bytes, a non-TTY one writes 8-byte-framed
// chunks. Reading a framed stream raw is the classic symptom where every
// log line starts with a couple of mojibake control characters.
//
// since, when not zero, starts the stream at that time instead of a line
// count: every line since then, then follow.
func (c *Client) StreamLogs(
	ctx context.Context,
	id string,
	tail int,
	timestamps bool,
	since time.Time,
) (*LogStream, error) {
	insp, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspecting %s for log stream: %w", shortID(id), err)
	}
	tty := insp.Container.Config != nil && insp.Container.Config.Tty

	rc, err := c.api.ContainerLogs(ctx, id, logOptions(tail, timestamps, since))
	if err != nil {
		return nil, fmt.Errorf("streaming logs for %s: %w", shortID(id), err)
	}

	lines := make(chan LogLine, 512)
	errc := make(chan error, 1)

	go func() {
		defer close(lines)
		defer rc.Close() //nolint:errcheck // stream teardown; close error is not actionable

		if tty {
			// Raw stream — every byte is stdout as far as the daemon is
			// concerned, so there is no stderr to distinguish.
			scanLines(ctx, rc, false, lines)
			errc <- nil
			return
		}

		// Non-TTY: stdcopy demuxes into two pipes so stdout and stderr
		// stay distinguishable in the view.
		outR, outW := io.Pipe()
		errR, errW := io.Pipe()
		go func() {
			_, cpErr := stdcopy.StdCopy(outW, errW, rc)
			outW.CloseWithError(cpErr) //nolint:errcheck // propagated to the scanners
			errW.CloseWithError(cpErr) //nolint:errcheck // propagated to the scanners
			errc <- cpErr
		}()

		done := make(chan struct{}, 2)
		go func() { scanLines(ctx, outR, false, lines); done <- struct{}{} }()
		go func() { scanLines(ctx, errR, true, lines); done <- struct{}{} }()
		<-done
		<-done
	}()

	return &LogStream{Lines: lines, Err: errc}, nil
}

// logOptions builds a follow-mode log request: the last tail lines (all
// when tail is 0), or — when since is set — everything since then.
func logOptions(tail int, timestamps bool, since time.Time) client.ContainerLogsOptions {
	opts := client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: timestamps,
		Tail:       "all",
	}
	if tail > 0 {
		opts.Tail = strconv.Itoa(tail)
	}
	if !since.IsZero() {
		// The API takes a Unix timestamp; with one, the tail is everything
		// since then rather than a count.
		opts.Since = strconv.FormatInt(since.Unix(), 10)
		opts.Tail = "all"
	}
	return opts
}

// maxLogLine caps a single line. A container that writes a megabyte
// without a newline (a minified bundle, a base64 blob) would otherwise
// make bufio.Scanner return an error and silently end the tail.
const maxLogLine = 256 * 1024

func scanLines(ctx context.Context, r io.Reader, stderr bool, out chan<- LogLine) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLogLine)
	for sc.Scan() {
		text := sc.Text()
		ts := time.Time{}
		// With Timestamps=true the daemon prefixes an RFC3339Nano stamp
		// and a space. Split it off so the view can format it itself.
		if i := strings.IndexByte(text, ' '); i > 0 {
			if t, err := time.Parse(time.RFC3339Nano, text[:i]); err == nil {
				ts, text = t, text[i+1:]
			}
		}
		select {
		case out <- LogLine{Time: ts, Text: text, Stderr: stderr}:
		case <-ctx.Done():
			return
		}
	}
}

// Logs fetches a bounded snapshot of a container's output without
// following. Used by the inspect/detail views that want recent context
// without holding a stream open.
func (c *Client) Logs(ctx context.Context, id string, tail int) ([]LogLine, error) {
	insp, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspecting %s for logs: %w", shortID(id), err)
	}
	tty := insp.Container.Config != nil && insp.Container.Config.Tty

	rc, err := c.api.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
	})
	if err != nil {
		return nil, fmt.Errorf("reading logs for %s: %w", shortID(id), err)
	}
	defer rc.Close() //nolint:errcheck // read-only body

	var reader io.Reader = rc
	if !tty {
		pr, pw := io.Pipe()
		go func() {
			_, cpErr := stdcopy.StdCopy(pw, pw, rc)
			pw.CloseWithError(cpErr) //nolint:errcheck // propagated to the scanner
		}()
		reader = pr
	}

	ch := make(chan LogLine, 1024)
	go func() { scanLines(ctx, reader, false, ch); close(ch) }()

	out := make([]LogLine, 0, tail)
	for l := range ch {
		out = append(out, l)
	}
	return out, nil
}
