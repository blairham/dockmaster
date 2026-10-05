// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadJumps: jumps.yaml decodes by source view; no file is no jumps,
// and an unknown key — k9s's targetGVR, say — is an error naming it.
func TestLoadJumps(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	js, err := LoadJumps()
	if err != nil || js != nil {
		t.Fatalf("no file: %v %v", js, err)
	}
	write := func(body string) {
		t.Helper()
		if werr := os.WriteFile(filepath.Join(dir, JumpsFileName), []byte(body), 0o600); werr != nil {
			t.Fatal(werr)
		}
	}
	write(`jumps:
  containers:
    targetView: volumes
    labelSelector: com.docker.compose.project=$PROJECT
  images:
    targetView: containers
    filter: $IMAGE
`)
	js, err = LoadJumps()
	if err != nil {
		t.Fatal(err)
	}
	if j := js["containers"]; j.TargetView != "volumes" || j.LabelSelector != "com.docker.compose.project=$PROJECT" {
		t.Errorf("containers: %+v", j)
	}
	if j := js["images"]; j.TargetView != "containers" || j.Filter != "$IMAGE" {
		t.Errorf("images: %+v", j)
	}
	write("jumps:\n  images:\n    targetGVR: v1/pods\n")
	if _, err := LoadJumps(); err == nil || !strings.Contains(err.Error(), "targetGVR") {
		t.Errorf("an unknown key was accepted: %v", err)
	}
}
