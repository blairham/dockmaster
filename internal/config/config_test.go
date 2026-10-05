// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseEmptyIsDefault(t *testing.T) {
	for _, in := range []string{"", "dockmaster:\n", "# only a comment\n"} {
		got, err := Parse(strings.NewReader(in))
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if !reflect.DeepEqual(got, Default()) {
			t.Errorf("Parse(%q) = %+v, want defaults %+v", in, got, Default())
		}
	}
}

func TestParseOverlaysDefaults(t *testing.T) {
	got, err := Parse(strings.NewReader(`dockmaster:
  refreshRate: 7
  requestTimeout: 90s
  readOnly: true
  defaultView: images
  context: colima
  ui:
    logoless: true
  thresholds:
    cpu:
      warn: 50
  logger:
    showTime: true
`))
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.RequestTimeout = 90 * time.Second
	want.RefreshRate, want.ReadOnly, want.DefaultView, want.Context = 7, true, "images", "colima"
	want.UI.Logoless, want.Logger.ShowTime = true, true
	want.Thresholds.CPU.Warn = 50 // critical keeps its default
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if got.Logger.Tail != DefaultLogTail {
		t.Errorf("an unset logger.tail lost its default: %d", got.Logger.Tail)
	}
}

// TestParseRejectsUnknownKeys pins the strict decode: a misspelled key
// must fail loudly, not be ignored as though it took effect.
func TestParseRejectsUnknownKeys(t *testing.T) {
	for _, in := range []string{
		"dockmaster:\n  readonly: true\n", // k9s spells it readOnly
		"dockmaster:\n  ui:\n    logoLess: true\n",
		"k9s:\n  refreshRate: 2\n",
	} {
		_, err := Parse(strings.NewReader(in))
		if err == nil {
			t.Errorf("Parse(%q) accepted an unknown key", in)
		} else if !strings.Contains(err.Error(), "unknown key") || strings.Contains(err.Error(), "type ") {
			t.Errorf("Parse(%q) error %q should say unknown key, without Go type names", in, err)
		}
	}
}

func TestParseRejectsBadValues(t *testing.T) {
	for in, want := range map[string]string{
		"dockmaster:\n  thresholds:\n    cpu:\n      warn: 90\n      critical: 70\n": "thresholds.cpu",
		"dockmaster:\n  thresholds:\n    memory:\n      critical: 101\n":             "thresholds.memory",
		"dockmaster:\n  refreshRate: 0\n":                                            "refreshRate",
		"dockmaster:\n  requestTimeout: -1s\n":                                       "requestTimeout",
		"dockmaster:\n  requestTimeout: soon\n":                                      "line 2",
		"dockmaster:\n  logger:\n    tail: 0\n":                                      "logger.tail",
		"dockmaster:\n  logger:\n    tail: -5\n":                                     "logger.tail",
		"dockmaster:\n  refreshRate: fast\n":                                         "line 2",
		"dockmaster:\n  logger:\n    tail: 1e9\n":                                    "tail",
		"dockmaster:\n  imageScans:\n    ttl: 0s\n":                                  "imageScans.ttl",
		"dockmaster:\n  imageScans:\n    ttl: 30s\n":                                 "imageScans.ttl",
		"dockmaster:\n  imageScans:\n    background: true\n":                         "imageScans.background needs",
	} {
		_, err := Parse(strings.NewReader(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) error = %v, want one naming %s", in, err, want)
		}
	}
}

// TestImageScans: imageScans is off by default with a week's TTL, and
// takes a duration and background with enable.
func TestImageScans(t *testing.T) {
	if d := Default().ImageScans; d.Enable || d.Background || d.TTL != 168*time.Hour {
		t.Errorf("default imageScans %+v", d)
	}
	got, err := Parse(
		strings.NewReader("dockmaster:\n  imageScans:\n    enable: true\n    background: true\n    ttl: 24h\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := (ImageScans{Enable: true, Background: true, TTL: 24 * time.Hour}); got.ImageScans != want {
		t.Errorf("imageScans %+v, want %+v", got.ImageScans, want)
	}
}

// TestSampleIsTheDefaults keeps the commented sample honest: it must
// parse, and every value in it must be the default it documents.
func TestSampleIsTheDefaults(t *testing.T) {
	got, err := Parse(strings.NewReader(Sample))
	if err != nil {
		t.Fatalf("Sample does not parse: %v", err)
	}
	// Exactly the defaults: defaultView stays empty, because an empty one
	// opens the view last used on the context and "containers" would not.
	want := Default()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sample = %+v\nwant %+v", got, want)
	}
}

func TestLoadMissingFileIsDefault(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope", FileName))
	if err != nil || !reflect.DeepEqual(got, Default()) {
		t.Errorf("Load(missing) = %+v, %v", got, err)
	}
}

func TestLoadNamesTheFileInErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(p, []byte("dockmaster:\n  bogus: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), p) {
		t.Errorf("Load error %v does not name %s", err, p)
	}
}

func TestDirPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvDir, "")
	t.Setenv("XDG_CONFIG_HOME", "")
	check := func(want string) {
		t.Helper()
		if got, err := Dir(); err != nil || got != want {
			t.Errorf("Dir() = %q, %v; want %q", got, err, want)
		}
	}
	check(filepath.Join(home, ".config", "dockmaster"))
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	check(filepath.Join("/xdg", "dockmaster"))
	t.Setenv(EnvDir, "/explicit")
	check("/explicit")
}

// TestHostShellImage: hostShell.image decodes, defaults to an image whose
// busybox has nsenter, and refuses an empty value or one docker would read
// as a flag (#59).
func TestHostShellImage(t *testing.T) {
	if got := Default().HostShell.Image; got != "alpine:3" {
		t.Errorf("default hostShell.image = %q", got)
	}
	cfg, err := Parse(strings.NewReader("dockmaster:\n  hostShell:\n    image: registry.local/tools:1\n"))
	if err != nil || cfg.HostShell.Image != "registry.local/tools:1" {
		t.Errorf("hostShell.image decoded as %q, %v", cfg.HostShell.Image, err)
	}
	cfg, err = Parse(strings.NewReader("dockmaster:\n  shell: zsh\n"))
	if err != nil || cfg.HostShell.Image != DefaultHostShellImage {
		t.Errorf("a file without hostShell gave image %q, %v", cfg.HostShell.Image, err)
	}
	for _, bad := range []string{`""`, `"  "`, `"--privileged"`, `"-v/:/host"`} {
		_, err := Parse(strings.NewReader("dockmaster:\n  hostShell:\n    image: " + bad + "\n"))
		if err == nil || !strings.Contains(err.Error(), "hostShell.image") {
			t.Errorf("hostShell.image %s: err %v", bad, err)
		}
	}
	if _, err := Parse(strings.NewReader("dockmaster:\n  hostShell:\n    imag: x\n")); err == nil {
		t.Error("an unknown key under hostShell was accepted")
	}
	if !strings.Contains(Sample, "hostShell:\n    image: "+DefaultHostShellImage+"\n") {
		t.Error("Sample does not document hostShell.image")
	}
}
