// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chrome"
	tktable "github.com/blairham/tuikit/table"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// Jump is a validated jumps.yaml entry: enter on a row of From opens To,
// filtered by Template with the row's $VARs filled in — as a -l selector
// when Label, else as a / filter.
type Jump struct {
	Template string
	ToName   string
	From     style.ViewType
	To       style.ViewType
	Label    bool
}

// jumpRowVars are the $VARs each view a jump may start from gives its
// selected row — pluginVars' names, which TestJumpVarsArePluginVars keeps
// in step — beside the ones every view has and the row's $COL-<HEADER>s.
var jumpRowVars = map[style.ViewType][]string{
	style.ViewContainers: {"NAME", "CONTAINER", "ID", "IMAGE", "PROJECT", "SERVICE", "STATE"},
	style.ViewImages:     {"NAME", "IMAGE", "ID"},
	style.ViewVolumes:    {"NAME", "VOLUME", "DRIVER", "MOUNTPOINT"},
	style.ViewNetworks:   {"NAME", "NETWORK", "ID"},
	style.ViewProjects:   {"NAME", "PROJECT", "WORKING_DIR", "CONFIG_FILES"},
	style.ViewRuntimes:   {"NAME", "PROVIDER"},
}

// jumpCommonVars are the $VARs every view gives.
var jumpCommonVars = []string{"DOCKER_HOST", "CONTEXT", "FILTER"}

// Jumps validates the jumps file before the UI starts, as the plugins file
// is: each entry is keyed by a view whose rows a jump can read, names a
// view to open, and has exactly one of a label selector, for a view whose
// rows have labels, or a filter, every $VAR in it one the row gives.
func Jumps(entries map[string]config.Jump) ([]Jump, error) {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	var (
		out  []Jump
		errs []string
		from = map[style.ViewType]string{}
	)
	for _, name := range names {
		j, err := jump(name, entries[name])
		if err == nil {
			if prev, ok := from[j.From]; ok {
				err = fmt.Errorf("%q is the same view, and a view has one jump", prev)
			}
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("jump %q: %v", name, err))
			continue
		}
		from[j.From] = name
		out = append(out, j)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("jumps: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

func jump(name string, e config.Jump) (Jump, error) {
	src, ok := ViewForCommand(name)
	if _, rows := jumpRowVars[src]; !ok || !rows {
		return Jump{}, errors.New("a jump starts from containers, images, volumes, networks, projects or runtimes")
	}
	dst, ok := ViewForCommand(e.TargetView)
	if !ok {
		return Jump{}, fmt.Errorf("targetView %q is not a view — %s", e.TargetView, strings.Join(ViewCommandNames(), ", "))
	}
	label, filter := strings.TrimSpace(e.LabelSelector), strings.TrimSpace(e.Filter)
	j := Jump{From: src, To: dst, ToName: strings.ToLower(strings.TrimSpace(e.TargetView)), Template: filter}
	switch {
	case (label == "") == (filter == ""):
		return Jump{}, errors.New("give one of labelSelector or filter")
	case label != "":
		if !labelledViews[dst] {
			return Jump{}, fmt.Errorf("labelSelector: %s rows have no labels — containers, images, volumes and "+
				"networks do; use filter", e.TargetView)
		}
		j.Template, j.Label = label, true
		if err := validSelector(label); err != nil {
			return Jump{}, fmt.Errorf("labelSelector %q: %w", label, err)
		}
	case strings.HasPrefix(filter, "-l ") || filter == "-l":
		return Jump{}, errors.New("a label filter is labelSelector, not filter")
	}
	if err := jumpVarsKnown(src, j.Template); err != nil {
		return Jump{}, err
	}
	return j, nil
}

// validSelector checks a label selector as tuikit's -l reads it, with
// every $VAR standing for a value: comma-separated terms, each a key with
// no space, = or ! in it — k=v, k==v, k!=v, k or !k. tuikit makes a
// malformed selector match nothing; a jump's is refused at load.
func validSelector(s string) error {
	s = pluginVarRe.ReplaceAllString(s, "v")
	terms := 0
	for raw := range strings.SplitSeq(s, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		terms++
		key := strings.TrimPrefix(raw, "!")
		if i := strings.IndexByte(raw, '='); i >= 0 {
			key = strings.TrimSuffix(raw[:i], "!")
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, "=! \t") {
			return fmt.Errorf("term %q has no label key", raw)
		}
	}
	if terms == 0 {
		return errors.New("no terms")
	}
	return nil
}

// jumpVarsKnown refuses a $VAR the source view's rows do not give: a typo
// would otherwise fill in empty and quietly filter on nothing.
func jumpVarsKnown(src style.ViewType, tmpl string) error {
	known := map[string]bool{}
	for _, n := range append(append([]string{}, jumpCommonVars...), jumpRowVars[src]...) {
		known[n] = true
	}
	for _, m := range pluginVarRe.FindAllStringSubmatch(tmpl, -1) {
		name := m[1] + m[2]
		if known[name] || strings.HasPrefix(name, "COL-") || strings.HasPrefix(name, "COL_") {
			continue
		}
		// "$NAME-suffix": a name, then text, as expandPluginVars reads it.
		if head, _, ok := strings.Cut(name, "-"); ok && m[2] != "" && known[head] {
			continue
		}
		return fmt.Errorf("$%s is not a variable %s rows give — %s, or $COL-<HEADER>", name,
			style.ViewResource(src), strings.Join(jumpRowVars[src], ", "))
	}
	return nil
}

// jumpFor is the jump set on the active view, if any.
func (a *App) jumpFor() (Jump, bool) {
	for _, j := range a.jumps {
		if j.From == a.view {
			return j, true
		}
	}
	return Jump{}, false
}

// jumpKey is enter in a view with a jump: the target view, pushed over
// this one so esc comes back, filtered by the jump with the selected row's
// values filled in. A row with no selection leaves enter to the view.
func (a *App) jumpKey(key string) (tea.Cmd, bool) {
	if key != "enter" {
		return nil, false
	}
	j, ok := a.jumpFor()
	if !ok {
		return nil, false
	}
	vars, ok := a.pluginVars()
	if !ok {
		return nil, false
	}
	filter, err := j.expand(vars)
	if err != nil {
		a.errFlash = "jump to " + j.ToName + ": " + err.Error()
		return nil, true
	}
	a.pushView(j.To)
	a.filter = filter
	a.setActiveFilter(filter)
	if av := a.activeView(); av != nil {
		return av.Refresh(), true
	}
	return nil, true
}

// expand fills the row's values into the jump's filter, once each. In a
// regex filter a value is quoted, so it matches itself and nothing wider;
// in a label selector a value holding a comma would add a term, so it is
// refused.
func (j Jump) expand(vars map[string]string) (string, error) {
	if j.Label {
		var bad string
		sel := expandVars(j.Template, vars, func(v string) string {
			if strings.Contains(v, ",") && bad == "" {
				bad = v
			}
			return v
		})
		if bad != "" {
			return "", fmt.Errorf("the value %q holds a comma, which would split the selector", bad)
		}
		return "-l " + sel, nil
	}
	if tktable.ParseFilter(j.Template).Kind() == tktable.FilterFuzzy {
		return expandVars(j.Template, vars, nil), nil
	}
	return expandVars(j.Template, vars, regexp.QuoteMeta), nil
}

// jumpHelp is the JUMP column of help, over a view with a jump.
func (a *App) jumpHelp() (chrome.HelpSection, bool) {
	j, ok := a.jumpFor()
	if !ok {
		return chrome.HelpSection{}, false
	}
	return chrome.HelpSection{
		Title:   "JUMP",
		Entries: []chrome.HelpEntry{{Key: "<enter>", Desc: "Jump to " + j.ToName}},
	}, true
}
