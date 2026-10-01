package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
	"github.com/blairham/dockyard/internal/tui/views"
)

func TestRefreshRateOption(t *testing.T) {
	if got := NewApp(nil, Options{}).refresh; got != pollInterval {
		t.Errorf("default refresh = %v, want %v", got, pollInterval)
	}
	if got := NewApp(nil, Options{RefreshRate: 7 * time.Second}).refresh; got != 7*time.Second {
		t.Errorf("refresh = %v, want 7s", got)
	}
}

// TestLogOptionsReachTheLogsView drives a log drill-in and checks the
// config's logger block arrived at the view the action built.
func TestLogOptionsReachTheLogsView(t *testing.T) {
	for _, tc := range []struct {
		name     string
		opts     Options
		wantTail int
		wantTS   bool
	}{
		{name: "defaults", opts: Options{}, wantTail: 500, wantTS: false},
		{name: "configured", opts: Options{LogTail: 42, LogShowTime: true}, wantTail: 42, wantTS: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewApp(nil, tc.opts)
			a.handleAction("logs", "abc")
			lv := typedView[*views.LogsView](a, style.ViewLogs)
			if lv.TailLines() != tc.wantTail || lv.Timestamps() != tc.wantTS {
				t.Errorf("logs view tail=%d timestamps=%v, want %d %v",
					lv.TailLines(), lv.Timestamps(), tc.wantTail, tc.wantTS)
			}
		})
	}
}

// TestTickUsesTheRefreshRate runs the tick command itself: a field that
// is set but never read by the tick would pass the test above.
func TestTickUsesTheRefreshRate(t *testing.T) {
	a := NewApp(nil, Options{RefreshRate: 10 * time.Millisecond})
	start := time.Now()
	if _, ok := a.tick()().(tickMsg); !ok {
		t.Fatal("tick did not produce a tickMsg")
	}
	if el := time.Since(start); el >= pollInterval {
		t.Errorf("tick took %v; the 10ms refresh rate was ignored", el)
	}
}

// TestRequestTimeoutSurvivesAContextSwitch: the switch dials a fresh
// client, which must inherit --request-timeout rather than reverting to
// the per-call defaults.
func TestRequestTimeoutSurvivesAContextSwitch(t *testing.T) {
	a := NewApp(nil, Options{RequestTimeout: 9 * time.Second})
	c, err := docker.New("unix:///nonexistent/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	a.applySwitchContext(switchContextMsg{name: "other", client: c})
	if a.client.RequestTimeout != 9*time.Second {
		t.Errorf("switched client RequestTimeout = %v, want 9s", a.client.RequestTimeout)
	}
}

// TestDaemonRequestsHonorRequestTimeout guards the views against a
// hand-rolled deadline: every daemon request goes through
// Client.RequestContext so --request-timeout reaches it. The allowed
// files time CLI calls (colima, podman), not daemon requests.
func TestDaemonRequestsHonorRequestTimeout(t *testing.T) {
	allowed := map[string]int{
		"runtimes.go": 1, // runtime CLIs
		"pods.go":     1, // podman CLI
		"inspect.go":  1, // the fetch-func branch: runtime CLIs
	}
	files, err := filepath.Glob("views/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no view sources found: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		n := strings.Count(string(b), "context.WithTimeout(")
		if want := allowed[filepath.Base(f)]; n != want {
			t.Errorf("%s has %d context.WithTimeout calls, want %d — use client.RequestContext", f, n, want)
		}
	}
}
