// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/google/shlex"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// Plugin is a validated plugin: its key as bubbletea names it, the views
// it is bound in (nil: every view), and what it runs.
type Plugin struct {
	Views   map[style.ViewType]bool
	Name    string
	Key     string
	Label   string
	Desc    string
	Command string
	Args    []string
	// Inputs is the form asked before it runs; each value reaches the
	// command as $INPUT_<NAME>, in its args and its environment.
	Inputs []views.PluginInput
	// Pipes are the commands its output runs through, each split into
	// words: command args | pipes[0] | pipes[1] ….
	Pipes      [][]string
	Background bool
	Confirm    bool
	Dangerous  bool
	Override   bool
	// OverwriteOutput is k9s's: a background plugin's first line of
	// output replaces the "done" flash, so a plugin can report its own
	// result. A foreground plugin's output is on the terminal already.
	OverwriteOutput bool
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
// the same view by another plugin or a hotkey, and inputs and pipes that
// make sense (pluginInputs, pluginPipes).
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
	key, err := checkPlugin(e)
	if err != nil {
		return Plugin{}, err
	}
	inputs, err := pluginInputs(e.Inputs)
	if err != nil {
		return Plugin{}, err
	}
	pipes, err := pluginPipes(e.Pipes)
	if err != nil {
		return Plugin{}, err
	}
	p := Plugin{
		Name: name, Key: key, Label: "<" + strings.ToLower(e.ShortCut) + ">",
		Desc: e.Description, Command: e.Command, Args: e.Args,
		Inputs: inputs, Pipes: pipes,
		Background: e.Background, Dangerous: e.Dangerous, Override: e.Override,
		OverwriteOutput: e.OverwriteOutput,
		// k9s's ShouldConfirm: confirm when asked to, and by default when
		// there are inputs — the values typed are shown before they run.
		Confirm: len(inputs) > 0,
	}
	if e.Confirm != nil {
		p.Confirm = *e.Confirm
	}
	if p.Desc == "" {
		p.Desc = name
	}
	scoped, all, err := pluginViews(e.Scopes)
	if err != nil {
		return Plugin{}, err
	}
	if !all {
		p.Views = scoped
	}
	return p, nil
}

// checkPlugin parses a plugin's shortcut and refuses an entry that cannot
// run: a dockmaster key, no command, no scopes, or background with pipes.
func checkPlugin(e config.Plugin) (string, error) {
	key, err := ParseShortcut(e.ShortCut)
	switch {
	case err != nil:
		return "", err
	case reservedKeys[key] || (len(key) == 1 && key[0] >= '0' && key[0] <= '9'):
		return "", fmt.Errorf("%s is a dockmaster key", e.ShortCut)
	case strings.TrimSpace(e.Command) == "":
		return "", errors.New("no command")
	case len(e.Scopes) == 0:
		return "", errors.New("no scopes — name the views it is for, or all")
	case e.Background && len(e.Pipes) > 0:
		// k9s starts such a pipeline without suspending the screen, so its
		// output lands on top of the UI; dockmaster refuses the pair.
		return "", errors.New("background and pipes together: a pipeline's output goes to the terminal, " +
			"which a background plugin does not have")
	}
	return key, nil
}

// pluginViews are the views a plugin's scopes name, read up to an "all",
// which makes it every view's.
func pluginViews(scopes []string) (scoped map[style.ViewType]bool, all bool, err error) {
	for _, s := range scopes {
		if strings.EqualFold(s, "all") {
			return nil, true, nil
		}
		vt, ok := scopeView(s)
		if !ok {
			return nil, false, fmt.Errorf(
				"scope %q is not a view — containers, images, volumes, networks, projects, runtimes, logs, … or all",
				s,
			)
		}
		if scoped == nil {
			scoped = map[style.ViewType]bool{}
		}
		scoped[vt] = true
	}
	return scoped, false, nil
}

// inputName is what an input may be called: it becomes $INPUT_<NAME>, so
// it has to read as one variable both to dockmaster's expansion and to a
// shell.
var inputName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// pluginInputs validates a plugin's inputs: k9s's rules — a name used
// once, a default valid for its type — plus a name that makes a variable,
// a type dockmaster knows, and a dropdown with options to pick from.
func pluginInputs(in []config.PluginInput) ([]views.PluginInput, error) {
	out := make([]views.PluginInput, 0, len(in))
	seen := map[string]string{}
	for i, e := range in {
		if !inputName.MatchString(e.Name) {
			return nil, fmt.Errorf("input %d: name %q must be letters, digits and _, not starting with a digit — "+
				"it becomes $INPUT_%s", i+1, e.Name, strings.ToUpper(e.Name))
		}
		upper := strings.ToUpper(e.Name)
		if prev, ok := seen[upper]; ok {
			return nil, fmt.Errorf("inputs %q and %q are both $INPUT_%s", prev, e.Name, upper)
		}
		seen[upper] = e.Name
		typ := e.Type
		if typ == "" {
			typ = views.InputString
		}
		if err := checkInputDefault(e, typ); err != nil {
			return nil, err
		}
		label := e.Label
		if label == "" {
			label = e.Name
		}
		out = append(out, views.PluginInput{
			Name: e.Name, Label: label, Type: typ, Default: e.Default,
			Options: e.Options, Required: e.Required,
		})
	}
	return out, nil
}

// checkInputDefault refuses a type dockmaster does not know, a dropdown
// without options, and a default not valid for its type.
func checkInputDefault(e config.PluginInput, typ string) error {
	switch typ {
	case views.InputString:
	case views.InputNumber:
		if e.Default != "" {
			if _, err := strconv.ParseFloat(e.Default, 64); err != nil {
				return fmt.Errorf("input %q: default %q is not a number", e.Name, e.Default)
			}
		}
	case views.InputBool:
		if e.Default != "" && e.Default != "true" && e.Default != "false" {
			return fmt.Errorf("input %q: a bool's default is true or false, not %q", e.Name, e.Default)
		}
	case views.InputDropdown:
		if len(e.Options) == 0 {
			return fmt.Errorf("input %q: a dropdown needs options", e.Name)
		}
		if e.Default != "" && !slices.Contains(e.Options, e.Default) {
			return fmt.Errorf("input %q: default %q is not one of its options", e.Name, e.Default)
		}
	default:
		return fmt.Errorf("input %q: type %q is not string, number, bool or dropdown", e.Name, e.Type)
	}
	return nil
}

// pluginPipes splits each pipe into words as k9s does (shlex), refusing
// one that does not split, or has fewer than two words: k9s skips those
// silently, which would run a different pipeline than the one written.
func pluginPipes(pipes []string) ([][]string, error) {
	out := make([][]string, 0, len(pipes))
	for _, pipe := range pipes {
		words, err := shlex.Split(pipe)
		if err != nil {
			return nil, fmt.Errorf("pipe %q: %w", pipe, err)
		}
		if len(words) < 2 {
			return nil, fmt.Errorf("pipe %q: k9s skips a pipe of fewer than two words and dockmaster refuses it — "+
				"give it an argument, or remove it", pipe)
		}
		out = append(out, words)
	}
	return out, nil
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
	err  error
	name string
	// output is stdout and stderr together, for a failure's reason;
	// stdout alone is what overwriteOutput shows.
	output     string
	stdout     string
	background bool
	overwrite  bool
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

// pendingPlugin is a plugin waiting on its inputs form, with the
// variables of the row it was started on — the form, not that row, is the
// active view by the time it is submitted.
type pendingPlugin struct {
	vars   map[string]string
	plugin Plugin
}

// runPlugin starts p on the selected row, once readonly allows: through
// its inputs form when it has inputs, else straight to confirm and run.
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
	if len(p.Inputs) > 0 {
		a.pluginPending = &pendingPlugin{plugin: p, vars: vars}
		a.setView(style.ViewPluginForm, views.NewPluginForm(p.Name, p.Desc, p.Inputs))
		a.pushView(style.ViewPluginForm)
		return nil
	}
	return a.launchPlugin(p, vars)
}

// submitPluginInputs runs the plugin whose form was submitted, each value
// added to its variables as INPUT_<NAME>. The form closes first, so the
// confirm and the plugin's own screen are over the view it started from.
func (a *App) submitPluginInputs(param string) tea.Cmd {
	sub, err := views.DecodePluginInputs(param)
	pending := a.pluginPending
	a.pluginPending = nil
	if a.view == style.ViewPluginForm {
		a.popView()
	}
	switch {
	case err != nil:
		a.errFlash = err.Error()
		return nil
	case pending == nil || pending.plugin.Name != sub.Plugin:
		a.errFlash = "plugin " + sub.Plugin + " is no longer waiting on its inputs"
		return nil
	}
	vars := maps.Clone(pending.vars)
	for _, in := range pending.plugin.Inputs {
		vars["INPUT_"+strings.ToUpper(in.Name)] = sub.Values[in.Name]
	}
	return a.launchPlugin(pending.plugin, vars)
}

// launchPlugin expands p's args from vars and runs it, after a confirm if
// it asks for one. Only args are expanded: pipes run as written, as in
// k9s.
func (a *App) launchPlugin(p Plugin, vars map[string]string) tea.Cmd {
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
	line := strings.Join(append([]string{p.Command}, argv...), " ")
	for _, pipe := range p.Pipes {
		line += " | " + strings.Join(pipe, " ")
	}
	a.ask("Run", fmt.Sprintf("run %s? %s", p.Desc, line))
	return nil
}

// execPlugin runs the command: with the terminal handed over, as s hands
// it to a shell, or in the background, its outcome a flash. A plugin with
// pipes runs as a pipeline, every command exec'd directly.
func (a *App) execPlugin(p Plugin, argv []string, vars map[string]string) tea.Cmd {
	bin, err := exec.LookPath(p.Command)
	if err != nil {
		a.errFlash = "plugin " + p.Name + ": " + p.Command + " is not on PATH"
		return nil
	}
	a.logPluginRun(p)
	env := os.Environ()
	for k, v := range vars {
		env = append(env, k+"="+v)
	}
	if len(p.Pipes) > 0 {
		return a.execPipeline(p, bin, argv, env)
	}
	if p.Background {
		a.flash = "plugin " + p.Name + " running…"
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), pluginTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, argv...) //nolint:gosec // the user's own plugin
			cmd.Env = env
			// Two writers mean exec copies stdout and stderr on two
			// goroutines, both into out: it takes a lock.
			var stdout bytes.Buffer
			out := &lockedBuffer{}
			cmd.Stdout, cmd.Stderr = io.MultiWriter(out, &stdout), out
			err := cmd.Run()
			return pluginDoneMsg{
				name: p.Name, background: true, err: err, output: out.String(),
				stdout: stdout.String(), overwrite: p.OverwriteOutput,
			}
		}
	}
	// context.Background(): the session is the user's, not a timeout's.
	cmd := exec.CommandContext(context.Background(), bin, argv...) //nolint:gosec // the user's own plugin
	cmd.Env = env
	return a.inTerminal(cmd, func(err error) tea.Msg { return pluginDoneMsg{name: p.Name, err: err} })
}

// execPipeline runs p's command and pipes as one pipeline in the
// terminal. Every word reaches its command as an argument — no shell sees
// any of it — and every command gets the plugin's environment.
func (a *App) execPipeline(p Plugin, bin string, argv, env []string) tea.Cmd {
	// context.Background(): the session is the user's, not a timeout's.
	ctx := context.Background()
	first := exec.CommandContext(ctx, bin, argv...) //nolint:gosec // the user's own plugin
	first.Env = env
	cmds := []*exec.Cmd{first}
	for _, words := range p.Pipes {
		pbin, err := exec.LookPath(words[0])
		if err != nil {
			a.errFlash = "plugin " + p.Name + ": pipe " + words[0] + " is not on PATH"
			return nil
		}
		c := exec.CommandContext(ctx, pbin, words[1:]...) //nolint:gosec // the user's own plugin
		c.Env = env
		cmds = append(cmds, c)
	}
	pl := &pipeline{cmds: cmds}
	done := func(err error) tea.Msg { return pluginDoneMsg{name: p.Name, err: err} }
	if a.execPipelineFn != nil {
		return a.execPipelineFn(pl, done)
	}
	return tea.Exec(pl, done)
}

func (a *App) handlePluginDone(msg pluginDoneMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.err != nil:
		reason := msg.err.Error()
		var exitErr *exec.ExitError
		if errors.As(msg.err, &exitErr) {
			reason = fmt.Sprintf("exited %d", exitErr.ExitCode())
		}
		a.logPluginDone(msg, reason)
		if last := lastLine(msg.output); last != "" {
			reason += ": " + last
		}
		a.errFlash = "plugin " + msg.name + " " + reason
		// Logged above, without the output the flash adds: it may echo
		// an input's value.
		a.loggedFlash = a.errFlash
	case msg.background:
		a.logPluginDone(msg, "")
		a.flash = "plugin " + msg.name + " done"
		// k9s's overwriteOutput: the plugin's own first line of output
		// says what it did. A plugin that printed nothing keeps "done".
		if line := firstLine(msg.stdout); msg.overwrite && line != "" {
			a.flash = line
		}
	default:
		a.logPluginDone(msg, "")
	}
	cmds := []tea.Cmd{a.refreshActiveView()}
	if !msg.background {
		cmds = append(cmds, tea.ClearScreen)
	}
	return a, tea.Batch(cmds...)
}

// lockedBuffer is a bytes.Buffer two goroutines can write to.
type lockedBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// firstLine is the first non-blank line of a plugin's output, as k9s's
// overwriteOutput shows it, made safe for the status bar: the output is
// the plugin's, and an escape or control character in it would act on the
// frame.
func firstLine(s string) string {
	for l := range strings.SplitSeq(s, "\n") {
		l = strings.TrimSpace(strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, xansi.Strip(l)))
		if l != "" {
			return l
		}
	}
	return ""
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
	if !selectionVars(a.activeView(), add) {
		return nil, false
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

// selectionVars adds the variables naming the selected row of a resource
// view, as name, value pairs; false when such a view has no selection.
func selectionVars(view views.View, add func(kv ...string)) bool {
	switch v := view.(type) {
	case *views.ContainersView:
		c, ok := v.Selected()
		add("NAME", c.Name, "CONTAINER", c.Name, "ID", c.ID, "IMAGE", c.Image,
			"PROJECT", c.Project, "SERVICE", c.Service, "STATE", c.State)
		return ok
	case *views.ImagesView:
		im, ok := v.Selected()
		ref := im.Ref()
		if im.Dangling {
			ref = im.ID
		}
		add("NAME", ref, "IMAGE", ref, "ID", im.ID)
		return ok
	case *views.VolumesView:
		vol, ok := v.Selected()
		add("NAME", vol.Name, "VOLUME", vol.Name, "DRIVER", vol.Driver, "MOUNTPOINT", vol.Mountpoint)
		return ok
	case *views.NetworksView:
		n, ok := v.Selected()
		add("NAME", n.Name, "NETWORK", n.Name, "ID", n.ID)
		return ok
	case *views.ProjectsView:
		p, ok := v.Selected()
		add("NAME", p.Name, "PROJECT", p.Name, "WORKING_DIR", p.WorkingDir,
			"CONFIG_FILES", strings.Join(p.ConfigFiles, ","))
		return ok
	case *views.RuntimesView:
		m, ok := v.Selected()
		add("NAME", m.Name, "PROVIDER", m.Provider)
		return ok
	case *views.LogsView:
		add("NAME", v.Title(), "CONTAINER", v.Title(), "ID", v.ContainerID())
	}
	return true
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
	return expandVars(arg, vars, nil)
}

// expandVars is expandPluginVars with each value passed through quote
// first, when it is not nil: a jump's regex filter quotes what it fills
// in, so a value matches itself. The text around the values is not
// quoted, and a value is never expanded again.
func expandVars(arg string, vars map[string]string, quote func(string) string) string {
	value := func(v string) string {
		if quote != nil {
			return quote(v)
		}
		return v
	}
	return pluginVarRe.ReplaceAllStringFunc(arg, func(m string) string {
		sub := pluginVarRe.FindStringSubmatch(m)
		name := sub[1] + sub[2]
		if v, ok := lookupVar(name, vars); ok {
			return value(v)
		}
		// "$NAME-suffix": a name, then text.
		if head, tail, ok := strings.Cut(name, "-"); ok && sub[2] != "" {
			if v, ok := lookupVar(head, vars); ok {
				return value(v) + "-" + tail
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
