// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeLogs serves canned streams by container ID and records how each was
// opened, so MergeLogs can be driven without a daemon.
type fakeLogs struct {
	lines  map[string][]string
	fail   map[string]error
	opened map[string][]time.Time
	mu     sync.Mutex
}

func (f *fakeLogs) open(_ context.Context, id string, since time.Time) (*LogStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened[id] = append(f.opened[id], since)
	if err := f.fail[id]; err != nil {
		return nil, err
	}
	ch := make(chan LogLine, len(f.lines[id]))
	for _, l := range f.lines[id] {
		ch <- LogLine{Text: l}
	}
	close(ch)
	return &LogStream{Lines: ch, Err: make(chan error, 1)}, nil
}

// collect reads lines until n arrive or a second passes.
func collect(t *testing.T, st *LogStream, n int) []LogLine {
	t.Helper()
	var got []LogLine
	timeout := time.After(time.Second)
	for len(got) < n {
		select {
		case l, ok := <-st.Lines:
			if !ok {
				return got
			}
			got = append(got, l)
		case <-timeout:
			t.Fatalf("got %d of %d lines: %+v", len(got), n, got)
		}
	}
	return got
}

func render(ls []LogLine) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		s := l.Source + "|" + l.Text
		if l.Note {
			s += " (note)"
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TestMergeLogs: every member's lines arrive tagged with its label, a member
// that ends leaves a note instead of ending the stream, one that cannot be
// opened says so, and the merged stream closes only when its context does.
func TestMergeLogs(t *testing.T) {
	f := &fakeLogs{
		lines:  map[string][]string{"a": {"a1", "a2"}, "b": {"b1"}},
		fail:   map[string]error{"c": errors.New("no such container")},
		opened: map[string][]time.Time{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	st := MergeLogs(
		ctx,
		[]LogSource{{ID: "a", Label: "web-1"}, {ID: "b", Label: "db-1"}, {ID: "c", Label: "gone-1"}},
		f.open,
		nil,
	)

	if got := strings.Join(st.Sources, ","); got != "web-1,db-1,gone-1" {
		t.Errorf("Sources = %q, want every member's label, so prefixes align from the first line", got)
	}
	got := render(collect(t, st, 6))
	want := []string{
		"db-1|b1", "db-1|— stopped — (note)",
		"gone-1|logs unavailable: no such container (note)",
		"web-1|a1", "web-1|a2", "web-1|— stopped — (note)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("merged lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	select {
	case l, ok := <-st.Lines:
		if ok {
			t.Fatalf("an extra line before cancel: %+v", l)
		}
		t.Fatal("the stream closed while its context was live")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case _, ok := <-st.Lines:
		if ok {
			t.Fatal("a line after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("the stream did not close after cancel")
	}
}

// TestMergeLogsJoins: a container that starts later joins the stream from
// the moment it started — its history is not replayed — and a start for a
// member that is still streaming is ignored rather than doubling its lines.
func TestMergeLogsJoins(t *testing.T) {
	f := &fakeLogs{
		lines:  map[string][]string{"a": {"a1"}, "n": {"n1"}},
		opened: map[string][]time.Time{},
	}
	block := make(chan LogLine) // "a" stays open until the test ends
	f.mu.Lock()
	f.lines["a"] = nil
	f.mu.Unlock()
	aOpen := make(chan struct{})
	openA := func(ctx context.Context, id string, since time.Time) (*LogStream, error) {
		if id == "a" {
			f.mu.Lock()
			f.opened["a"] = append(f.opened["a"], since)
			if len(f.opened["a"]) == 1 {
				close(aOpen)
			}
			f.mu.Unlock()
			return &LogStream{Lines: block}, nil
		}
		return f.open(ctx, id, since)
	}

	ctx, cancel := context.WithCancel(context.Background())
	starts := make(chan LogSource)
	st := MergeLogs(ctx, []LogSource{{ID: "a", Label: "web-1"}}, openA, starts)
	defer func() {
		// "a" never ends on its own; canceling must still close the merge.
		cancel()
		select {
		case <-st.Err:
		case <-time.After(time.Second):
			t.Error("the merged stream did not shut down with a member still open")
		}
	}()

	// Members open on their own goroutines; wait for "a" to be live before
	// telling the merge it started again.
	select {
	case <-aOpen:
	case <-time.After(time.Second):
		t.Fatal("the initial member was never opened")
	}
	starts <- LogSource{ID: "a", Label: "web-1"} // already streaming
	starts <- LogSource{ID: "n", Label: "worker-1"}
	got := render(collect(t, st, 2))
	if want := "worker-1|n1\nworker-1|— stopped — (note)"; strings.Join(got, "\n") != want {
		t.Errorf("joined lines:\n%s", strings.Join(got, "\n"))
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if n := len(f.opened["a"]); n != 1 {
		t.Errorf("a was opened %d times; a start for a live member must not reopen it", n)
	}
	if s := f.opened["n"]; len(s) != 1 || s[0].IsZero() {
		t.Errorf("the joiner was opened with since %v; want the time it started, not its whole history", s)
	}
	if s := f.opened["a"]; len(s) != 1 || !s[0].IsZero() {
		t.Errorf("an initial member was opened with since %v; want the usual tail", s)
	}
}

func TestProjectLogLabel(t *testing.T) {
	for _, c := range []struct{ project, name, want string }{
		{project: "shop", name: "shop-web-1", want: "web-1"},
		{project: "shop", name: "custom_name", want: "custom_name"},
		{project: "shop", name: "shop-", want: "shop-"},
	} {
		if got := ProjectLogLabel(c.project, c.name); got != c.want {
			t.Errorf("ProjectLogLabel(%q, %q) = %q, want %q", c.project, c.name, got, c.want)
		}
	}
}
