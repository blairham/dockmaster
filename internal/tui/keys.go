package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"
	"github.com/blairham/tuikit/viewfsm"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// knownCommands feeds the `:` palette's fuzzy suggestion.
var knownCommands = []string{
	"q",
	"q!",
	"quit",
	"exit",
	"containers",
	"ps",
	"images",
	"volumes",
	"networks",
	"projects",
	"compose",
	"runtimes",
	"colima",
	"df",
	"pf",
	"pods",
	"events",
	"context",
	"ctx",
	"contexts",
	"logs",
	"inspect",
	"describe",
	"top",
	"diff",
	"health",
	"prune",
	"prune all",
	"prune all volumes",
	"prune cache",
	"pull",
	"readonly",
	"logo",
	"logoless",
	"stats",
	"all",
}

// viewCommands maps every name that opens a top-level view — from the `:`
// palette or `-c` at startup — to that view. One table, so the flag and the
// palette cannot drift apart.
var viewCommands = map[string]style.ViewType{
	"containers": style.ViewContainers, "container": style.ViewContainers, "ps": style.ViewContainers,
	"images": style.ViewImages, "image": style.ViewImages,
	"volumes": style.ViewVolumes, "volume": style.ViewVolumes,
	"networks": style.ViewNetworks, "network": style.ViewNetworks,
	"projects": style.ViewProjects, "project": style.ViewProjects, "compose": style.ViewProjects,
	"runtimes": style.ViewRuntimes, "runtime": style.ViewRuntimes, "engines": style.ViewRuntimes,
	"machines": style.ViewRuntimes, "machine": style.ViewRuntimes,
	"colima": style.ViewRuntimes, "podman": style.ViewRuntimes, "vm": style.ViewRuntimes, "vms": style.ViewRuntimes,
	"profiles": style.ViewRuntimes, "profile": style.ViewRuntimes,
	"pods": style.ViewPods, "pod": style.ViewPods,
	"events": style.ViewEvents, "event": style.ViewEvents, "ev": style.ViewEvents,
	"pf": style.ViewPortForwards, "portforward": style.ViewPortForwards, "portforwards": style.ViewPortForwards,
	"forwards": style.ViewPortForwards, "forward": style.ViewPortForwards,
	"df": style.ViewDiskUsage, "disk": style.ViewDiskUsage, "usage": style.ViewDiskUsage, "system": style.ViewDiskUsage,
}

// ViewForCommand resolves a view name as the palette spells it.
func ViewForCommand(name string) (style.ViewType, bool) {
	vt, ok := viewCommands[strings.ToLower(strings.TrimSpace(name))]
	return vt, ok
}

// ViewCommandNames lists the canonical view names, for error messages.
func ViewCommandNames() []string {
	return []string{"containers", "images", "volumes", "networks", "projects", "runtimes"}
}

// fuzzyMatch picks the best command for a partial input: exact prefixes
// first, then subsequence matches, shortest winning ties.
func fuzzyMatch(input string) string {
	if input == "" {
		return ""
	}
	lower := strings.ToLower(input)

	var best string
	for _, cmd := range knownCommands {
		if strings.HasPrefix(cmd, lower) && (best == "" || len(cmd) < len(best)) {
			best = cmd
		}
	}
	if best != "" {
		return best
	}
	for _, cmd := range knownCommands {
		if isSubsequence(lower, cmd) && (best == "" || len(cmd) < len(best)) {
			best = cmd
		}
	}
	return best
}

// isSubsequence reports whether every rune of needle appears in order in
// haystack.
func isSubsequence(needle, haystack string) bool {
	hi := 0
	for _, c := range needle {
		found := false
		for hi < len(haystack) {
			if rune(haystack[hi]) == c {
				hi++
				found = true
				break
			}
			hi++
		}
		if !found {
			return false
		}
	}
	return true
}

// handleKey is the whole key path.
//
//nolint:gocyclo,gocognit // flat key dispatch
func (a *App) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		a.shutdown()
		return a, tea.Quit
	}

	// Any keypress clears a flash — it has been read, or it has not, and
	// either way it must not outlive the next interaction.
	a.errFlash = ""
	a.flash = ""

	// Bars get first refusal. Each reports whether it consumed the key,
	// including the case where the dispatch closed it.
	switch {
	case a.confirm.Active():
		// Confirm is checked first and deliberately: while a y/n question
		// is on screen, no other key may reach a view. Ordering this
		// after the command bar would let `:` open a palette on top of a
		// pending "remove this container?" and lose the question.
		if handled, cmd := a.confirm.Update(msg, a.confirmDispatch); handled {
			a.resizeActiveView()
			return a, cmd
		}
	case a.commandBar.Active():
		if handled, cmd := a.commandBar.Update(msg, a.dispatchCommand); handled {
			a.resizeActiveView()
			return a, cmd
		}
	case a.filterBar.Active():
		if handled, cmd := a.filterBar.Update(msg, a.onFilterChange); handled {
			a.resizeActiveView()
			return a, cmd
		}
	case a.prompt.Active():
		if handled, cmd := a.prompt.Update(msg, a.promptDispatch); handled {
			a.resizeActiveView()
			return a, cmd
		}
	}

	// A view taking typed input gets every key but esc: otherwise a `1`
	// typed into a field would switch views and an `r` would refresh.
	if c, ok := a.activeView().(views.InputCapturer); ok && c.CapturesInput() && !a.showHelp {
		if key == "esc" {
			a.popView()
			return a, a.refreshActiveView()
		}
		if action, param := a.activeViewHandleKey(key); action != "" {
			return a.handleAction(action, param)
		}
		return a, a.updateActiveTable(msg)
	}

	if a.showHelp {
		return a.handleHelpKey(key)
	}

	// A view using the digits itself (the log view's time ranges) gets
	// them before they switch views.
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
		if c, ok := a.activeView().(views.DigitClaimer); ok && c.ClaimsDigits() {
			if action, param := a.activeViewHandleKey(key); action != "" {
				return a.handleAction(action, param)
			}
		}
	}

	switch key {
	case "?":
		a.showHelp = true
		return a, nil
	case "esc":
		if a.filter != "" {
			a.filter = ""
			a.setActiveFilter("")
			return a, nil
		}
		if b, ok := a.activeView().(views.Backer); ok {
			if cmd, ok := b.Back(); ok {
				return a, cmd
			}
		}
		if len(a.viewStack) > 0 {
			a.popView()
			return a, a.refreshActiveView()
		}
		return a, nil
	case ":":
		cmd := a.commandBar.Open()
		a.resizeActiveView()
		return a, cmd
	case "/":
		cmd := a.filterBar.OpenWith(a.filter)
		a.resizeActiveView()
		return a, cmd
	case "0":
		return a, a.switchView(style.ViewContainers)
	case "1":
		return a, a.switchView(style.ViewImages)
	case "2":
		return a, a.switchView(style.ViewVolumes)
	case "3":
		return a, a.switchView(style.ViewNetworks)
	case "4":
		return a, a.switchView(style.ViewProjects)
	case "5":
		return a, a.switchView(style.ViewRuntimes)
	case "6":
		return a, a.switchView(style.ViewEvents)
	case "r", chrome.KeyReload:
		a.flash = "refreshing..."
		return a, a.refreshActiveView()
	// q leaves a drill-in, like esc without clearing the filter. At a
	// top-level view there is nothing to go back to, and quitting stays
	// on :q and ctrl+c — a stray q must not end the session.
	case chrome.KeyBack:
		if b, ok := a.activeView().(views.Backer); ok {
			if cmd, ok := b.Back(); ok {
				return a, cmd
			}
		}
		if len(a.viewStack) > 0 {
			a.popView()
			return a, a.refreshActiveView()
		}
		return a, nil
	// k9s's view history: [ and ] walk it, - toggles to the last view.
	case chrome.KeyHistoryBack:
		return a, a.historyJump(a.history.Back())
	case chrome.KeyHistoryForward:
		return a, a.historyJump(a.history.Forward())
	case chrome.KeyLastView:
		return a, a.historyJump(a.history.Last())
	// k9s's keys for the chrome: ctrl+g the breadcrumbs, ctrl+e the whole
	// header. tuikit owns the state and the layout; the binding is the app's.
	// The freed rows go to the table — syncViewSize re-sizes it on the next
	// frame.
	case chrome.KeyToggleCrumbs:
		a.chrome.ToggleCrumbs()
		a.resizeActiveView()
		return a, nil
	case chrome.KeyToggleHeader:
		a.chrome.ToggleHeader()
		a.resizeActiveView()
		return a, nil
	}

	// With rows marked, lifecycle and remove keys act on all of them.
	if cmd, ok := a.bulkKey(key); ok {
		return a, cmd
	}

	// View-specific keys next, then table navigation.
	if action, param := a.activeViewHandleKey(key); action != "" {
		return a.handleAction(action, param)
	}
	// Then the keys every table shares.
	if a.tableKey(key) {
		return a, nil
	}

	// Then navigation: tuikit maps the vim keys (j/k/h/l, g/G, ctrl+f/b)
	// onto the arrow and page keys tables and viewports understand. After
	// the view keys, so a view's own letter (l for logs) still wins.
	return a, a.updateActiveTable(viewfsm.TranslateNavKey(msg))
}

// historyJump shows a view the history stepped to, without recording the
// step as a new visit — History has already moved.
func (a *App) historyJump(id viewfsm.ViewID, ok bool) tea.Cmd {
	if !ok {
		return nil
	}
	return a.showView(style.ViewType(id))
}

// handleHelpKey processes keys while the help overlay is up. `:` and `/`
// open their bars on top of the overlay so a command can be issued without
// losing your place; everything else but esc/? is swallowed.
func (a *App) handleHelpKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "?", "q":
		a.showHelp = false
		return a, nil
	case ":":
		cmd := a.commandBar.Open()
		a.resizeActiveView()
		return a, cmd
	case "/":
		cmd := a.filterBar.OpenWith(a.filter)
		a.resizeActiveView()
		return a, cmd
	}
	return a, nil
}

// onFilterChange pushes each filter keystroke into the active view.
func (a *App) onFilterChange(value string) {
	a.filter = value
	a.setActiveFilter(value)
}

// dispatchCommand parses a `:` command.
//
//nolint:gocyclo // flat command table
func (a *App) dispatchCommand(input string) (string, tea.Cmd) {
	raw := strings.TrimSpace(input)
	lower := strings.ToLower(raw)

	// Commands taking an argument.
	if rest, ok := strings.CutPrefix(lower, "pull "); ok {
		_, cmd := a.handleAction("pull", strings.TrimSpace(rest))
		return "", cmd
	}
	if rest, ok := strings.CutPrefix(lower, "ctx "); ok {
		return a.switchContextByName(strings.TrimSpace(rest))
	}
	if rest, ok := strings.CutPrefix(lower, "context "); ok {
		return a.switchContextByName(strings.TrimSpace(rest))
	}

	if vt, ok := ViewForCommand(lower); ok {
		return "", a.switchView(vt)
	}

	switch lower {
	case "q", "q!", "quit", "exit":
		a.shutdown()
		return "", tea.Quit
	case "logs", "log":
		return a.rowCommand("logs", func(act string) bool { return act == "logs" }, "l")
	case "inspect", "describe":
		return a.rowCommand("inspect", isInspectAction, "o")
	// :stats stays the CPU/MEM poll toggle below; the stats view is S.
	case "top", "diff", "health":
		key := map[string]string{"top": "T", "diff": "D", "health": "H"}[lower]
		return a.rowCommand(lower, func(act string) bool { return act == lower }, key)
	case "context", "ctx", "contexts":
		_, cmd := a.handleAction("contexts", "")
		return "", cmd
	case "prune all":
		_, cmd := a.handleAction("confirm_prune_all", "")
		return "", cmd
	case "prune all volumes", "prune all -v", "prune all --volumes":
		_, cmd := a.handleAction("confirm_prune_all_volumes", "")
		return "", cmd
	case "prune cache", "prune build", "prune builder":
		_, cmd := a.handleAction("confirm_prune_cache", "")
		return "", cmd
	case "prune":
		_, cmd := a.handleAction("confirm_prune_containers", "")
		return "", cmd
	case "all":
		_, cmd := a.handleAction("toggle_all", "")
		return "", cmd
	case "stats":
		_, cmd := a.handleAction("toggle_stats", "")
		return "", cmd
	case "readonly":
		a.readonly = !a.readonly
		a.flash = "readonly " + onOff(a.readonly)
		return "", nil
	case "logo":
		a.setLogoless(!a.logoless)
		return "", nil
	case "logoless":
		a.setLogoless(true)
		return "", nil
	}
	return "unknown command: " + lower, nil
}

// rowCommand runs a palette command that acts on the selected row — :logs,
// :inspect — by asking the active view what its own key for that does, so
// the command and the key can never disagree about which row or which kind
// of inspect. The first key whose action passes want is carried out.
func (a *App) rowCommand(name string, want func(string) bool, keys ...string) (string, tea.Cmd) {
	for _, k := range keys {
		if action, param := a.activeViewHandleKey(k); want(action) {
			_, cmd := a.handleAction(action, param)
			return "", cmd
		}
	}
	return fmt.Sprintf(":%s needs a selected row it applies to — nothing here to %s", name, name), nil
}

// isInspectAction matches every view's inspect action (inspect_container,
// inspect_image, runtime_inspect, ...).
func isInspectAction(action string) bool {
	return strings.HasPrefix(action, "inspect_") || strings.HasSuffix(action, "_inspect")
}

// switchContextByName resolves a context name from the store and switches
// to it, so `:ctx colima` works without opening the picker.
func (a *App) switchContextByName(name string) (string, tea.Cmd) {
	if name == "" {
		_, cmd := a.handleAction("contexts", "")
		return "", cmd
	}
	for _, c := range docker.Contexts() {
		if strings.EqualFold(c.Name, name) {
			a.flash = "connecting to " + c.Name + "..."
			return "", doSwitchContext(c.Name, c.Host)
		}
	}
	return "no such docker context: " + name, nil
}

// setLogoless hides or restores the header logo at runtime — the `:logo`
// counterpart of the --logoless flag.
func (a *App) setLogoless(v bool) {
	a.logoless = v
	if v {
		a.chrome.Logo = nil
	} else {
		a.chrome.Logo = dmLogo
	}
	a.resizeActiveView()
}
