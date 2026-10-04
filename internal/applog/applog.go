// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package applog is dockmaster's own log (#60): what would otherwise be
// flashed once and lost — errors, plugin and hotkey runs, context switches
// — as log/slog text lines in <state>/dockmaster.log. No bubbletea here.
//
// The file is opened append-only, 0600, in a directory created 0700, and
// is rotated once to <file>.1 when it reaches MaxSize, so it never grows
// past about twice that.
package applog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blairham/dockmaster/internal/config"
)

// FileName is the log file inside the state directory.
const FileName = "dockmaster.log"

// DefaultLevel is the level with no --log-level: warnings and errors.
const DefaultLevel = "warn"

// MaxSize is where the log rotates to <file>.1.
const MaxSize = 5 << 20

// Levels are the names --log-level takes.
var Levels = []string{"debug", "info", "warn", "error"}

// ParseLevel reads a --log-level name.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q — one of: %s", name, strings.Join(Levels, ", "))
}

// Path is the log file: override when set, else <state>/dockmaster.log.
func Path(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	dir, err := config.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Discard is a logger that writes nothing.
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// Open opens the log at path (Path's default when ""), at level, and
// returns the logger and what closes the file.
func Open(path, level string) (*slog.Logger, io.Closer, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, nil, err
	}
	if path, err = Path(path); err != nil {
		return nil, nil, err
	}
	f, err := openFile(path, MaxSize)
	if err != nil {
		return nil, nil, err
	}
	return slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: lvl})), f, nil
}

// file is the log file, rotated once to <path>.1 when a write would take
// it past limit.
type file struct {
	f     *os.File
	path  string
	size  int64
	limit int64
	mu    sync.Mutex
}

func openFile(path string, limit int64) (*file, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("log directory: %w", err)
	}
	lf := &file{path: path, limit: limit}
	if err := lf.open(); err != nil {
		return nil, err
	}
	return lf, nil
}

func (lf *file) open() error {
	f, err := os.OpenFile(
		lf.path,
		os.O_WRONLY|os.O_APPEND|os.O_CREATE,
		0o600,
	) //nolint:gosec // the user's own state dir or --log-file
	if err != nil {
		return fmt.Errorf("opening the log: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close() //nolint:errcheck // the stat error is the one to report
		return fmt.Errorf("opening the log: %w", err)
	}
	lf.f, lf.size = f, st.Size()
	return nil
}

// Write appends p, rotating first when it would take the file past limit.
// A rotation that fails keeps writing to the file as it is.
func (lf *file) Write(p []byte) (int, error) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	if lf.size > 0 && lf.size+int64(len(p)) > lf.limit {
		lf.rotate()
	}
	n, err := lf.f.Write(p)
	lf.size += int64(n)
	return n, err
}

func (lf *file) rotate() {
	_ = lf.f.Close()                     //nolint:errcheck // replaced below either way
	_ = os.Rename(lf.path, lf.path+".1") //nolint:errcheck // on failure the file is reopened and grows
	if err := lf.open(); err != nil {
		// Nowhere left to write: drop lines rather than fail the program.
		// A nil *os.File refuses writes with an error; it does not panic.
		lf.f, _ = os.OpenFile(os.DevNull, os.O_WRONLY, 0) //nolint:errcheck // see above
		lf.size = 0
	}
}

// Close closes the file.
func (lf *file) Close() error {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	return lf.f.Close()
}
