package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// Plugin is a validated plugin: its key as bubbletea names it, the views
// it is bound in (nil: every view), and what it runs.
type Plugin struct {
	Views      map[style.ViewType]bool
	Name       string
	Key        string
	Label      string
	Desc       string
	Command    string
	Args       []string
	Background bool
	Confirm    bool
	Dangerous  bool
	Override   bool
}

// pluginTimeout bounds a background plugin; a foreground one is the
// user's for as long as they keep it.
const pluginTimeout = 10 * time.Minute

// pluginScopes are the view names a plugin's scopes may use, beyond the
// palette's own view names.
var pluginScopes = map[string]style.ViewType{
	"logs": style.ViewLogs, "log": style.ViewLogs,
	"files": style.ViewVolumeBrowse, "layers": style.ViewLayers, "node": style.ViewNode,
	"sd": style.ViewDumps, "dumps": style.ViewDumps,
}

func scopeView(name string) (style.ViewType, bool) {
	if vt, ok := ViewForCommand(name); ok {
		return vt, true
	}
	vt, ok := pluginScopes[strings.ToLower(name)]
	return vt, ok
}

// Plugins validates the plugins file before the UI starts, as the aliases
// and hotkeys files are: each has a key that parses and is not one
// dockmaster keeps, a command, scopes it understands, and no key taken in
// the same view by another plugin or a hotkey. k9s's pipes and inputs are
// refused rather than ignored, which would run a different command.
func Plugins(entries map[string]config.Plugin, hotKeys []HotKey) ([]Plugin, error) {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	var (
		out  []Plugin
		errs []string
	)
	for _, name := range names {
		e := entries[name]
		p, err := plugin(name, e)
		if err == nil {
			err = clash(p, out, hotKeys)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("plugin %q: %v", name, err))
			continue
		}
		out = append(out, p)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("plugins: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

func plugin(name string, e config.Plugin) (Plugin, error) {
	key, err := ParseShortcut(e.ShortCut)
	switch {
	case err != nil:
		return Plugin{}, err
	case reservedKeys[key] || (len(key) == 1 && key[0] >= '0' && key[0] <= '9'):
		return Plugin{}, fmt.Errorf("%s is a dockmaster key", e.ShortCut)
	case strings.TrimSpace(e.Command) == "":
		return Plugin{}, errors.New("no command")
	case len(e.Scopes) == 0:
		return Plugin{}, errors.New("no scopes — name the views it is for, or all")
	case len(e.Pipes) > 0 || len(e.Inputs) > 0:
		return Plugin{}, errors.New("pipes and inputs are not supported by dockmaster")
	}
	p := Plugin{
		Name: name, Key: key, Label: "<" + strings.ToLower(e.ShortCut) + ">",
		Desc: e.Description, Command: e.Command, Args: e.Args,
		Background: e.Background, Dangerous: e.Dangerous, Override: e.Override,
		Confirm: e.Confirm != nil && *e.Confirm,
	}
	if p.Desc == "" {
		p.Desc = name
	}
	for _, s := range e.Scopes {
		if strings.EqualFold(s, "all") {
			p.Views = nil
			return p, nil
		}
		vt, ok := scopeView(s)
		if !ok {
			return Plugin{}, fmt.Errorf(
				"scope %q is not a view — containers, images, volumes, networks, projects, runtimes, logs, … or all",
				s,
			)
		}
		if p.Views == nil {
			p.Views = map[style.ViewType]bool{}
		}
		p.Views[vt] = true
	}
	return p, nil
}

// clash reports p's key already bound, in a view they share, by an earlier
// plugin, or anywhere by a hotkey.
func clash(p Plugin, earlier []Plugin, hotKeys []HotKey) error {
	for _, hk := range hotKeys {
		if hk.Key == p.Key {
			return fmt.Errorf("%s is already a hotkey", p.Label)
		}
	}
	for _, q := range earlier {
		if q.Key != p.Key {
			continue
		}
		if p.Views == nil || q.Views == nil {
			return fmt.Errorf("%s is already plugin %q", p.Label, q.Name)
		}
		for vt := range p.Views {
			if q.Views[vt] {
				return fmt.Errorf("%s is already plugin %q in %s", p.Label, q.Name, style.ViewResource(vt)+"s")
			}
		}
	}
	return nil
}

// in reports whether p is bound in view vt.
func (p Plugin) in(vt style.ViewType) bool { return p.Views == nil || p.Views[vt] }

// pluginDoneMsg reports a plugin run's end.
type pluginDoneMsg struct {
	err        error
	name       string
	output     string
	background bool
}

// pluginKey runs the plugin bound to key in the active view, if any — the
// overriding ones before the view's own keys, the rest after them.
func (a *App) pluginKey(key string, override bool) (tea.Cmd, bool) {
	for _, p := range a.plugins {
		if p.Key == key && p.Override == override && p.in(a.view) {
			return a.runPlugin(p), true
		}
	}
	return nil, false
}

// runPlugin starts p on the selected row, once readonly and confirm allow.
func (a *App) runPlugin(p Plugin) tea.Cmd {
	if a.readonly && p.Dangerous {
		a.errFlash = "readonly mode — plugin " + p.Name + " is marked dangerous and refused"
		return nil
	}
	vars, ok := a.pluginVars()
	if !ok {
		a.errFlash = "plugin " + p.Name + " needs a selected row — nothing selected here"
		return nil
	}
	argv := make([]string, 0, len(p.Args))
	for _, arg := range p.Args {
		argv = append(argv, expandPluginVars(arg, vars))
	}
	if !p.Confirm {
		return a.execPlugin(p, argv, vars)
	}
	a.confirmDispatch = func(yes bool) (string, tea.Cmd) {
		if !yes {
			return "", nil
		}
		return "", a.execPlugin(p, argv, vars)
	}
	a.confirm.Open(fmt.Sprintf("run %s? %s", p.Desc, strings.Join(append([]string{p.Command}, argv...), " ")))
	return nil
}

// execPlugin runs the command: with the terminal handed over, as s hands
// it to a shell, or in the background, its outcome a flash.
func (a *App) execPlugin(p Plugin, argv []string, vars map[string]string) tea.Cmd {
	bin, err := exec.LookPath(p.Command)
	if err != nil {
		a.errFlash = "plugin " + p.Name + ": " + p.Command + " is not on PATH"
		return nil
	}
	env := os.Environ()
	for k, v := range vars {
		env = append(env, k+"="+v)
	}
	if p.Background {
		a.flash = "plugin " + p.Name + " running…"
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), pluginTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, argv...) //nolint:gosec // the user's own plugin
			cmd.Env = env
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			err := cmd.Run()
			return pluginDoneMsg{name: p.Name, background: true, err: err, output: out.String()}
		}
	}
	// context.Background(): the session is the user's, not a timeout's.
	cmd := exec.CommandContext(context.Background(), bin, argv...) //nolint:gosec // the user's own plugin
	cmd.Env = env
	return a.inTerminal(cmd, func(err error) tea.Msg { return pluginDoneMsg{name: p.Name, err: err} })
}

func (a *App) handlePluginDone(msg pluginDoneMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.err != nil:
		reason := msg.err.Error()
		var exitErr *exec.ExitError
		if errors.As(msg.err, &exitErr) {
			reason = fmt.Sprintf("exited %d", exitErr.ExitCode())
		}
		if last := lastLine(msg.output); last != "" {
			reason += ": " + last
		}
		a.errFlash = "plugin " + msg.name + " " + reason
	case msg.background:
		a.flash = "plugin " + msg.name + " done"
	}
	cmds := []tea.Cmd{a.refreshActiveView()}
	if !msg.background {
		cmds = append(cmds, tea.ClearScreen)
	}
	return a, tea.Batch(cmds...)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// pluginVars is what a plugin gets from the view: DOCKER_HOST, CONTEXT and
// FILTER always; the selected row's fields by name ($NAME, $IMAGE, …); and
// every column of it as $COL-<HEADER>, as k9s gives. ok is false in a
// table view with no row selected.
func (a *App) pluginVars() (map[string]string, bool) {
	vars := map[string]string{"FILTER": a.filter}
	if a.client != nil {
		vars["DOCKER_HOST"], vars["CONTEXT"] = a.client.Host, a.client.ContextName
	}
	add := func(kv ...string) {
		for i := 0; i+1 < len(kv); i += 2 {
			vars[kv[i]] = kv[i+1]
		}
	}
	switch v := a.activeView().(type) {
	case *views.ContainersView:
		c, ok := v.Selected()
		if !ok {
			return nil, false
		}
		add("NAME", c.Name, "CONTAINER", c.Name, "ID", c.ID, "IMAGE", c.Image,
			"PROJECT", c.Project, "SERVICE", c.Service, "STATE", c.State)
	case *views.ImagesView:
		im, ok := v.Selected()
		if !ok {
			return nil, false
		}
		ref := im.Ref()
		if im.Dangling {
			ref = im.ID
		}
		add("NAME", ref, "IMAGE", ref, "ID", im.ID)
	case *views.VolumesView:
		vol, ok := v.Selected()
		if !ok {
			return nil, false
		}
		add("NAME", vol.Name, "VOLUME", vol.Name, "DRIVER", vol.Driver, "MOUNTPOINT", vol.Mountpoint)
	case *views.NetworksView:
		n, ok := v.Selected()
		if !ok {
			return nil, false
		}
		add("NAME", n.Name, "NETWORK", n.Name, "ID", n.ID)
	case *views.ProjectsView:
		p, ok := v.Selected()
		if !ok {
			return nil, false
		}
		add("NAME", p.Name, "PROJECT", p.Name, "WORKING_DIR", p.WorkingDir,
			"CONFIG_FILES", strings.Join(p.ConfigFiles, ","))
	case *views.RuntimesView:
		m, ok := v.Selected()
		if !ok {
			return nil, false
		}
		add("NAME", m.Name, "PROVIDER", m.Provider)
	case *views.LogsView:
		add("NAME", v.Title(), "CONTAINER", v.Title(), "ID", v.ContainerID())
	}
	if t, ok := a.activeView().(views.Tabler); ok {
		row := t.Table().SelectedRow()
		if row == nil {
			return nil, false
		}
		for i, c := range t.Table().Columns() {
			if i < len(row) {
				// k9s's $COL-NAME, and $COL_NAME — the spelling a shell can read,
				// which arguments must see too, since they are expanded here.
				val, name := strings.TrimSpace(xansi.Strip(row[i])), colName(c.Title)
				vars["COL-"+name], vars["COL_"+name] = val, val
			}
		}
	}
	return vars, true
}

// colName is a column title as a $COL- name: "CPU%↑" is CPU, "IMAGE ID" is
// IMAGE_ID.
func colName(title string) string {
	t := strings.TrimRight(xansi.Strip(title), "↑↓")
	t = strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r >= 'a' && r <= 'z':
			return r - 32
		case r == ' ' || r == '-':
			return '_'
		}
		return -1
	}, t)
	return strings.Trim(t, "_")
}

var pluginVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_-]*)\}|\$([A-Za-z_][A-Za-z0-9_-]*)`)

// expandPluginVars fills $NAME and ${NAME} in a plugin argument from vars,
// then the environment, as k9s does — $COL-IMAGE included, which os.Expand
// would read as $COL. A name not found reads as empty.
func expandPluginVars(arg string, vars map[string]string) string {
	return pluginVarRe.ReplaceAllStringFunc(arg, func(m string) string {
		sub := pluginVarRe.FindStringSubmatch(m)
		name := sub[1] + sub[2]
		if v, ok := lookupVar(name, vars); ok {
			return v
		}
		// "$NAME-suffix": a name, then text.
		if head, tail, ok := strings.Cut(name, "-"); ok && sub[2] != "" {
			if v, ok := lookupVar(head, vars); ok {
				return v + "-" + tail
			}
		}
		return ""
	})
}

func lookupVar(name string, vars map[string]string) (string, bool) {
	if v, ok := vars[name]; ok {
		return v, true
	}
	return os.LookupEnv(name)
}

// pluginShortcuts are the active view's plugins, for the header.
func (a *App) pluginShortcuts() []chrome.Shortcut {
	var out []chrome.Shortcut
	for _, p := range a.plugins {
		if p.in(a.view) {
			out = append(out, chrome.Shortcut{Key: p.Label, Desc: p.Desc})
		}
	}
	return out
}

// pluginsHelp is the PLUGINS column of help: the active view's plugins.
func (a *App) pluginsHelp() (chrome.HelpSection, bool) {
	s := chrome.HelpSection{Title: "PLUGINS"}
	for _, p := range a.plugins {
		if p.in(a.view) {
			s.Entries = append(s.Entries, chrome.HelpEntry{Key: p.Label, Desc: p.Desc})
		}
	}
	return s, len(s.Entries) > 0
}
