// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
)

// pipeline is `cmds[0] | cmds[1] | …` built in Go rather than by a shell:
// each command is exec'd with its own argv, and os.Pipe joins one's stdout
// to the next one's stdin. Every command's stderr, and the last one's
// stdout, go to the terminal. It is a tea.ExecCommand, so the terminal is
// handed over to it as to a single command.
type pipeline struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	cmds   []*exec.Cmd
}

func (p *pipeline) SetStdin(r io.Reader)  { p.stdin = r }
func (p *pipeline) SetStdout(w io.Writer) { p.stdout = w }
func (p *pipeline) SetStderr(w io.Writer) { p.stderr = w }

// Run starts every command, waits for all of them, and reports the last
// one's exit, as a shell does without pipefail (and as k9s does): `grep`
// finding nothing is the pipeline failing, a `yes` upstream dying of a
// closed pipe is not.
func (p *pipeline) Run() error {
	if len(p.cmds) == 0 {
		return errors.New("empty pipeline")
	}
	last := len(p.cmds) - 1
	// Every command writes stderr to the same place. A file is shared by
	// the children themselves; anything else is copied in by one goroutine
	// per command, which must take turns.
	stderr := p.stderr
	if _, ok := stderr.(*os.File); !ok && stderr != nil {
		stderr = &lockedWriter{w: stderr}
	}
	var ends []*os.File
	closeEnds := func() {
		for _, f := range ends {
			_ = f.Close() //nolint:errcheck // a pipe end; nothing to recover
		}
	}
	for i, c := range p.cmds {
		c.Stderr = stderr
		if i == 0 {
			c.Stdin = p.stdin
		}
		if i == last {
			c.Stdout = p.stdout
			continue
		}
		r, w, err := os.Pipe()
		if err != nil {
			closeEnds()
			return err
		}
		ends = append(ends, r, w)
		c.Stdout, p.cmds[i+1].Stdin = w, r
	}
	started := 0
	var err error
	for _, c := range p.cmds {
		if err = c.Start(); err != nil {
			break
		}
		started++
	}
	// The children hold their own copies of the pipe ends now; the parent's
	// must close, or a reader never sees end of file.
	closeEnds()
	if err != nil {
		for _, c := range p.cmds[:started] {
			_ = c.Process.Kill() //nolint:errcheck // the pipeline already failed to start
			_ = c.Wait()         //nolint:errcheck // reaping only
		}
		return err
	}
	errs := make([]error, len(p.cmds))
	for i, c := range p.cmds {
		errs[i] = c.Wait()
	}
	return errs[last]
}

// lockedWriter serializes writes to w.
type lockedWriter struct {
	w  io.Writer
	mu sync.Mutex
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}
