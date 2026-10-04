// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/table"
	tktable "github.com/blairham/tuikit/table"
	"github.com/charmbracelet/x/ansi"

	"github.com/blairham/dockmaster/internal/docker"
)

// ExprColumn is a views.yaml expression column (#61), k9s's
// TITLE:<path>|<attributes>: a column read from the row's own data — the
// list response the view already has, never another daemon call. The path
// is a field of the row (.Image) or one of its labels (.Labels.<key>, a
// dot in the key escaped as \.).
type ExprColumn struct {
	// Title is the column's header, as written.
	Title string
	// Field is the row field read, or "Labels" for a label.
	Field string
	// Label is the label key when Field is "Labels".
	Label string
	// Right right-aligns the cells (R); Wide shows the column only in wide
	// mode (W); Age draws a time as an age and sorts by it (T); Numeric
	// sorts the cells as numbers, the rest after them (N).
	Right, Wide, Age, Numeric bool
}

// exprKind is what a row field holds, which decides the attributes it
// takes and how its cell is drawn.
type exprKind int

const (
	exprText exprKind = iota
	exprNumber
	exprTime
	exprBool
)

// exprField reads one field of a row. get returns nil for a row of
// another type, which draws as an empty cell.
type exprField struct {
	get  func(any) any
	kind exprKind
}

// field is an exprField reading f from rows of type T.
func field[T, V any](kind exprKind, f func(T) V) exprField {
	return exprField{kind: kind, get: func(x any) any {
		if r, ok := x.(T); ok {
			return f(r)
		}
		return nil
	}}
}

// exprSource is what a view's rows offer an expression: their labels and
// their fields, by the name a path gives them.
type exprSource struct {
	labels func(any) map[string]string
	fields map[string]exprField
}

// labelsOf reads the labels of rows of type T.
func labelsOf[T any](f func(T) map[string]string) func(any) map[string]string {
	return func(x any) map[string]string {
		if r, ok := x.(T); ok {
			return f(r)
		}
		return nil
	}
}

// exprSources are the views an expression column can be added to, by
// views.yaml key, and the fields each one's rows offer. The names are the
// row's own, which follow the Engine API's list response where it has the
// field. A view is here only when the items its sortRows is handed are
// these rows.
var exprSources = map[string]exprSource{
	"containers": {
		labels: labelsOf(func(c docker.Container) map[string]string { return c.Labels }),
		fields: map[string]exprField{
			"ID":      field(exprText, func(c docker.Container) string { return c.ID }),
			"Name":    field(exprText, func(c docker.Container) string { return c.Name }),
			"Image":   field(exprText, func(c docker.Container) string { return c.Image }),
			"ImageID": field(exprText, func(c docker.Container) string { return c.ImageID }),
			"Command": field(exprText, func(c docker.Container) string { return c.Command }),
			"State":   field(exprText, func(c docker.Container) string { return c.State }),
			"Status":  field(exprText, func(c docker.Container) string { return c.Status }),
			"Health":  field(exprText, func(c docker.Container) string { return c.Health }),
			"Ports":   field(exprText, func(c docker.Container) string { return c.Ports }),
			"Network": field(exprText, func(c docker.Container) string { return c.Network }),
			"IP":      field(exprText, func(c docker.Container) string { return c.IP }),
			"Project": field(exprText, func(c docker.Container) string { return c.Project }),
			"Service": field(exprText, func(c docker.Container) string { return c.Service }),
			"Created": field(exprTime, func(c docker.Container) time.Time { return c.Created }),
			"SizeRw":  field(exprNumber, func(c docker.Container) int64 { return c.SizeRw }),
		},
	},
	"images": {
		labels: labelsOf(func(i docker.Image) map[string]string { return i.Labels }),
		fields: map[string]exprField{
			"ID":         field(exprText, func(i docker.Image) string { return i.ID }),
			"Repo":       field(exprText, func(i docker.Image) string { return i.Repo }),
			"Tag":        field(exprText, func(i docker.Image) string { return i.Tag }),
			"Digest":     field(exprText, func(i docker.Image) string { return i.Digest }),
			"Created":    field(exprTime, func(i docker.Image) time.Time { return i.Created }),
			"Size":       field(exprNumber, func(i docker.Image) int64 { return i.Size }),
			"Containers": field(exprNumber, func(i docker.Image) int64 { return i.Containers }),
			"Dangling":   field(exprBool, func(i docker.Image) bool { return i.Dangling }),
		},
	},
	"volumes": {
		labels: labelsOf(func(v docker.Volume) map[string]string { return v.Labels }),
		fields: map[string]exprField{
			"Name":       field(exprText, func(v docker.Volume) string { return v.Name }),
			"Driver":     field(exprText, func(v docker.Volume) string { return v.Driver }),
			"Scope":      field(exprText, func(v docker.Volume) string { return v.Scope }),
			"Mountpoint": field(exprText, func(v docker.Volume) string { return v.Mountpoint }),
			"Project":    field(exprText, func(v docker.Volume) string { return v.Project }),
			"Created":    field(exprTime, func(v docker.Volume) time.Time { return v.Created }),
			"Size":       field(exprNumber, func(v docker.Volume) int64 { return v.Size }),
			"Refs":       field(exprNumber, func(v docker.Volume) int64 { return v.Refs }),
		},
	},
	"networks": {
		labels: labelsOf(func(n docker.Network) map[string]string { return n.Labels }),
		fields: map[string]exprField{
			"ID":         field(exprText, func(n docker.Network) string { return n.ID }),
			"Name":       field(exprText, func(n docker.Network) string { return n.Name }),
			"Driver":     field(exprText, func(n docker.Network) string { return n.Driver }),
			"Scope":      field(exprText, func(n docker.Network) string { return n.Scope }),
			"Subnet":     field(exprText, func(n docker.Network) string { return n.Subnet }),
			"Gateway":    field(exprText, func(n docker.Network) string { return n.Gateway }),
			"Project":    field(exprText, func(n docker.Network) string { return n.Project }),
			"Created":    field(exprTime, func(n docker.Network) time.Time { return n.Created }),
			"Containers": field(exprNumber, func(n docker.Network) int { return n.Containers }),
			"Internal":   field(exprBool, func(n docker.Network) bool { return n.Internal }),
			"Attachable": field(exprBool, func(n docker.Network) bool { return n.Attachable }),
			"IPv6":       field(exprBool, func(n docker.Network) bool { return n.IPv6 }),
		},
	},
}

// labelsField is the path's name for a row's labels.
const labelsField = "Labels"

// ExprViews is every view an expression column can be added to, sorted.
func ExprViews() []string {
	out := make([]string, 0, len(exprSources))
	for name := range exprSources {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// ExprFields is every field an expression on the named view can read,
// sorted, Labels among them; false for a view that takes none.
func ExprFields(view string) ([]string, bool) {
	src, ok := exprSources[view]
	if !ok {
		return nil, false
	}
	out := []string{labelsField}
	for name := range src.fields {
		out = append(out, name)
	}
	slices.Sort(out)
	return out, true
}

// IsExprColumn reports whether a views.yaml column entry is an expression
// column: a title, a colon, then a path. No view's own title has a colon.
func IsExprColumn(spec string) bool { return strings.Contains(spec, ":") }

// exprAttrs are the attributes an expression column takes, as listed in
// an error.
const exprAttrs = "R, L, W, T, N"

// ParseExprColumn reads one views.yaml expression column for the named
// view, TITLE:<path>|<attributes>, and checks it against what that view's
// rows hold. Every attribute it does not implement is refused, never
// ignored.
func ParseExprColumn(view, spec string) (ExprColumn, error) {
	title, rest, ok := strings.Cut(spec, ":")
	if !ok {
		return ExprColumn{}, errors.New(
			`not an expression column: TITLE:<path>, as in SVC:.Labels.com\.docker\.compose\.service`,
		)
	}
	e := ExprColumn{Title: strings.TrimSpace(title)}
	if err := checkExprTitle(e.Title); err != nil {
		return ExprColumn{}, err
	}
	src, ok := exprSources[view]
	if !ok {
		return ExprColumn{}, fmt.Errorf("the %s view takes no expression columns (only %s do)",
			view, strings.Join(ExprViews(), ", "))
	}
	segs, attrs, err := splitExprPath(strings.TrimSpace(rest))
	if err != nil {
		return ExprColumn{}, err
	}
	kind, err := e.resolve(view, src, segs)
	if err != nil {
		return ExprColumn{}, err
	}
	if err := e.setAttrs(view, kind, attrs); err != nil {
		return ExprColumn{}, err
	}
	return e, nil
}

// checkExprTitle refuses a title that could not head a column: empty, or
// holding a control character, a | or a sort arrow.
func checkExprTitle(title string) error {
	switch {
	case title == "":
		return errors.New(`no title before the colon, as in SVC:.Labels.com\.docker\.compose\.service`)
	case strings.ContainsAny(title, "|"+tktable.SortAscIndicator+tktable.SortDescIndicator):
		return fmt.Errorf("title %q: a title holds no | and no sort arrow", title)
	case strings.ContainsFunc(title, unicode.IsControl):
		return fmt.Errorf("title %q holds a control character", title)
	}
	return nil
}

// splitExprPath splits a path into its names, and what follows the first
// unescaped | — the attributes. A path starts with a dot, and each name
// follows a dot, space around it trimmed; \. is a dot within a name (a
// label key's), \| a bar and \\ a backslash.
func splitExprPath(p string) (segs []string, attrs string, err error) {
	if !strings.HasPrefix(p, ".") {
		return nil, "", fmt.Errorf("path %q: a path starts with a dot, as in .Image or .Labels.<key>", p)
	}
	var cur strings.Builder
	rs := []rune(p[1:])
	end := len(rs)
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; r {
		case '\\':
			if i+1 == len(rs) || !strings.ContainsRune(`.|\`, rs[i+1]) {
				return nil, "", fmt.Errorf(`path %q: \ escapes only a dot, a | or a \`, p)
			}
			i++
			cur.WriteRune(rs[i])
		case '.':
			segs = append(segs, cur.String())
			cur.Reset()
		case '|':
			end = i
			attrs = string(rs[i+1:])
			i = len(rs)
		default:
			cur.WriteRune(r)
		}
	}
	segs = append(segs, cur.String())
	for i := range segs {
		segs[i] = strings.TrimSpace(segs[i])
	}
	if slices.Contains(segs, "") {
		return nil, "", fmt.Errorf("path %q: an empty name — every dot is followed by one", "."+string(rs[:end]))
	}
	return segs, attrs, nil
}

// resolve sets e's field or label from a path's names, as the view's rows
// hold them, and returns what kind of value it reads.
func (e *ExprColumn) resolve(view string, src exprSource, segs []string) (exprKind, error) {
	path := "." + strings.Join(segs, ".")
	if segs[0] == labelsField {
		switch len(segs) {
		case 1:
			return 0, fmt.Errorf(`path %q: name a label, as in .Labels.com\.docker\.compose\.service`, path)
		case 2:
			e.Field, e.Label = labelsField, segs[1]
			return exprText, nil
		}
		return 0, fmt.Errorf(`path %q: labels are one level deep; escape the dots in a label key, as in `+
			`.Labels.com\.docker\.compose\.service`, path)
	}
	f, ok := src.fields[segs[0]]
	if !ok {
		names, _ := ExprFields(view)
		return 0, fmt.Errorf("path %q: %s rows have no field %q (fields are %s)",
			path, view, segs[0], strings.Join(names, ", "))
	}
	if len(segs) > 1 {
		return 0, fmt.Errorf("path %q: .%s has no fields under it", path, segs[0])
	}
	e.Field = segs[0]
	return f.kind, nil
}

// setAttrs applies k9s's column attributes, each a letter, to e: those
// dockmaster implements, and an error naming any other.
func (e *ExprColumn) setAttrs(view string, kind exprKind, attrs string) error {
	left := false
	for _, r := range strings.TrimSpace(attrs) {
		switch r {
		case 'R':
			e.Right = true
		case 'L':
			left = true
		case 'W':
			if len(columnSets[view]) < 2 {
				return fmt.Errorf("attribute W: the %s view has no wide mode", view)
			}
			e.Wide = true
		case 'T':
			if kind != exprTime {
				return fmt.Errorf("attribute T: %s is not a time (the times are .Created)", e.path())
			}
			e.Age = true
		case 'N':
			if kind != exprNumber && e.Label == "" {
				return fmt.Errorf("attribute N: %s is not a number or a label", e.path())
			}
			e.Numeric = true
		case 'H':
			return errors.New("attribute H is not supported: to hide a column, leave it out of columns")
		case 'S':
			return errors.New("attribute S is not supported: an expression column is always shown, unless W")
		default:
			return fmt.Errorf("attribute %q is not supported (the attributes are %s)", r, exprAttrs)
		}
	}
	if e.Right && left {
		return errors.New("attributes R and L: a column is aligned one way")
	}
	return nil
}

// pathEscaper escapes what splitExprPath unescapes.
var pathEscaper = strings.NewReplacer(`\`, `\\`, ".", `\.`, "|", `\|`)

// path is e's path as it would be written, its label key escaped.
func (e ExprColumn) path() string {
	if e.Field == labelsField {
		return "." + labelsField + "." + pathEscaper.Replace(e.Label)
	}
	return "." + e.Field
}

// width is the width an expression column is laid out at: its title's
// at least, and room for what its kind of value usually takes.
func (e ExprColumn) width(view string) int {
	w := 20
	if f, ok := exprSources[view].fields[e.Field]; ok {
		switch {
		case e.Age:
			w = 6
		case f.kind == exprTime:
			w = len(exprTimeLayout)
		case f.kind == exprNumber, f.kind == exprBool:
			w = 8
		}
	}
	return max(w, ansi.StringWidth(e.Title))
}

// column is e as a table column.
func (e ExprColumn) column(view string) table.Column {
	return table.Column{Title: e.Title, Width: e.width(view)}
}

// exprTimeLayout draws a time field without T: sortable as text.
const exprTimeLayout = "2006-01-02 15:04:05"

// cell is e's value on a row of the named view: "" for a label the row
// does not carry, a negative count or size (the daemon's "unknown"), and
// a zero time. A row of the wrong type, which no view hands over, is
// empty too.
func (e ExprColumn) cell(view string, row any) string {
	src, ok := exprSources[view]
	if !ok {
		return ""
	}
	if e.Field == labelsField {
		return cleanCell(src.labels(row)[e.Label])
	}
	f, ok := src.fields[e.Field]
	if !ok {
		return ""
	}
	switch v := f.get(row).(type) {
	case string:
		return cleanCell(v)
	case int64:
		return count(v)
	case int:
		return count(int64(v))
	case bool:
		return strconv.FormatBool(v)
	case time.Time:
		switch {
		case v.IsZero():
			return ""
		case e.Age:
			return since(v)
		}
		return v.Local().Format(exprTimeLayout)
	}
	return ""
}

// count is a count or size as a cell; a negative one is unknown.
func count(n int64) string {
	if n < 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

// cleanCell keeps a value from a label or a field on one line of its cell:
// a label is anybody's text, and an escape or a newline in it would act on
// the frame. Tabs and line breaks become spaces; every other control
// character, ESC and C1 included, is dropped.
func cleanCell(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

// alignRight pads s on the left to width cells.
func alignRight(s string, width int) string {
	if pad := width - ansi.StringWidth(s); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}

// numericCompare orders an N column: numbers by value, then everything
// else — an empty cell, a label that is not a number — after them, in
// tuikit's order. Every column already compares two numbers by value;
// what N changes is that tuikit's order puts an empty cell first.
func numericCompare(a, b string) int {
	x, errA := strconv.ParseFloat(strings.TrimSpace(a), 64)
	y, errB := strconv.ParseFloat(strings.TrimSpace(b), 64)
	switch {
	case errA == nil && errB == nil:
		return cmp.Compare(x, y)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return tktable.NaturalCompare(a, b)
}
