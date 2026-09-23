package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
)

// knownCommands feeds the `:` palette's fuzzy suggestion.
var knownCommands = []string{
	"q", "q!", "quit", "exit",
	"containers", "ps", "images", "volumes", "networks", "projects", "compose",
	"context", "ctx", "contexts",
	"logs", "inspect", "prune", "pull",
	"readonly", "logo", "logoless", "stats", "all",
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

	if a.showHelp {
		return a.handleHelpKey(key)
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
	case "1":
		return a, a.switchView(style.ViewContainers)
	case "2":
		return a, a.switchView(style.ViewImages)
	case "3":
		return a, a.switchView(style.ViewVolumes)
	case "4":
		return a, a.switchView(style.ViewNetworks)
	case "5":
		return a, a.switchView(style.ViewProjects)
	case "r":
		a.flash = "refreshing..."
		return a, a.refreshActiveView()
	}

	// View-specific keys next, then table navigation.
	if action, param := a.activeViewHandleKey(key); action != "" {
		return a.handleAction(action, param)
	}

	// j/k/g/G and friends: the log and inspect viewports take a different
	// key vocabulary than the tables, so let the tail widget claim its own
	// scroll keys before falling through.
	return a, a.updateActiveTable(msg)
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

	switch lower {
	case "q", "q!", "quit", "exit":
		a.shutdown()
		return "", tea.Quit
	case "containers", "container", "ps":
		return "", a.switchView(style.ViewContainers)
	case "images", "image":
		return "", a.switchView(style.ViewImages)
	case "volumes", "volume":
		return "", a.switchView(style.ViewVolumes)
	case "networks", "network":
		return "", a.switchView(style.ViewNetworks)
	case "projects", "project", "compose":
		return "", a.switchView(style.ViewProjects)
	case "context", "ctx", "contexts":
		_, cmd := a.handleAction("contexts", "")
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
	case "logo", "logoless":
		a.setLogoless(!a.logoless)
		return "", nil
	}
	return "unknown command: " + lower, nil
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

// setLogoless toggles the header logo, mirroring k9s's ctrl-l.
func (a *App) setLogoless(v bool) {
	a.logoless = v
	if v {
		a.chrome.Logo = nil
	} else {
		a.chrome.Logo = logoLines
	}
	a.resizeActiveView()
}
