package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAliases(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	if a, err := LoadAliases(); err != nil || a != nil {
		t.Fatalf("no file: %v %v", a, err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, AliasesFileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("aliases:\n  pg: containers /Postgres\n")
	if a, err := LoadAliases(); err != nil || a["pg"] != "containers /Postgres" {
		t.Errorf("file: %v %v", a, err)
	}
	write("alias:\n  pg: containers\n")
	if _, err := LoadAliases(); err == nil || !strings.Contains(err.Error(), "alias") {
		t.Errorf("an unknown key was accepted: %v", err)
	}
}
