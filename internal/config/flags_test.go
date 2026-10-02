// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestApplyFlagsOnlySetFlagsWin pins the precedence: a flag left unset
// keeps the file's value, and one set explicitly beats it either way.
func TestApplyFlagsOnlySetFlagsWin(t *testing.T) {
	file := Default()
	file.ReadOnly, file.UI.Logoless, file.DefaultView = true, true, "images"

	got := ApplyFlags(file, map[string]bool{}, FlagValues{})
	if !got.ReadOnly || !got.UI.Logoless || got.DefaultView != "images" {
		t.Errorf("unset flags overwrote the file: %+v", got)
	}

	got = ApplyFlags(file, map[string]bool{"readonly": true, "headless": true, "c": true},
		FlagValues{ReadOnly: false, Headless: true, Command: "volumes"})
	if got.ReadOnly {
		t.Error("--readonly=false did not override readOnly: true")
	}
	if !got.UI.Headless || got.DefaultView != "volumes" || !got.UI.Logoless {
		t.Errorf("set flags not applied, or unset ones disturbed: %+v", got)
	}
}

func TestApplyFlagsRefreshAndTimeout(t *testing.T) {
	file := Default()
	file.RefreshRate, file.RequestTimeout = 9, time.Minute
	if got := ApplyFlags(
		file,
		map[string]bool{},
		FlagValues{Refresh: 0},
	); got.RefreshRate != 9 ||
		got.RequestTimeout != time.Minute {
		t.Errorf("unset -r / --request-timeout overwrote the file: %+v", got)
	}
	for _, name := range []string{"r", "refresh"} {
		got := ApplyFlags(file, map[string]bool{name: true, "request-timeout": true},
			FlagValues{Refresh: 1, RequestTimeout: 5 * time.Second})
		if got.RefreshRate != 1 || got.RequestTimeout != 5*time.Second {
			t.Errorf("-%s: %+v", name, got)
		}
	}
}

func TestConfigInitWritesTheSampleOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	t.Setenv(EnvDir, dir)

	var out bytes.Buffer
	if err := Command(
		[]string{"path"},
		&out,
	); err != nil ||
		strings.TrimSpace(out.String()) != filepath.Join(dir, FileName) {
		t.Fatalf("config path = %q, %v", out.String(), err)
	}
	if err := Command([]string{"init"}, &out); err != nil {
		t.Fatalf("config init: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil || string(b) != Sample {
		t.Fatalf("init wrote %q, %v", b, err)
	}
	if err := Command([]string{"init"}, &out); err == nil {
		t.Error("a second init overwrote the existing file")
	}
	if err := Command(nil, &out); err == nil {
		t.Error("bare `config` should print usage as an error")
	}
}
