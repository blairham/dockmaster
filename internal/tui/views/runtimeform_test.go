package views

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockyard/internal/engines"
)

var sgrStrip = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// closingBrackets is the column of each input box's "]" in a rendered form.
func closingBrackets(t *testing.T, out string) []int {
	t.Helper()
	var cols []int
	for _, line := range strings.Split(sgrStrip.ReplaceAllString(out, ""), "\n") {
		if strings.Contains(line, "[") {
			cols = append(cols, len([]rune(line[:strings.Index(line, "]")])))
		}
	}
	return cols
}

// TestFormBracketsLineUp: every input box is the same width, so the
// brackets line up down the form whatever each field's limit is.
func TestFormBracketsLineUp(t *testing.T) {
	hostMemoryGiB = func() int { return 64 }
	t.Cleanup(func() { hostMemoryGiB = func() int { return int(engines.HostMemory() >> 30) } })

	for name, f := range map[string]*RuntimeFormView{
		"create": NewRuntimeCreateForm("colima", []engines.Provider{engines.Colima{}}, nil),
		"edit": NewRuntimeEditForm(engines.Machine{
			Provider: "colima", Name: "default", Running: true, Runtime: "docker",
			CPUs: 14, Memory: 60 << 30, Disk: 100 << 30,
		}, engines.Colima{}.Caps()),
	} {
		cols := closingBrackets(t, f.View())
		if len(cols) < 3 {
			t.Fatalf("%s: found %d input boxes", name, len(cols))
		}
		for _, c := range cols[1:] {
			if c != cols[0] {
				t.Errorf("%s: closing brackets at columns %v, want one column", name, cols)
				break
			}
		}
		if !strings.Contains(f.View(), "this machine has 64 GiB") {
			t.Errorf("%s: no memory hint", name)
		}
	}
}

// TestFormRefusesMoreMemoryThanTheHost: like CPUs, memory is checked
// against what this machine has.
func TestFormRefusesMoreMemoryThanTheHost(t *testing.T) {
	hostMemoryGiB = func() int { return 64 }
	t.Cleanup(func() { hostMemoryGiB = func() int { return int(engines.HostMemory() >> 30) } })

	f := NewRuntimeEditForm(
		engines.Machine{Provider: "colima", Name: "default", CPUs: 4, Memory: 8 << 30, Disk: 100 << 30},
		engines.Colima{}.Caps(),
	)
	f.UpdateTable(tea.KeyPressMsg{Code: tea.KeyTab}) // Memory
	for range 3 {
		f.UpdateTable(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range "96" {
		f.UpdateTable(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if action, _ := f.HandleKey(KeyEnter); action != "" || !strings.Contains(f.View(), "Memory: this machine has 64 GiB") {
		t.Errorf("96 GiB on a 64 GiB host: action %q\n%s", action, sgrStrip.ReplaceAllString(f.View(), ""))
	}
}
