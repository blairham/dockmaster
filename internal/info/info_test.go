// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package info

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points every directory info reads at temp ones — config, state,
// the docker CLI's config and context store — and clears the endpoint
// variables, so nothing of the user's is read. It returns the docker
// config dir.
func isolate(t *testing.T) (cfgDir, stateDir, dockerDir string) {
	t.Helper()
	cfgDir, stateDir, dockerDir = t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("DOCKMASTER_CONFIG_DIR", cfgDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("DOCKER_CONFIG", dockerDir)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	return cfgDir, stateDir, dockerDir
}

// addContext writes a context to the store as the docker CLI does.
func addContext(t *testing.T, dockerDir, name, host string) {
	t.Helper()
	sum := sha256.Sum256([]byte(name))
	dir := filepath.Join(dockerDir, "contexts", "meta", hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := `{"Name":"` + name + `","Endpoints":{"docker":{"Host":"` + host + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestInfoPaths: every path follows the env overrides, and nothing needs
// a daemon — the endpoints here would refuse a dial.
func TestInfoPaths(t *testing.T) {
	cfgDir, stateDir, _ := isolate(t)
	var out bytes.Buffer
	if err := Command(nil, &out); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(stateDir, "dockmaster")
	for _, want := range []string{
		"Config dir:  " + cfgDir + "\n",
		"Config file: " + filepath.Join(cfgDir, "config.yaml") + " (not present",
		"Skins dir:   " + filepath.Join(cfgDir, "skins") + "\n",
		"State dir:   " + state + "\n",
		"Dumps:       " + filepath.Join(state, "dumps") + "\n",
		"Saved logs:  " + filepath.Join(state, "logs") + "\n",
		"Log file:    " + filepath.Join(state, "dockmaster.log") + "\n",
		"History:     " + filepath.Join(state, "history.json") + "\n",
		"Context:     default\n",
		"Resolved by: default\n",
		"Version:     ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("info lacks %q:\n%s", want, out.String())
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Errorf("info created the state dir: %v", err)
	}

	// screenDumpDir moves the dumps and saved logs, and --log-file the log.
	dumps := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"),
		[]byte("dockmaster:\n  screenDumpDir: "+dumps+"\n  context: nowhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Command([]string{"--log-file", "/var/tmp/dm.log"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Config file: " + filepath.Join(cfgDir, "config.yaml") + "\n",
		"Dumps:       " + filepath.Join(dumps, "dumps") + "\n",
		"Saved logs:  " + filepath.Join(dumps, "logs") + "\n",
		"Log file:    /var/tmp/dm.log\n",
		"Context:     nowhere\n",
		"Endpoint:    (unknown)\n",
		"Resolved by: context (config.yaml) — no such context in the store\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("with config.yaml, info lacks %q:\n%s", want, out.String())
		}
	}

	if err := Command([]string{"extra"}, &out); err == nil {
		t.Error("info took a stray argument")
	}
}

// TestInfoResolvesTheEndpoint: each step of the precedence, and what says
// which one it was.
func TestInfoResolvesTheEndpoint(t *testing.T) {
	_, _, dockerDir := isolate(t)
	addContext(t, dockerDir, "colima", "unix:///colima.sock")
	addContext(t, dockerDir, "remote", "tcp://192.0.2.7:2376")
	addContext(t, dockerDir, "fromcfg", "unix:///cfg.sock")
	if err := os.WriteFile(
		filepath.Join(dockerDir, "config.json"),
		[]byte(`{"currentContext":"colima"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	type res struct{ name, host, how string }
	check := func(what string, o Options, cfgCtx string, want res) {
		t.Helper()
		n, h, how := Endpoint(o, cfgCtx)
		if (res{n, h, how}) != want {
			t.Errorf("%s: (%q, %q, %q), want %+v", what, n, h, how, want)
		}
	}
	check(
		"currentContext",
		Options{},
		"",
		res{"colima", "unix:///colima.sock", "context (the docker CLI's current context)"},
	)
	check("config.yaml", Options{}, "fromcfg", res{"fromcfg", "unix:///cfg.sock", "context (config.yaml)"})
	t.Setenv("DOCKER_CONTEXT", "remote")
	check("$DOCKER_CONTEXT", Options{}, "fromcfg", res{"remote", "tcp://192.0.2.7:2376", "env ($DOCKER_CONTEXT)"})
	t.Setenv("DOCKER_HOST", "tcp://192.0.2.9:2375")
	check("$DOCKER_HOST", Options{}, "fromcfg", res{"", "tcp://192.0.2.9:2375", "env ($DOCKER_HOST)"})
	check("--context", Options{Context: "colima"}, "", res{"colima", "unix:///colima.sock", "explicit (--context)"})
	check("--host", Options{Host: "ssh://me@box", Context: "colima"}, "", res{"", "ssh://me@box", "explicit (--host)"})
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	if err := os.WriteFile(filepath.Join(dockerDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	n, h, how := Endpoint(Options{}, "")
	if n != "default" || how != "default" || !strings.HasPrefix(h, "unix://") {
		t.Errorf("no context at all: (%q, %q, %q)", n, h, how)
	}
}

// TestInfoPrintsNoSecret: a password in an endpoint is masked, and no
// other variable's value is printed.
func TestInfoPrintsNoSecret(t *testing.T) {
	isolate(t)
	t.Setenv("DOCKER_HOST", "tcp://admin:hunter2@192.0.2.1:2375")
	t.Setenv("DOCKER_CERT_PATH", "/secret/certs")
	t.Setenv("DOCKMASTER_SKIN", "secret-skin")
	var out bytes.Buffer
	if err := Command(nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Endpoint:    tcp://admin:xxxxx@192.0.2.1:2375\n") {
		t.Errorf("endpoint not shown masked:\n%s", got)
	}
	for _, leak := range []string{"hunter2", "/secret/certs", "secret-skin"} {
		if strings.Contains(got, leak) {
			t.Errorf("info printed %q:\n%s", leak, got)
		}
	}
	if redact("ssh://me@box") != "ssh://me@box" || redact("unix:///x.sock") != "unix:///x.sock" {
		t.Error("redact changed an endpoint with no password")
	}
}
