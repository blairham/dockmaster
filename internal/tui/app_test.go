package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/chrome"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
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
	}
	// A real ctrl chord, not Text "ctrl+f": a fake whose String() happens
	// to match would pass a test the terminal's own keypress fails.
	if c, ok := strings.CutPrefix(s, "ctrl+"); ok && len(c) == 1 {
		return tea.KeyPressMsg{Code: rune(c[0]), Mod: tea.ModCtrl}
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
	out := render(a)
	for _, l := range logoLines {
		if !strings.Contains(out, strings.TrimRight(l, " ")) {
			t.Errorf("splash is missing logo line %q", l)
		}
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
		{key: "1", want: style.ViewImages},
		{key: "2", want: style.ViewVolumes},
		{key: "3", want: style.ViewNetworks},
		{key: "4", want: style.ViewProjects},
		{key: "5", want: style.ViewRuntimes},
		{key: "6", want: style.ViewEvents},
		{key: "0", want: style.ViewContainers},
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
	// Every section heading is k9s's plain green; none is set apart.
	styled := renderStyled(a)
	th := a.chrome.Theme
	for _, h := range []string{"RESOURCE", "CONTAINER", "GENERAL", "NAVIGATION"} {
		if want := th.On(th.HelpSection).Render(h); !strings.Contains(styled, want) {
			t.Errorf("help header %s is not plain HelpSection green", h)
		}
	}
	step(a, key("esc"))
	if a.showHelp {
		t.Error("esc did not close help")
	}
}

func TestProjectsFoldContainersByLabel(t *testing.T) {
	a := newTestApp(t)
	step(a, key("4"))
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
	for _, want := range []string{"Context:", "Endpoint:", "Engine:", "Counts:", "DM Rev:"} {
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
	step(a, key("3"))
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

// TestFrameFitsTheTerminal pins the header budget. tuikit sizes the content
// box from TopSectionRows — the logo's height — and trusts the shortcut
// grid not to exceed it. The containers view once handed it twelve rows of
// actions: the header grew by six, the bottom border was pushed off-screen,
// and the rows with no logo beside them were right-aligned under the logo.
func TestFrameFitsTheTerminal(t *testing.T) {
	for _, w := range []int{80, 100, 119, 120, 132, 160, 240} {
		for _, logoless := range []bool{false, true} {
			a := NewApp(nil, Options{Version: "test", Logoless: logoless})
			a.splashActive = false
			a.loading = false
			step(a, tea.WindowSizeMsg{Width: w, Height: 40})

			for _, vt := range []style.ViewType{
				style.ViewContainers, style.ViewImages, style.ViewVolumes,
				style.ViewNetworks, style.ViewProjects, style.ViewRuntimes, style.ViewLogs,
				style.ViewInspect, style.ViewContexts,
			} {
				a.view = vt
				if n := len(a.renderShortcuts(a.renderInfoPanel())); n > a.chrome.TopSectionRows() {
					t.Errorf("width %d, logoless %v, view %v: %d shortcut rows, header holds %d",
						w, logoless, vt, n, a.chrome.TopSectionRows())
				}
			}

			a.view = style.ViewContainers
			a.resizeActiveView()
			step(a, views.ContainersRefreshMsg{Containers: sampleContainers()})
			lines := strings.Split(strings.TrimRight(render(a), "\n"), "\n")
			if len(lines) != 40 {
				t.Errorf("width %d, logoless %v: frame is %d lines, terminal is 40", w, logoless, len(lines))
			}
			bottom := -1
			for i, l := range lines {
				if strings.Contains(l, "╰") {
					bottom = i
				}
			}
			if bottom < 0 || bottom < len(lines)-3 {
				t.Errorf("width %d, logoless %v: content box bottom border at line %d of %d",
					w, logoless, bottom, len(lines))
			}
		}
	}
}

// TestShellAndStartKeys pins the rebind: `s` shells in wherever a container
// is in view, and start moved to `u` ("up") in both the containers and the
// projects views, so `s` never means start anywhere.
func TestShellAndStartKeys(t *testing.T) {
	cs := sampleContainers()
	cv := views.NewContainersView(nil, true, false)
	cv.Update(views.ContainersRefreshMsg{Containers: cs})
	for _, tc := range []struct {
		v    views.View
		name string
		key  string
		want string
	}{
		{name: "containers s", v: cv, key: "s", want: "exec"},
		{name: "containers u", v: cv, key: "u", want: "start"},
		{name: "containers e", v: cv, key: "e", want: "edit_form"}, // edit — not the old shell
	} {
		if got, _ := tc.v.HandleKey(tc.key); got != tc.want {
			t.Errorf("%s: action %q, want %q", tc.name, got, tc.want)
		}
	}

	pv := views.NewProjectsView(nil)
	pv.Update(views.ProjectsRefreshMsg{Containers: cs})
	if got, _ := pv.HandleKey("u"); got != "compose_up" {
		t.Errorf("projects u: action %q, want compose_up", got)
	}
	if got, _ := pv.HandleKey("s"); got == "compose_up" || got == "start_project" {
		t.Error("projects s still starts the project")
	}
}

// TestNavigationKeysDoWhatHelpSays drives every key the NAVIGATION column
// advertises against a loaded table. Help once listed j/k while nothing
// bound them; now that tuikit's TranslateNavKey does, each listed key must
// move the cursor.
func TestNavigationKeysDoWhatHelpSays(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	step(a, key("?"))
	out := render(a)
	for _, e := range chrome.NavigationHelp().Entries {
		if !strings.Contains(out, e.Key) {
			t.Errorf("help is missing %s", e.Key)
		}
	}
	step(a, key("esc"))

	cv := typedView[*views.ContainersView](a, style.ViewContainers)
	name := func() string { c, _ := cv.Selected(); return c.Name }
	top := name()
	step(a, key("j"))
	second := name()
	if second == top {
		t.Fatal("j did not move the cursor down")
	}
	step(a, key("k"))
	if name() != top {
		t.Errorf("k did not move back up: %q", name())
	}
	step(a, key("G"))
	bottom := name()
	if bottom == top {
		t.Error("G did not jump to the bottom")
	}
	step(a, key("g"))
	if name() != top {
		t.Errorf("g did not jump to the top: %q", name())
	}
	step(a, key("ctrl+f"))
	if name() == top {
		t.Error("ctrl+f did not page down")
	}
	step(a, key("ctrl+b"))
	if name() != top {
		t.Errorf("ctrl+b did not page up: %q", name())
	}
	// h and l: l is the containers view's own logs key and must still win
	// over the translation to →.
	step(a, key("l"))
	if a.view != style.ViewLogs {
		t.Errorf("l in containers opened %v, want the logs view", a.view)
	}
}

// TestBackReloadAndHistoryKeys covers the GENERAL and history keys tuikit
// names: q, ctrl+r, [, ] and -.
func TestBackReloadAndHistoryKeys(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)

	step(a, key("l"))
	if a.view != style.ViewLogs {
		t.Fatalf("setup: l opened %v", a.view)
	}
	step(a, key("q"))
	if a.view != style.ViewContainers {
		t.Errorf("q from logs went to %v, want containers", a.view)
	}
	if cmd := step(a, key("q")); cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Error("q at a top-level view quit the program")
		}
	}
	if a.view != style.ViewContainers {
		t.Errorf("q at a top-level view moved to %v", a.view)
	}

	step(a, key("ctrl+r"))
	if a.flash != "refreshing..." {
		t.Errorf("ctrl+r flash = %q, want a refresh", a.flash)
	}

	step(a, key("1")) // images
	step(a, key("2")) // volumes
	for _, tc := range []struct {
		key  string
		want style.ViewType
	}{
		{key: "[", want: style.ViewImages},
		{key: "[", want: style.ViewContainers},
		{key: "[", want: style.ViewContainers}, // oldest: stays put
		{key: "]", want: style.ViewImages},
		{key: "]", want: style.ViewVolumes},
		{key: "]", want: style.ViewVolumes}, // newest: stays put
		{key: "-", want: style.ViewImages},
		{key: "-", want: style.ViewVolumes}, // - toggles
	} {
		step(a, key(tc.key))
		if a.view != tc.want {
			t.Fatalf("after %s: view %v, want %v", tc.key, a.view, tc.want)
		}
	}
}

// assertFrameFits checks the whole frame is exactly the terminal's height
// and still ends with the content box's bottom border and the breadcrumb.
func assertFrameFits(t *testing.T, a *App, what string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(render(a), "\n"), "\n")
	if len(lines) != a.height {
		t.Errorf("%s: frame is %d lines, terminal is %d", what, len(lines), a.height)
	}
	bottom := -1
	for i, l := range lines {
		if strings.Contains(l, "╰") {
			bottom = i
		}
	}
	// Below the box: the status bar when a flash is up, the breadcrumb,
	// and tuikit's bottom gap.
	want := len(lines) - 3
	if a.flash != "" || a.errFlash != "" {
		want--
	}
	if bottom != want {
		t.Errorf("%s: bottom border at line %d of %d, want %d", what, bottom, len(lines), want)
	}
	wantCrumb := chrome.CrumbText(style.ViewName(a.view))
	if crumb := strings.Join(lines[max(0, len(lines)-2):], "\n"); !strings.Contains(crumb, wantCrumb) {
		t.Errorf("%s: breadcrumb %q missing from the last lines %q", what, wantCrumb, crumb)
	}
}

// TestConfirmBarKeepsTheFrame pins the confirm bar's three rows coming out
// of the table, not off the bottom of the screen. Opening the bar from an
// action never re-sized the active view, so the table kept its full height,
// the box grew by three rows, and the border and breadcrumb fell off.
func TestConfirmBarKeepsTheFrame(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	assertFrameFits(t, a, "before")
	for _, k := range []string{"ctrl+d", "K"} {
		step(a, key(k))
		if !a.confirm.Active() {
			t.Fatalf("%s did not open the confirm bar", k)
		}
		assertFrameFits(t, a, "containers "+k+" confirm")
		step(a, key("n"))
		assertFrameFits(t, a, "containers after "+k+" declined")
	}

	c, _ := newColimaApp(t, Options{})
	for _, k := range []string{"x", "R", "ctrl+d"} {
		step(c, key(k))
		assertFrameFits(t, c, "colima "+k+" confirm")
		step(c, key("n"))
	}
}

// TestStatusFlashKeepsTheFrame: a flash set by an action adds the status
// bar's row, and must take it from the table too.
func TestStatusFlashKeepsTheFrame(t *testing.T) {
	a, _ := newColimaApp(t, Options{})
	step(a, tea.KeyPressMsg{Code: tea.KeyDown})
	step(a, key("u")) // sets "starting work — …" without running the command
	if a.flash == "" {
		t.Fatal("start set no flash")
	}
	assertFrameFits(t, a, "flash after start")
}

// systemdLines are shaped like a kind node's console output, which is what
// broke the logs view: CRLF line ends from a TTY container, erase-in-line
// (\x1b[K), a tab, and SGR color that resets mid-line.
var systemdLines = []docker.LogLine{
	{
		Text: "[\x1b[0;32m  OK  \x1b[0m] Finished \x1b[0;1;39msystemd-remount-f…ount\x1b[0m - Remount Root and Kernel File Systems.\r",
	},
	{Text: "         Starting \x1b[0;1;39msystemd-sysctl.se…ce\x1b[0m - Apply Kernel Variables...\x1b[K\r"},
	// systemd shortened this unit name through the middle of its color code,
	// leaving a half escape; on a real kind node it cost every row after it
	// the canvas color.
	{Text: "         Starting \x1b[0;1;39mkubelet.service\x1b[…elet: The Kubernetes Node Agent...\r"},
	{Text: "progress 10%\rprogress 100%\r"},
	{Text: "col1\tcol2\tcol3"},
	{Text: strings.Repeat("a very long line that is wider than any terminal ", 8) + "\r"},
}

// TestLogsViewKeepsItsBorder pins the logs view against raw console
// output: a carriage return sent the cursor back to the start of the row and
// \x1b[K erased the rest of it, taking the box's right border with it.
func TestLogsViewKeepsItsBorder(t *testing.T) {
	a := newTestApp(t)
	lv := views.NewLogsView(nil, "abc", "k8s-control-plane")
	a.setView(style.ViewLogs, lv)
	a.pushView(style.ViewLogs)
	step(a, views.LogBatchMsg{Lines: systemdLines})

	raw := renderStyled(a)
	for _, bad := range []string{"\r", "\x1b[K", "\t"} {
		if strings.Contains(raw, bad) {
			t.Errorf("frame still carries %q", bad)
		}
	}
	assertFrameFits(t, a, "logs")
	inBox := false
	for i, l := range strings.Split(render(a), "\n") {
		switch {
		case strings.Contains(l, "╭"):
			inBox = true
			continue
		case strings.Contains(l, "╰"):
			inBox = false
		}
		if !inBox {
			continue
		}
		if w := lipgloss.Width(l); w != a.width {
			t.Errorf("box line %d is %d wide, want %d: %q", i, w, a.width, l)
		}
		if !strings.HasPrefix(l, "│") || !strings.HasSuffix(strings.TrimRight(l, " "), "│") {
			t.Errorf("box line %d lost its border: %q", i, l)
		}
	}
	out := render(a)
	for _, want := range []string{"OK", "Remount Root", "progress 100%", "col1    col2"} {
		if !strings.Contains(out, want) {
			t.Errorf("logs view is missing %q", want)
		}
	}
	if strings.Contains(out, "progress 10%") {
		t.Error("overwritten carriage-return segment was kept")
	}
}

// TestSelectedRowSurvivesTheCanvas: under the painted canvas the selected
// row must still carry the selection color, not the canvas black.
func TestSelectedRowSurvivesTheCanvas(t *testing.T) {
	a := newTestApp(t)
	loadContainers(a)
	for _, l := range strings.Split(renderStyled(a), "\n") {
		if strings.Contains(ansi.ReplaceAllString(l, ""), "web") {
			if !strings.Contains(l, "48;2;135;206;250") {
				t.Errorf("selected row lost its highlight: %q", l)
			}
			return
		}
	}
	t.Fatal("no row for web")
}

// TestHeaderLogo pins when the header carries the Graffiti logo: whenever
// every shortcut column fits beside it, and never at the cost of one.
func TestHeaderLogo(t *testing.T) {
	for _, tc := range []struct {
		width    int
		wantLogo bool
	}{
		{width: 240, wantLogo: true},
		{width: 200, wantLogo: true},
		// The info panel is as wide as its content (k9s layout), which
		// leaves the 22-column DM logo room beside every column from 139;
		// a narrower header sheds the logo rather than a shortcut. (The
		// full dockmaster art, 69 columns, needed 186 — why the header
		// draws DM and only the splash the full name.)
		{width: 180, wantLogo: true},
		{width: 160, wantLogo: true},
		{width: 139, wantLogo: true},
		{width: 138},
		{width: 120},
	} {
		a := NewApp(nil, Options{Version: "test"})
		a.splashActive, a.loading = false, false
		step(a, tea.WindowSizeMsg{Width: tc.width, Height: 40})
		loadContainers(a)
		out := render(a)
		header := strings.Join(strings.Split(out, "\n")[:6], "\n")

		all := 0
		for _, l := range dmLogo {
			if strings.Contains(header, strings.TrimRight(l, " ")) {
				all++
			}
		}
		if got := all == len(dmLogo); got != tc.wantLogo {
			t.Errorf("width %d: logo shown %v (%d/%d lines), want %v", tc.width, got, all, len(dmLogo), tc.wantLogo)
		}
		if strings.ContainsRune(out, 'ت') {
			t.Errorf("width %d: stray glyph in the frame", tc.width)
		}
		// Every container action stays on screen whether or not the logo does.
		for _, want := range []string{"<0>", "Restart", "Stop", "Shell", "Remove"} {
			if !strings.Contains(header, want) {
				t.Errorf("width %d: shortcut %q shed", tc.width, want)
			}
		}
	}
}

// TestLogoless pins both ways to drop the header logo — the --logoless flag
// and the :logo toggle — on a terminal wide enough that it would show.
func TestLogoless(t *testing.T) {
	hasLogo := func(a *App) bool {
		header := strings.Join(strings.Split(render(a), "\n")[:6], "\n")
		return strings.Contains(header, strings.TrimRight(dmLogo[1], " "))
	}
	a := NewApp(nil, Options{Version: "test", Logoless: true})
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 240, Height: 40})
	loadContainers(a)
	if hasLogo(a) {
		t.Error("--logoless still shows the logo")
	}
	a.dispatchCommand("logo")
	if !hasLogo(a) {
		t.Error(":logo did not bring the logo back")
	}
	assertFrameFits(t, a, "after :logo on")
	a.dispatchCommand("logoless")
	if hasLogo(a) {
		t.Error(":logoless did not hide the logo")
	}
	a.dispatchCommand("logoless")
	if hasLogo(a) {
		t.Error(":logoless twice brought the logo back")
	}
	assertFrameFits(t, a, "after :logoless")

	step(a, key("?"))
	if out := render(a); !strings.Contains(out, "<:logo>") {
		t.Error("help does not mention :logo")
	}
}

func newSizedApp(t *testing.T, opts Options, w, h int) *App {
	t.Helper()
	if opts.Version == "" {
		opts.Version = "test"
	}
	a := NewApp(nil, opts)
	a.loading = false
	step(a, tea.WindowSizeMsg{Width: w, Height: h})
	return a
}

func TestSplashless(t *testing.T) {
	a := newSizedApp(t, Options{Splashless: true}, 160, 30)
	if a.splashActive {
		t.Fatal("splash active under --splashless")
	}
	loadContainers(a)
	if out := render(
		a,
	); !strings.Contains(out, "containers(") ||
		strings.Contains(out, "Connecting to the Docker daemon") {
		t.Errorf("first frame is not the containers view:\n%s", out)
	}
}

// TestHeadlessAndCrumbsless pins that the hidden rows go to the table: the
// frame stays exactly the terminal's height, the box starts on the first
// line without a header and ends on the last without breadcrumbs.
func TestHeadlessAndCrumbsless(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		headless, crumbsless bool
	}{
		{name: "headless", headless: true},
		{name: "crumbsless", crumbsless: true},
		{name: "both", headless: true, crumbsless: true},
	} {
		a := newSizedApp(t, Options{Splashless: true, Headless: tc.headless, Crumbsless: tc.crumbsless}, 240, 30)
		loadContainers(a)
		check := func(what string) {
			t.Helper()
			lines := strings.Split(strings.TrimRight(render(a), "\n"), "\n")
			if len(lines) != a.height {
				t.Errorf("%s %s: frame is %d lines, terminal is %d", tc.name, what, len(lines), a.height)
				return
			}
			joined := strings.Join(lines, "\n")
			if hasHeader := strings.Contains(joined, "Context:"); hasHeader == tc.headless {
				t.Errorf("%s %s: header shown = %v", tc.name, what, hasHeader)
			}
			last := lines[len(lines)-1]
			if tc.crumbsless {
				if !strings.Contains(last, "╰") {
					t.Errorf("%s %s: last line is not the box bottom: %q", tc.name, what, last)
				}
			} else if !strings.Contains(strings.Join(lines[len(lines)-2:], ""), chrome.CrumbText("Containers")) {
				t.Errorf("%s %s: breadcrumb missing", tc.name, what)
			}
			if tc.headless && !strings.Contains(lines[0], "╭") && !strings.Contains(lines[0], "remove container") {
				t.Errorf("%s %s: first line is neither the box nor a bar: %q", tc.name, what, lines[0])
			}
		}
		check("plain")
		step(a, key("ctrl+d"))
		check("with confirm")
		step(a, key("n"))
	}
}

func TestCommandOpensView(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want style.ViewType
	}{
		{cmd: "images", want: style.ViewImages},
		{cmd: "Colima", want: style.ViewRuntimes},
		{cmd: "compose", want: style.ViewProjects},
		{cmd: "", want: style.ViewContainers},
		{cmd: "nonsense", want: style.ViewContainers},
	} {
		if got := newSizedApp(t, Options{Command: tc.cmd}, 160, 30).view; got != tc.want {
			t.Errorf("-c %q opened %v, want %v", tc.cmd, got, tc.want)
		}
	}
	if got := newSizedApp(t, Options{Command: "images", StartOnRuntimes: true}, 160, 30).view; got != style.ViewRuntimes {
		t.Errorf("a down colima daemon should win over -c, got %v", got)
	}
}

// TestStderrIsNotPaintedRed: the registry, like most Go services, logs
// everything to stderr, and painting stderr red turned `level=info` into a
// screen that read as a failing service. A line keeps its own colors.
func TestStderrIsNotPaintedRed(t *testing.T) {
	a := newTestApp(t)
	lv := views.NewLogsView(nil, "abc", "kind-registry")
	a.setView(style.ViewLogs, lv)
	a.pushView(style.ViewLogs)
	step(a, views.LogBatchMsg{Lines: []docker.LogLine{
		{Text: `time="2026-10-01T01:29:47Z" level=info msg="listening on [::]:5000"`, Stderr: true},
		{Text: "[\x1b[0;32m  OK  \x1b[0m] Started kubelet.service"},
	}})
	sample := lipgloss.NewStyle().Foreground(style.ColorRed).Render("x")
	red := sample[:strings.IndexByte(sample, 'x')]
	for _, l := range strings.Split(renderStyled(a), "\n") {
		if strings.Contains(l, "listening on") && strings.Contains(l, red) {
			t.Errorf("stderr info line painted red: %q", l)
		}
		if strings.Contains(l, "Started kubelet") && !strings.Contains(l, "\x1b[0;32m") {
			t.Errorf("the line's own color was lost: %q", l)
		}
	}
}

// TestHeaderLooksLikeK9s pins the header against k9s: shortcuts start right
// after the info panel rather than against the logo, a column holds six
// entries so the first action column sits beside the view digits, the logo
// is pinned to the right edge, and the info labels are k9s's orange.
func TestHeaderLooksLikeK9s(t *testing.T) {
	a := newSizedApp(t, Options{Splashless: true}, 220, 45)
	a.view = style.ViewContainers
	a.resizeActiveView()
	step(a, views.ContainersRefreshMsg{Containers: sampleContainers()})

	lines := strings.Split(render(a), "\n")
	row0 := lines[0]
	info := strings.Index(row0, "Context:")
	digit := strings.Index(row0, "<0>")
	if info < 0 || digit < 0 {
		t.Fatalf("header row 0 = %q", row0)
	}
	infoW := 0
	for _, l := range a.renderInfoPanel() {
		infoW = max(infoW, lipgloss.Width(l))
	}
	if gap := digit - (info + infoW); gap < 1 || gap > 4 {
		t.Errorf("<0> starts %d cells after the widest info line; want it right after (row %q)", gap, row0)
	}
	if !strings.Contains(lines[5], "<5>") || strings.Contains(lines[6], "<") {
		t.Errorf("want <5> as the sixth and last row of the view column; rows 5-6 = %q / %q", lines[5], lines[6])
	}
	// The logo is a block, padded to its widest line; that line must end
	// at the right edge.
	widest := 0
	for i, l := range dmLogo {
		if len(strings.TrimRight(l, " ")) > len(strings.TrimRight(dmLogo[widest], " ")) {
			widest = i
		}
	}
	if row := strings.TrimRight(lines[widest], " "); !strings.HasSuffix(row, strings.TrimRight(dmLogo[widest], " ")) {
		t.Errorf("logo is not pinned to the right edge: %q", row)
	}
	if styled := renderStyled(a); !strings.Contains(styled, "38;2;255;165;0mContext:") {
		t.Errorf("info label is not k9s orange")
	}
}
