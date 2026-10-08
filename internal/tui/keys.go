// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"
	tktable "github.com/blairham/tuikit/table"
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
	"sd",
	"lint",
	"xray",
	"xray net",
	"xray vol",
	"xray img",
	"hostshell",
	"pulses",
	"help",
	"dir",
	"screendump",
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
	"delete all",
	"delete all images",
	"delete all volumes",
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
	"pulses": style.ViewPulses, "pulse": style.ViewPulses, "pu": style.ViewPulses,
}

// ViewForCommand resolves a view name as the palette spells it.
func ViewForCommand(name string) (style.ViewType, bool) {
	vt, ok := viewCommands[strings.ToLower(strings.TrimSpace(name))]
	return vt, ok
}

// ViewCommandNames lists the canonical view names, for error messages.
func ViewCommandNames() []string {
	return []string{
		"containers", "images", "volumes", "networks", "projects", "runtimes",
		"events", "pods", "pf", "df", "pulses",
	}
}

// fuzzyMatch picks the best command for a partial input: the shortest
// command that extends it, then the input itself when it is a command,
// then subsequence matches, shortest winning ties. Extending first is what
// gives the palette something to show — `q` suggests `q!` and `qu` suggests
// `quit`, as k9s does — where a command that only matched itself would
// leave no completion to draw.
func fuzzyMatch(input string, aliases ...string) string {
	if input == "" {
		return ""
	}
	lower := strings.ToLower(input)
	candidates := append(append([]string{"aliases"}, knownCommands...), aliases...)

	var best string
	for _, cmd := range candidates {
		if len(cmd) > len(lower) && strings.HasPrefix(cmd, lower) && (best == "" || len(cmd) < len(best)) {
			best = cmd
		}
	}
	if best != "" {
		return best
	}
	if slices.Contains(candidates, lower) {
		return lower
	}
	for _, cmd := range candidates {
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

// keyAliases opens the aliases view, as in k9s.
const keyAliases = "ctrl+a"

// handleKey is the whole key path.
//
//nolint:gocyclo,gocognit // flat key dispatch
func (a *App) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		if a.noExitOnCtrlC {
			a.flash = "ctrl-c does not quit (noExitOnCtrlC) — :q quits"
			return a, nil
		}
		a.shutdown()
		return a, tea.Quit
	}

	// Any keypress clears a flash — it has been read, or it has not, and
	// either way it must not outlive the next interaction.
	a.errFlash, a.loggedFlash = "", ""
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
	// k9s's aliases view: every command and alias, enter running one. The
	// bars got ctrl+a first, so inside them it is still line-start.
	case keyAliases:
		a.showAliases()
		return a, nil
	}

	// A plugin, then a hotkey, that overrides a view's own key goes first.
	if cmd, ok := a.pluginKey(key, true); ok {
		return a, cmd
	}
	if cmd, ok := a.hotKey(key, true); ok {
		return a, cmd
	}

	// With rows marked, lifecycle and remove keys act on all of them.
	if cmd, ok := a.bulkKey(key); ok {
		return a, cmd
	}
	// A jump (jumps.yaml) takes over its view's enter, as k9s's does.
	if cmd, ok := a.jumpKey(key); ok {
		return a, cmd
	}

	// View-specific keys next, then table navigation.
	if action, param := a.activeViewHandleKey(key); action != "" {
		return a.handleAction(action, param)
	}
	// Then the keys every table shares.
	if cmd, ok := a.tableKey(key); ok {
		return a, cmd
	}
	// Then the user's plugins and hotkeys — after the view's own keys, so a
	// view that binds the same key keeps it.
	if cmd, ok := a.pluginKey(key, false); ok {
		return a, cmd
	}
	if cmd, ok := a.hotKey(key, false); ok {
		return a, cmd
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
	// A label filter can only match rows that have labels; anywhere else
	// it empties the list, which reads like a broken filter unless said.
	if tktable.ParseFilter(value).Kind() == tktable.FilterLabel && !labelledViews[a.view] {
		a.flash = "-l filters labels: containers, images, volumes and networks have them, this view does not"
	}
}

// labelledViews are the views whose rows carry labels a -l filter reads.
var labelledViews = map[style.ViewType]bool{
	style.ViewContainers: true, style.ViewImages: true, style.ViewVolumes: true, style.ViewNetworks: true,
}

// dispatchCommand parses a `:` command.
//
//nolint:gocyclo // flat command table
func (a *App) dispatchCommand(input string) (string, tea.Cmd) {
	raw, err := expand(a.aliases, strings.TrimSpace(input))
	if err != nil {
		// Validated at startup, so only a command typed at the palette.
		raw = strings.TrimSpace(input)
	}
	// `:<view> @context`, k9s's: switch to the context, then open the view.
	if ctxName, rest, ok := cutContextArg(raw); ok {
		return a.viewInContext(ctxName, rest)
	}
	lower := strings.ToLower(raw)

	// A view with a filter: `:containers /postgres`, what an alias such as
	// `pg: containers /postgres` stands for. The filter keeps its case.
	if name, filter, ok := strings.Cut(raw, " /"); ok {
		if vt, ok := ViewForCommand(name); ok {
			cmd := a.switchView(vt)
			a.filter = strings.TrimSpace(filter)
			a.setActiveFilter(a.filter)
			return "", cmd
		}
	}

	// Commands taking an argument.
	// The reference keeps its case: a tag may hold capitals (`app:RC1`).
	if rest, ok := cutPrefixFold(raw, "pull "); ok {
		_, cmd := a.handleAction("pull", strings.TrimSpace(rest))
		return "", cmd
	}
	// :dir <path> keeps the path's case; ~ is the home directory.
	if rest, ok := strings.CutPrefix(raw, "dir "); ok {
		dir := strings.TrimSpace(rest)
		if home, err := os.UserHomeDir(); err == nil && (dir == "~" || strings.HasPrefix(dir, "~/")) {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return "not a directory: " + dir, nil
		}
		_, cmd := a.handleAction("dir", dir)
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
	// :xray <root>, k9s's: the tree grown from networks, volumes or images.
	if f := strings.Fields(lower); len(f) == 2 && (f[0] == "xray" || f[0] == "x") {
		root, ok := views.XrayRoot(f[1])
		if !ok {
			return "xray grows from projects, net, vol or img — not " + f[1], nil
		}
		_, cmd := a.handleAction("xray", root)
		return "", cmd
	}

	switch lower {
	case "aliases", "alias":
		a.showAliases()
		return "", nil
	case "q", "q!", "quit", "exit":
		a.shutdown()
		return "", tea.Quit
	case "logs", "log":
		return a.rowCommand("logs", func(act string) bool { return act == "logs" || act == "project_logs" }, "l")
	case "inspect", "describe":
		return a.rowCommand("inspect", isInspectAction, "o")
	// :stats stays the CPU/MEM poll toggle below; the stats view is S.
	case "top", "diff", "health":
		key := map[string]string{"top": "T", "diff": "D", "health": "H"}[lower]
		return a.rowCommand(lower, func(act string) bool { return act == lower }, key)
	case "context", "ctx", "contexts":
		_, cmd := a.handleAction("contexts", "")
		return "", cmd
	case "lint":
		_, cmd := a.handleAction("lint", "")
		return "", cmd
	case "xray", "x":
		_, cmd := a.handleAction("xray", "")
		return "", cmd
	case "hostshell":
		_, cmd := a.handleAction("confirm_host_shell", "")
		return "", cmd
	case "help", "h", "?":
		a.showHelp = true
		return "", nil
	case "dir":
		dir, err := os.Getwd()
		if err != nil {
			return "no working directory: " + err.Error(), nil
		}
		_, cmd := a.handleAction("dir", dir)
		return "", cmd
	case "sd", "screendump", "screendumps", "dumps":
		_, cmd := a.handleAction("dumps", "")
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
	case "delete all":
		// The view decides what "all" is; anywhere else, say how to name it.
		switch a.view {
		case style.ViewImages:
			_, cmd := a.handleAction("confirm_delete_all_images", "")
			return "", cmd
		case style.ViewVolumes:
			_, cmd := a.handleAction("confirm_delete_all_volumes", "")
			return "", cmd
		}
		return "delete all works in the images or volumes view — or :delete all images / :delete all volumes", nil
	case "delete all images":
		_, cmd := a.handleAction("confirm_delete_all_images", "")
		return "", cmd
	case "delete all volumes":
		_, cmd := a.handleAction("confirm_delete_all_volumes", "")
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

// cutContextArg finds an @context word before any /filter and returns it
// and the command without it: "containers @prod /web" is ("prod",
// "containers /web"). A filter is the user's text, so an @ in it is not
// a context.
func cutContextArg(raw string) (name, rest string, ok bool) {
	head, filter, hasFilter := strings.Cut(raw, " /")
	words := strings.Fields(head)
	for i, w := range words {
		if len(w) > 1 && strings.HasPrefix(w, "@") {
			name = w[1:]
			rest = strings.Join(append(words[:i:i], words[i+1:]...), " ")
			if hasFilter {
				rest += " /" + filter
			}
			return name, rest, true
		}
	}
	return "", raw, false
}

// viewInContext opens the view rest names in context name, switching to
// the context first unless it is the one on screen.
func (a *App) viewInContext(name, rest string) (string, tea.Cmd) {
	view, filter, _ := strings.Cut(rest, " /")
	head := strings.ToLower(strings.TrimSpace(view))
	vt, isView := ViewForCommand(head)
	land := &landing{view: vt, filter: strings.TrimSpace(filter)}
	if !isView {
		// Any other command runs once the switch is done (#57) — but not
		// one that leaves or switches context itself.
		if err := validateCommand(rest, a.aliases); err != nil || noContextCommands[strings.Fields(head + " x")[0]] {
			return "@context goes with a command that opens a view, as in :xray @" + name, nil
		}
		land = &landing{command: rest}
	}
	for _, c := range docker.Contexts() {
		if !strings.EqualFold(c.Name, name) {
			continue
		}
		if a.client != nil && a.client.ContextName == c.Name {
			if land.command != "" {
				return a.dispatchCommand(land.command)
			}
			cmd := a.switchView(vt)
			a.filter = land.filter
			a.setActiveFilter(a.filter)
			return "", cmd
		}
		a.flash = "connecting to " + c.Name + "..."
		return "", doSwitchContextTo(c.Name, c.Host, land)
	}
	return "no such docker context: " + name, nil
}

// noContextCommands are refused with @context: they quit, or switch
// context themselves.
var noContextCommands = map[string]bool{
	"q": true, "q!": true, "quit": true, "exit": true, "ctx": true, "context": true, "contexts": true,
}

// argCommands take an argument after their name. xray's must be a root it
// can grow from (validateCommand).
var argCommands = map[string]bool{
	"pull": true, "dir": true, "ctx": true, "context": true, "prune": true, "xray": true,
}

// ValidateCommand checks a command line as -c and defaultView take it: an
// alias expands first, then it must be one the palette runs — a view (with
// an @context and a /filter, as at the palette), one of knownCommands, or
// one of those that take an argument (#57). The context itself is looked
// up when the command runs, since the store can change.
func ValidateCommand(input string, aliases map[string]string) error {
	if err := validateCommand(input, aliases); err != nil {
		return fmt.Errorf("unknown command %q — a view (%s) or any : command", input,
			strings.Join(ViewCommandNames(), ", "))
	}
	return nil
}

func validateCommand(input string, aliases map[string]string) error {
	raw, err := expand(aliases, strings.TrimSpace(input))
	if err != nil {
		return err
	}
	if _, rest, ok := cutContextArg(raw); ok {
		raw = rest
	}
	head, _, _ := strings.Cut(raw, " /")
	head = strings.ToLower(strings.TrimSpace(head))
	if _, ok := ViewForCommand(head); ok || head == "aliases" || head == "alias" {
		return nil
	}
	if slices.Contains(knownCommands, head) || isOtherSpelling(head) {
		return nil
	}
	if f := strings.Fields(head); len(f) > 1 && argCommands[f[0]] {
		if f[0] != "xray" {
			return nil
		}
		if _, ok := views.XrayRoot(f[1]); ok && len(f) == 2 {
			return nil
		}
	}
	return errors.New("not a command")
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

// cutPrefixFold is strings.CutPrefix with the prefix matched without
// regard to case and the rest returned as written.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return s, false
	}
	return s[len(prefix):], true
}
