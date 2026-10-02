// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
)

// FuzzParse: config.yaml is read before the UI starts, so a malformed file
// must come back as an error, never a panic or a hang.
func FuzzParse(f *testing.F) {
	f.Add(Sample)
	for _, s := range []string{
		"",
		"dockmaster:\n  ui:\n    skin: x\n",
		"dockmaster:\n  logger:\n    tail: -1\n",
		"dockmaster: [",
		"unknown: 1\n",
		"dockmaster:\n  refreshRate: 1e309\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if _, err := Parse(strings.NewReader(s)); err != nil {
			t.Skip("rejected, as it should be")
		}
	})
}
