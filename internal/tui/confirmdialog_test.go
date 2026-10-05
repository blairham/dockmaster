// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
)

// The confirm is k9s's dialog: it floats over the table rather than
// taking rows from it, and focus starts on Cancel, so the enter that
// drills into a row can never remove one.
func TestConfirmDialogStartsOnCancel(t *testing.T) {
	a, calls := markedApp(t, Options{Version: "test"})
	a.flash, a.errFlash = "", "" // a keypress clears a flash, which frees a row
	h := a.tableHeight()

	step(a, key("ctrl+d"))
	if !a.confirm.Active() {
		t.Fatal("ctrl-d did not open the dialog")
	}
	if got := a.tableHeight(); got != h {
		t.Errorf("the dialog took rows from the table: %d -> %d", h, got)
	}
	out := render(a)
	for _, want := range []string{"<Remove>", "Cancel", "OK", "migrate"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered frame lacks %q:\n%s", want, out)
		}
	}

	runOnce(a, step(a, key("enter")))
	if a.confirm.Active() || len(calls()) != 0 {
		t.Fatalf("enter on the fresh dialog: open %v, ran %q", a.confirm.Active(), calls())
	}

	step(a, key("ctrl+d"))
	step(a, key("tab"))
	runOnce(a, step(a, key("enter")))
	if got := strings.Count(strings.Join(calls(), "|"), "DELETE"); got != 2 {
		t.Errorf("tab then enter should press OK and remove both marked: %q", calls())
	}
}

func TestConfirmTitles(t *testing.T) {
	for action, want := range map[string]string{
		"kill":             "Kill",
		"remove_container": "Remove",
		"remove_dump":      "Delete",
		"prune_images":     "Prune",
		"runtime_restart":  "Restart",
		"runtime_k8s":      "Kubernetes",
		"compose_down":     "Compose Down",
		"host_shell":       "Host Shell",
		"runtime_apply":    "Confirm",
	} {
		if got := confirmTitle(action); got != want {
			t.Errorf("confirmTitle(%q) = %q, want %q", action, got, want)
		}
	}
}
