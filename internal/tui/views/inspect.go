package views

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/tail"

	"github.com/blairham/dockyard/internal/docker"
	"github.com/blairham/dockyard/internal/tui/style"
)

// InspectKind is which object an InspectView is showing.
type InspectKind int

// Inspect kinds.
const (
	InspectContainer InspectKind = iota
	InspectImage
	InspectVolume
	InspectNetwork
)

func (k InspectKind) String() string {
	switch k {
	case InspectImage:
		return "image"
	case InspectVolume:
		return "volume"
	case InspectNetwork:
		return "network"
	default:
		return "container"
	}
}

// InspectRefreshMsg carries inspect JSON.
type InspectRefreshMsg struct {
	Err  error
	ID   string
	Body []byte
	Kind InspectKind
}

// InspectView is a read-only, syntax-colored viewport over inspect JSON.
type InspectView struct {
	// plain shows the fetched body as styled text, not colorized JSON.
	plain  bool
	tail   *tail.Model
	client *docker.Client

	err error

	id    string
	name  string
	kind  InspectKind
	lines int

	width  int
	height int

	loading bool

	// fetch, when set, replaces the daemon call: the view shows whatever
	// it returns — a runtime's own description of a machine, say.
	fetch func(context.Context) ([]byte, error)
}

// NewInspectView builds an inspect view for one object.
func NewInspectView(client *docker.Client, kind InspectKind, id, name string) *InspectView {
	t := tail.New()
	// Inspect output is a document, not a stream — pin it to the top and
	// leave it there, or every refresh would yank the user to the bottom.
	t.SetFollow(false)
	paintTailBackground(t)
	return &InspectView{client: client, tail: t, kind: kind, id: id, name: name, loading: true}
}

// NewInspectFetchView shows what fetch returns, indented when it is JSON.
func NewInspectFetchView(name string, fetch func(context.Context) ([]byte, error)) *InspectView {
	v := NewInspectView(nil, InspectContainer, "", name)
	v.fetch = fetch
	return v
}

// Title is the object's name, for the border title.
func (v *InspectView) Title() string {
	if v.name != "" {
		return v.name
	}
	return v.id
}

// Kind is the inspected object kind.
func (v *InspectView) Kind() InspectKind { return v.kind }

// Init kicks off the fetch.
func (v *InspectView) Init() tea.Cmd { return v.Refresh() }

// Update folds the fetch result in.
func (v *InspectView) Update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(InspectRefreshMsg)
	if !ok || m.ID != v.id || m.Kind != v.kind {
		return nil
	}
	v.loading = false
	if m.Err != nil {
		v.err = docker.FormatUserError(m.Err)
		return nil
	}
	v.err = nil

	raw := strings.Split(strings.TrimRight(string(m.Body), "\n"), "\n")
	styled := raw
	if !v.plain {
		styled = make([]string, 0, len(raw))
		for _, l := range raw {
			styled = append(styled, colorizeJSON(l))
		}
	}
	v.lines = len(styled)

	// Replace rather than append: this is a document that gets refetched,
	// so appending would stack N copies of the same JSON in the buffer.
	v.tail.SetFilter("")
	fresh := tail.New()
	fresh.SetFollow(false)
	paintTailBackground(fresh)
	w, h := v.dims()
	fresh.Resize(w, h)
	fresh.AppendLines(styled)
	v.tail = fresh
	return nil
}

// dims remembers the last size so a rebuilt viewport keeps it. tail.Model
// exposes no getter, so the view tracks it alongside.
func (v *InspectView) dims() (int, int) { return v.width, v.height }

// UpdateTable forwards scroll keys.
func (v *InspectView) UpdateTable(msg tea.Msg) tea.Cmd { return v.tail.Update(msg) }

// Resize re-lays the viewport.
func (v *InspectView) Resize(width, height int) {
	v.width, v.height = width, height
	v.tail.Resize(width, height)
}

// Count is the visible line count.
func (v *InspectView) Count() int { return v.tail.VisibleCount() }

// Loading reports whether the fetch is outstanding.
func (v *InspectView) Loading() bool { return v.loading }

// SetFilter applies a regex filter over the JSON lines.
func (v *InspectView) SetFilter(f string) { v.tail.SetFilter(f) }

// HandleKey maps a keystroke to an app action.
func (v *InspectView) HandleKey(key string) (string, string) {
	if v.kind == InspectContainer {
		switch key {
		case "l":
			return "logs", v.id
		case "s":
			return "exec", v.id
		}
	}
	return "", ""
}

// View renders the viewport.
func (v *InspectView) View() string {
	if v.err != nil {
		return style.Error.Render(fmt.Sprintf("  %v", v.err))
	}
	if !v.tail.Ready() {
		return ""
	}
	return v.tail.View()
}

// Refresh refetches the inspect body.
func (v *InspectView) Refresh() tea.Cmd {
	kind, id, client := v.kind, v.id, v.client
	if fetch := v.fetch; fetch != nil {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			body, err := fetch(ctx)
			if err == nil && json.Valid(body) {
				var buf bytes.Buffer
				if json.Indent(&buf, bytes.TrimSpace(body), "", "  ") == nil {
					body = buf.Bytes()
				}
			}
			return InspectRefreshMsg{Kind: kind, ID: id, Body: body, Err: err}
		}
	}
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(20 * time.Second)
		defer cancel()

		var (
			body []byte
			err  error
		)
		switch kind {
		case InspectImage:
			body, err = client.InspectImage(ctx, id)
		case InspectVolume:
			body, err = client.InspectVolume(ctx, id)
		case InspectNetwork:
			body, err = client.InspectNetwork(ctx, id)
		default:
			body, err = client.InspectContainer(ctx, id)
		}
		return InspectRefreshMsg{Kind: kind, ID: id, Body: body, Err: err}
	}
}

var (
	jsonKeyStyle = lipgloss.NewStyle().Foreground(style.ColorDockerBlue)
	jsonStrStyle = lipgloss.NewStyle().Foreground(style.ColorGreen)
	jsonNumStyle = lipgloss.NewStyle().Foreground(style.ColorPapayaWhip)
	jsonNilStyle = lipgloss.NewStyle().Foreground(style.ColorSlateGray)
)

// colorizeJSON tints one already-indented JSON line. This is deliberately
// a line-shaped heuristic rather than a tokenizer: the viewport renders a
// few thousand lines on every refresh, and a real parse per line is both
// slower and no more correct for output the daemon already validated.
func colorizeJSON(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(trimmed)]

	key, rest, ok := strings.Cut(trimmed, ": ")
	if !ok || !strings.HasPrefix(key, `"`) {
		return indent + colorizeJSONValue(trimmed)
	}
	return indent + jsonKeyStyle.Render(key) + ": " + colorizeJSONValue(rest)
}

func colorizeJSONValue(s string) string {
	bare := strings.TrimRight(s, ",")
	suffix := s[len(bare):]

	switch {
	case bare == "null":
		return jsonNilStyle.Render(bare) + suffix
	case bare == "true" || bare == "false":
		return jsonNumStyle.Render(bare) + suffix
	case strings.HasPrefix(bare, `"`):
		return jsonStrStyle.Render(bare) + suffix
	case len(bare) > 0 && (bare[0] == '-' || (bare[0] >= '0' && bare[0] <= '9')):
		return jsonNumStyle.Render(bare) + suffix
	default:
		return s
	}
}
