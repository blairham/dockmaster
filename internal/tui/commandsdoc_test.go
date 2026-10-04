// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"strings"
	"testing"
)

// TestCommandsDocCoversEveryCommand: docs/commands.md names every `:`
// command and every spelling the palette accepts, so the reference cannot
// fall behind the code.
func TestCommandsDocCoversEveryCommand(t *testing.T) {
	b, err := os.ReadFile("../../docs/commands.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	names := append([]string{}, knownCommands...)
	for name := range viewCommands {
		names = append(names, name)
	}
	// Spellings dispatchCommand takes that are in neither list.
	names = append(names, "x", "h", "?", "alias", "aliases", "log", "screendumps", "dumps",
		"prune build", "prune builder", "prune all -v", "prune all --volumes")
	for _, n := range names {
		if !strings.Contains(doc, "`:"+n+"`") && !strings.Contains(doc, "`:"+n+" ") {
			t.Errorf("docs/commands.md does not name :%s", n)
		}
	}
}
