// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
)

// TestPullKeepsTheReferenceCase: a tag may hold capitals, so :pull passes
// the reference as typed, whatever the case of the command word (#52).
func TestPullKeepsTheReferenceCase(t *testing.T) {
	for _, in := range []string{"pull app:RC1", "PULL app:RC1", "Pull  app:RC1 "} {
		a := newTestApp(t)
		c, err := docker.New("unix:///nonexistent/docker.sock")
		if err != nil {
			t.Fatal(err)
		}
		a.client = c
		errMsg, cmd := a.dispatchCommand(in)
		if errMsg != "" || cmd == nil {
			t.Fatalf("%q: err %q, cmd %v", in, errMsg, cmd)
		}
		done, ok := cmd().(actionDoneMsg)
		if !ok || done.subject != "app:RC1" {
			t.Errorf("%q pulled %q, want app:RC1", in, done.subject)
		}
	}
}
