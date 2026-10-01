package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
	"github.com/blairham/dockyard/internal/tui/views"
)

type fakeCompose struct {
	fail  error
	calls []string
	mu    sync.Mutex
}

func (f *fakeCompose) run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	return nil, f.fail
}

func (f *fakeCompose) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

// newComposeApp opens the projects view on one project, "shop", whose
// compose file exists when present is true and points nowhere otherwise.
func newComposeApp(t *testing.T, opts Options, present bool) (*App, *fakeCompose, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	if present {
		if err := os.WriteFile(file, []byte("services: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	labels := map[string]string{
		docker.LabelProject: "shop", docker.LabelService: "web",
		docker.LabelConfigFiles: file, docker.LabelWorkingDir: dir,
	}
	a := newSizedApp(t, opts, 200, 30)
	f := &fakeCompose{}
	a.composeRunner = f.run
	a.splashActive = false
	step(a, key("4"))
	step(a, views.ProjectsRefreshMsg{Containers: []docker.Container{
		{ID: "a1", Name: "shop-web-1", State: "exited", Project: "shop", Service: "web", Labels: labels},
	}})
	return a, f, dir
}

func TestComposeUpRunsCompose(t *testing.T) {
	a, f, dir := newComposeApp(t, Options{}, true)
	if out := render(a); !strings.Contains(out, "compose.yaml") {
		t.Errorf("compose file not shown:\n%s", out)
	}
	cmd := step(a, key("u"))
	if out := render(a); !strings.Contains(out, "starting…") {
		t.Errorf("row does not show the up in progress:\n%s", out)
	}
	runOnce(a, cmd)
	want := "compose -p shop --project-directory " + dir + " -f " + filepath.Join(dir, "compose.yaml") + " up -d"
	if got := f.last(); got != want {
		t.Errorf("ran %q\nwant %q", got, want)
	}
	if strings.Contains(render(a), "starting…") {
		t.Error("busy marker survived")
	}
	if a.flash != "up shop" {
		t.Errorf("flash = %q", a.flash)
	}
}

func TestComposeRestartAndPull(t *testing.T) {
	a, f, _ := newComposeApp(t, Options{}, true)
	runOnce(a, step(a, key("R")))
	if !strings.HasSuffix(f.last(), " restart") {
		t.Errorf("R ran %q", f.last())
	}
	runOnce(a, step(a, key("p")))
	if !strings.HasSuffix(f.last(), " pull") {
		t.Errorf("p ran %q", f.last())
	}
}

// TestComposeDownConfirms: down removes containers and networks, so it
// asks first, says volumes survive, and declining runs nothing.
func TestComposeDownConfirms(t *testing.T) {
	a, f, _ := newComposeApp(t, Options{}, true)
	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "compose down shop") ||
		!strings.Contains(a.confirm.Prompt(), "volumes are kept") {
		t.Fatalf("confirm = %v %q", a.confirm.Active(), a.confirm.Prompt())
	}
	step(a, key("n"))
	if len(f.calls) != 0 {
		t.Fatalf("declined down ran %q", f.calls)
	}
	step(a, key("ctrl+d"))
	runOnce(a, step(a, key("y")))
	if !strings.HasSuffix(f.last(), " down") {
		t.Errorf("confirmed down ran %q", f.last())
	}
}

// TestComposeFallsBackWithoutFiles: with the compose file gone, u starts the
// existing containers, pull refuses with the reason, and ctrl-d asks to
// force-remove — and none of them runs compose against a file that is not
// there.
func TestComposeFallsBackWithoutFiles(t *testing.T) {
	a, f, _ := newComposeApp(t, Options{}, false)
	if out := render(a); !strings.Contains(out, "not on this machine") {
		t.Errorf("missing file not shown:\n%s", out)
	}
	if cmd := step(a, key("u")); cmd == nil || !strings.Contains(a.flash, "starting the existing containers") {
		t.Errorf("u did not fall back (cmd %v, flash %q)", cmd != nil, a.flash)
	}
	step(a, key("p"))
	if !strings.Contains(a.errFlash, "cannot pull shop") {
		t.Errorf("pull errFlash = %q", a.errFlash)
	}
	step(a, key("ctrl+d"))
	if !strings.Contains(a.confirm.Prompt(), "force-remove every container in shop") {
		t.Errorf("fallback confirm = %q", a.confirm.Prompt())
	}
	step(a, key("n"))
	if len(f.calls) != 0 {
		t.Errorf("compose ran without its files: %q", f.calls)
	}
}

func TestComposeFailureSurfacesTheReason(t *testing.T) {
	a, f, _ := newComposeApp(t, Options{}, true)
	f.fail = &docker.ComposeError{
		Stderr: "Error response from daemon: port is already allocated\n",
		Err:    errors.New("exit 1"),
	}
	runOnce(a, step(a, key("u")))
	if a.errFlash != "compose: Error response from daemon: port is already allocated" {
		t.Errorf("errFlash = %q", a.errFlash)
	}
	if pv := typedView[*views.ProjectsView](a, style.ViewProjects); pv.Busy("shop") != "" {
		t.Error("a failed up left the project busy")
	}
}

func TestComposeReadonly(t *testing.T) {
	a, f, _ := newComposeApp(t, Options{ReadOnly: true}, true)
	for _, k := range []string{"u", "R", "p", "ctrl+d"} {
		a.errFlash = ""
		runOnce(a, step(a, key(k)))
		if !strings.Contains(a.errFlash, "readonly") || a.confirm.Active() {
			t.Errorf("%s not refused in readonly (flash %q)", k, a.errFlash)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("readonly ran compose: %q", f.calls)
	}
}

// runOnce runs a command and feeds back its message, without following the
// commands that produces: after a compose verb the app refreshes the project
// list, which would dial the (absent) test daemon.
func runOnce(a *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch m := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range m {
			runOnce(a, c)
		}
	case nil:
	default:
		step(a, m)
	}
}
