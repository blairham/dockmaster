package views

import (
	"strings"
	"testing"
)

func TestVolumeFileText(t *testing.T) {
	if got := volumeFileText([]byte("PK\x03\x04\x00\x00"), false); !strings.Contains(got, "binary file") {
		t.Errorf("binary: %q", got)
	}
	got := volumeFileText([]byte("a\nb\n"), true)
	if !strings.HasPrefix(got, "a\nb\n") || !strings.Contains(got, "first 4B shown") {
		t.Errorf("cut text: %q", got)
	}
}
