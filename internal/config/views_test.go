// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadViews(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	if v, err := LoadViews(); err != nil || v != nil {
		t.Fatalf("no file: %v %v", v, err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, ViewsFileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(
		"views:\n  containers:\n    columns: [NAME, STATE]\n    sortColumn: AGE:desc\n  images:\n    columns:\n      - TAG\n",
	)
	v, err := LoadViews()
	if err != nil || !slices.Equal(v["containers"].Columns, []string{"NAME", "STATE"}) ||
		v["containers"].SortColumn != "AGE:desc" || !slices.Equal(v["images"].Columns, []string{"TAG"}) {
		t.Errorf("file: %+v %v", v, err)
	}
	write("views:\n  containers:\n    sortcolumn: AGE\n")
	if _, err := LoadViews(); err == nil || !strings.Contains(err.Error(), "sortcolumn") {
		t.Errorf("a misspelled key was accepted: %v", err)
	}
	write("view:\n  containers: {}\n")
	if _, err := LoadViews(); err == nil || !strings.Contains(err.Error(), "view") {
		t.Errorf("a misspelled root was accepted: %v", err)
	}
}
