package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
	"github.com/blairham/dockyard/internal/tui/views"
)

// newTestApp builds an App past the splash with a known window size. The
// client is nil: every test here drives the views by feeding refresh
// messages directly, so nothing dials a daemon and the tests run in CI
// with no docker at all.
func newTestApp(t *testing.T) *App {
	t.Helper()
	a := NewApp(nil, Options{Version: "test"})
	a.splashActive = false
	a.loading = false
	step(a, tea.WindowSizeMsg{Width: 160, Height: 44})
	return a
}

// step applies one message and discards the command, which is what a test
// wants for everything but the commands it explicitly exercises.
func step(a *App, msg tea.Msg) tea.Cmd {
	_, cmd := a.Update(msg)
	return cmd
}

func key(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+d":
		return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

// ansi matches SGR escape sequences.
var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// render returns the frame with styling stripped.
//
// Stripping is not a convenience — it is required for any assertion on
// text. lipgloss emits a separate escape pair around EVERY CHARACTER of
// an underlined span, so the help overlay's "RESOURCE" header reaches the
// buffer as "\x1b[..mR\x1b[m\x1b[..mE\x1b[m..." and no substring check
// for the word can ever match. The border title has a milder version of
// the same problem: each segment of "containers(all)[4]" is styled
// separately, so "[4]" is split by escapes too.
func render(a *App) string { return ansi.ReplaceAllString(a.View().Content, "") }

// renderStyled returns the frame with styling intact, for the few
// assertions that are about color rather than text.
func renderStyled(a *App) string { return a.View().Content }

// sampleContainers is a fixture spanning the states that render
// differently: running with a healthcheck, running without one, exited,
// and paused.
func sampleContainers() []docker.Container {
	now := time.Now().Add(-3 * time.Hour)
	return []docker.Container{
		{
			ID: "aaaaaaaaaaaa1111", Name: "web", Image: "nginx:1.27", State: "running",
			Status: "Up 3 hours (healthy)", Health: "healthy", Ports: "8080→80/tcp",
			Created: now, Project: "shop", Service: "web",
		},
		{
			ID: "bbbbbbbbbbbb2222", Name: "api", Image: "ghcr.io/acme/api:v2", State: "running",
			Status: "Up 3 hours", Created: now, Project: "shop", Service: "api",
		},
		{
			ID: "cccccccccccc3333", Name: "migrate", Image: "ghcr.io/acme/api:v2", State: "exited",
			Status: "Exited (0) 2 hours ago", Created: now, Project: "shop", Service: "migrate",
		},
		{
			ID: "dddddddddddd4444", Name: "standalone", Image: "redis:7", State: "paused",
			Status: "Up 3 hours (Paused)", Created: now,
		},
	}
}

func loadContainers(a *App) {
	step(a, views.ContainersRefreshMsg{Containers: sampleContainers()})
}

func TestRendersContainersTable(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)

	out := render(a)
	for _, want := range []string{"web", "nginx:1.27", "api", "standalone", "8080→80/tcp", "NAME", "IMAGE", "STATE", "CPU%"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered frame is missing %q", want)
		}
	}
	// The border title carries the resource, the filter, and the count.
	if !strings.Contains(out, "containers") || !strings.Contains(out, "[4]") {
		t.Errorf("border title missing resource/count; got frame:\n%s", out)
	}
}

func TestSplashRendersBeforeFirstFrame(t *testing.T) {
	a := NewApp(nil, Options{Version: "test"})
	step(a, tea.WindowSizeMsg{Width: 120, Height: 40})
	if out := render(a); !strings.Contains(out, "~~~") {
		t.Errorf("splash did not render the logo block")
	}
	step(a, splashDoneMsg{})
	if a.splashActive {
		t.Error("splash still active after splashDoneMsg")
	}
}

func TestDigitKeysSwitchViews(t *testing.T) {
	a := newTestApp(t)
	for _, tc := range []struct {
		key  string
		want style.ViewType
	}{
		{key: "1", want: style.ViewContainers},
		{key: "2", want: style.ViewImages},
		{key: "3", want: style.ViewVolumes},
		{key: "4", want: style.ViewNetworks},
		{key: "5", want: style.ViewProjects},
	} {
		step(a, key(tc.key))
		if a.view != tc.want {
			t.Errorf("key %q: view = %v, want %v", tc.key, a.view, tc.want)
		}
	}
}

func TestFilterNarrowsRows(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)

	step(a, key("/"))
	if !a.filterBar.Active() {
		t.Fatal("/ did not open the filter bar")
	}
	for _, r := range "web" {
		step(a, tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	if got := a.activeView().Count(); got != 1 {
		t.Errorf("filter %q matched %d rows, want 1", a.activeFilter(), got)
	}
	out := render(a)
	if !strings.Contains(out, "web") || strings.Contains(out, "standalone") {
		t.Errorf("filtered frame should show only web:\n%s", out)
	}

	// Esc clears the filter and restores every row.
	step(a, key("esc"))
	step(a, key("esc"))
	if got := a.activeView().Count(); got != 4 {
		t.Errorf("after clearing the filter, %d rows, want 4", got)
	}
}

// TestNegatedFilter pins the `!` negation tuikit's RowFilter provides,
// since it is advertised in the help overlay.
func TestNegatedFilter(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	a.activeView().SetFilter("!shop")
	if got := a.activeView().Count(); got != 1 {
		t.Errorf("!shop matched %d rows, want 1 (only the non-compose container)", got)
	}
}

func TestEmptyFilterDoesNotWedgeTheCursor(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)

	v := typedView[*views.ContainersView](a, style.ViewContainers)
	v.SetFilter("zzzz-matches-nothing")
	if v.Count() != 0 {
		t.Fatalf("expected an empty table, got %d rows", v.Count())
	}
	// This is the bubbles cursor-clamp bug setTableRows exists to fix: an
	// empty row set pins the cursor at -1 and it is never restored, so
	// every row action dies silently for the rest of the session.
	v.SetFilter("")
	if _, ok := v.Selected(); !ok {
		t.Error("cursor did not recover after the filter was cleared — row actions are now dead")
	}
}

func TestReadonlyRefusesMutations(t *testing.T) {
	a := newTestApp(t)
	a.readonly = true
	loadContainers(a)

	step(a, key("x")) // stop
	if a.errFlash == "" {
		t.Error("readonly mode did not refuse the stop action")
	}
	if !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("refusal message should name readonly mode, got %q", a.errFlash)
	}
	if a.confirm.Active() {
		t.Error("readonly mode should not even open a confirm bar")
	}
}

func TestDestructiveKeysAskFirst(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)

	step(a, key("ctrl+d"))
	if !a.confirm.Active() {
		t.Fatal("ctrl-d did not open the confirm bar")
	}
	if p := a.confirm.Prompt(); !strings.Contains(p, "remove container") || !strings.Contains(p, "web") {
		t.Errorf("confirm prompt should name the action and the container, got %q", p)
	}
	out := render(a)
	if !strings.Contains(out, "remove container") {
		t.Error("confirm prompt is not visible in the rendered frame")
	}

	// `n` declines and closes without acting.
	step(a, key("n"))
	if a.confirm.Active() {
		t.Error("confirm bar stayed open after declining")
	}
}

// TestConfirmBarOwnsEveryKey pins the ordering in handleKey: while a
// destructive question is on screen no other key may reach a view, or a
// stray `:` would replace the question with a palette and lose it.
func TestConfirmBarOwnsEveryKey(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	step(a, key("ctrl+d"))

	step(a, key(":"))
	if a.commandBar.Active() {
		t.Error(": opened the command palette on top of a pending confirm")
	}
	if !a.confirm.Active() {
		t.Error("the confirm question was lost")
	}
}

func TestKillAsksBeforeSignalling(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	step(a, key("K"))
	if !a.confirm.Active() {
		t.Fatal("K did not ask before sending SIGKILL")
	}
	if p := a.confirm.Prompt(); !strings.Contains(p, "SIGKILL") {
		t.Errorf("confirm prompt should name the signal, got %q", p)
	}
}

func TestDrillIntoLogsAndBack(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)

	step(a, key("enter"))
	if a.view != style.ViewLogs {
		t.Fatalf("enter did not drill into logs, view = %v", a.view)
	}
	// The breadcrumb records where we came from.
	if got := a.breadcrumb(); len(got) != 2 || got[0].Label != "Containers" || !got[1].Leaf {
		t.Errorf("breadcrumb = %+v, want Containers > Logs(leaf)", got)
	}
	// The border title names the container, not the resource.
	if out := render(a); !strings.Contains(out, "web") {
		t.Error("log view title should name the container")
	}

	step(a, key("esc"))
	if a.view != style.ViewContainers {
		t.Errorf("esc did not return to containers, view = %v", a.view)
	}
}

func TestPauseKeyFlipsWithState(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	v := typedView[*views.ContainersView](a, style.ViewContainers)

	// Cursor starts on a running container: p means pause.
	if action, _ := v.HandleKey("p"); action != "pause" {
		t.Errorf("p on a running container = %q, want pause", action)
	}
	// Move to the paused one. Running sorts first, so `standalone`
	// (paused) is not row 0 — find it.
	for i := 0; i < 4; i++ {
		if c, ok := v.Selected(); ok && c.State == "paused" {
			if action, _ := v.HandleKey("p"); action != "unpause" {
				t.Errorf("p on a paused container = %q, want unpause", action)
			}
			return
		}
		v.UpdateTable(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	t.Fatal("never landed on the paused container")
}

func TestCommandPaletteSwitchesViews(t *testing.T) {
	a := newTestApp(t)
	if errMsg, _ := a.dispatchCommand("images"); errMsg != "" {
		t.Fatalf(":images returned %q", errMsg)
	}
	if a.view != style.ViewImages {
		t.Errorf(":images did not switch, view = %v", a.view)
	}
	if errMsg, _ := a.dispatchCommand("nonsense"); errMsg == "" {
		t.Error("an unknown command should report an error")
	}
}

func TestFuzzyMatchPrefersPrefixes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{in: "im", want: "images"},
		{in: "vol", want: "volumes"},
		{in: "ctx", want: "ctx"},
		{in: "q", want: "q"},
		{in: "", want: ""},
	} {
		if got := fuzzyMatch(tc.in); got != tc.want {
			t.Errorf("fuzzyMatch(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHelpOverlayRenders(t *testing.T) {
	a := newTestApp(t)
	step(a, key("?"))
	if !a.showHelp {
		t.Fatal("? did not open help")
	}
	out := render(a)
	for _, want := range []string{"RESOURCE", "CONTAINER", "GENERAL", "NAVIGATION", "SIGKILL"} {
		if !strings.Contains(out, want) {
			t.Errorf("help overlay is missing %q", want)
		}
	}
	step(a, key("esc"))
	if a.showHelp {
		t.Error("esc did not close help")
	}
}

func TestProjectsFoldContainersByLabel(t *testing.T) {
	a := newTestApp(t)
	step(a, key("5"))
	step(a, views.ProjectsRefreshMsg{Containers: sampleContainers()})

	v := typedView[*views.ProjectsView](a, style.ViewProjects)
	if got := v.Count(); got != 1 {
		t.Fatalf("expected 1 compose project, got %d", got)
	}
	p, ok := v.Selected()
	if !ok {
		t.Fatal("no project selected")
	}
	if p.Name != "shop" || p.Total != 3 || p.Running != 2 {
		t.Errorf("project = %+v, want shop with 2/3 running", p)
	}
	if out := render(a); !strings.Contains(out, "shop") || !strings.Contains(out, "2/3") {
		t.Errorf("projects frame missing the fold:\n%s", out)
	}
}

func TestInfoPanelReportsEndpointAndCounts(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	lines := a.renderInfoPanel()
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Context:", "Endpoint:", "Engine:", "Counts:", "Dockyard:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("info panel missing %q", want)
		}
	}
	if !strings.Contains(joined, "2/4 ctr") {
		t.Errorf("counts line should report 2/4 running containers, got:\n%s", joined)
	}
}

func TestReadonlyBadgeShows(t *testing.T) {
	a := newTestApp(t)
	a.readonly = true
	if got := strings.Join(a.renderInfoPanel(), "\n"); !strings.Contains(got, "RO") {
		t.Error("readonly mode should show an [RO] badge in the info panel")
	}
}

func TestStatsToggleBlanksTheColumns(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	step(a, key("t"))
	if a.statsOn {
		t.Fatal("t did not turn the stats poll off")
	}
	if !strings.Contains(a.flash, "off") {
		t.Errorf("toggling stats should say so, flash = %q", a.flash)
	}
}

func TestBuiltinNetworkDeleteIsRefusedWithAReason(t *testing.T) {
	a := newTestApp(t)
	step(a, key("4"))
	step(a, views.NetworksRefreshMsg{Networks: []docker.Network{
		{ID: "net1", Name: "bridge", Driver: "bridge", Scope: "local"},
	}})
	step(a, key("ctrl+d"))
	if a.confirm.Active() {
		t.Error("a built-in network should not even reach a confirm prompt")
	}
	if !strings.Contains(a.errFlash, "built-in") {
		t.Errorf("refusal should explain why, got %q", a.errFlash)
	}
}

func TestEveryViewRendersWithoutData(t *testing.T) {
	// A view that panics on an empty data set is a view that panics on a
	// fresh daemon, which is the first thing a new user sees.
	a := newTestApp(t)
	for _, vt := range []style.ViewType{
		style.ViewContainers, style.ViewImages, style.ViewVolumes,
		style.ViewNetworks, style.ViewProjects,
	} {
		a.view = vt
		a.loading = false
		if v := a.viewMap[vt]; v != nil {
			v.SetFilter("")
		}
		if out := render(a); out == "" {
			t.Errorf("view %v rendered an empty frame", vt)
		}
	}
}

func TestNarrowTerminalStillRenders(t *testing.T) {
	a := NewApp(nil, Options{Version: "test"})
	a.splashActive = false
	a.loading = false
	step(a, tea.WindowSizeMsg{Width: 80, Height: 24})
	loadContainers(a)
	if out := render(a); !strings.Contains(out, "web") {
		t.Error("an 80x24 terminal should still show container rows")
	}
}

// TestStateColumnIsColored asserts on the styled frame: the STATE column
// is the one place color carries information rather than decoration, so a
// regression that drops it would be invisible to every other test here.
func TestStateColumnIsColored(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	out := renderStyled(a)

	// style.StateRunning is the theme's OK green; style.StatePaused the
	// filter amber. Both must appear, or states are rendering flat.
	green := lipgloss.NewStyle().Foreground(style.ColorGreen).Render("running")
	if !strings.Contains(out, green) {
		t.Error("running state is not rendered in the OK color")
	}
	paused := lipgloss.NewStyle().Foreground(style.ColorYellow).Render("paused")
	if !strings.Contains(out, paused) {
		t.Error("paused state is not rendered in the warn color")
	}
}

// TestTickDoesNotRestartTheLogStream pins the polled-view set. Refresh()
// on a stream view means "start over": on the tick it would cancel the
// tail and re-open it every three seconds, dropping the scrollback and
// re-fetching history each time. Logs must not be in `polled`.
func TestTickDoesNotRestartTheLogStream(t *testing.T) {
	if polled[style.ViewLogs] {
		t.Error("logs must not be polled — Refresh() restarts the stream")
	}
	for _, vt := range []style.ViewType{style.ViewInspect, style.ViewLayers, style.ViewContexts} {
		if polled[vt] {
			t.Errorf("view %v is a document, not a feed — it should not be polled", vt)
		}
	}
	// The expensive inventory views load on open and on <r>, never on the
	// tick: `docker images` is measured in minutes on a real host.
	for _, vt := range []style.ViewType{style.ViewImages, style.ViewVolumes, style.ViewNetworks} {
		if polled[vt] {
			t.Errorf("view %v is slow inventory — polling it buys nothing", vt)
		}
	}
	if !polled[style.ViewContainers] || !polled[style.ViewProjects] {
		t.Error("containers and projects are the volatile views — they must poll")
	}
}

// TestTickOnLogsViewIssuesNoCommand is the behavioral half of the above:
// the tick must produce no refresh command at all while logs are open.
func TestTickOnLogsViewIssuesNoCommand(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	step(a, key("enter"))
	if a.view != style.ViewLogs {
		t.Fatalf("expected the logs view, got %v", a.view)
	}
	if cmd := a.refreshPolledView(); cmd != nil {
		t.Error("the tick issued a refresh on the logs view — it would restart the stream")
	}
}

// TestContentNeverWrapsAtAnyWidth guards the column fitter. bubbles has no
// horizontal scrolling: if the column widths sum past the frame, every row
// wraps onto a second line and the table becomes unreadable. This caught a
// real bug — the fitter counted one cell of padding per column when
// tuikit's styles apply Padding(0, 1), which is two.
func TestContentNeverWrapsAtAnyWidth(t *testing.T) {
	for _, w := range []int{80, 100, 118, 132, 160, 200, 240} {
		a := NewApp(nil, Options{Version: "test"})
		a.splashActive = false
		a.loading = false
		step(a, tea.WindowSizeMsg{Width: w, Height: 32})

		for _, vt := range []style.ViewType{
			style.ViewContainers, style.ViewImages,
			style.ViewVolumes, style.ViewNetworks, style.ViewProjects,
		} {
			a.view = vt
			a.resizeActiveView()
			switch vt {
			case style.ViewContainers:
				step(a, views.ContainersRefreshMsg{Containers: sampleContainers()})
			case style.ViewProjects:
				step(a, views.ProjectsRefreshMsg{Containers: sampleContainers()})
			}
			for i, line := range strings.Split(render(a), "\n") {
				if n := lipgloss.Width(line); n > w {
					t.Errorf("width %d, view %v: line %d is %d cells wide — content will wrap\n%q",
						w, vt, i, n, line)
					break
				}
			}
		}
	}
}
