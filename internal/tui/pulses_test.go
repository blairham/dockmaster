// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chart"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakePulses is the daemon behind a test dashboard. Its event streams
// behave as docker.StreamEvents does: the channel closes, and the
// goroutine feeding it exits, when the stream's context is canceled.
type fakePulses struct {
	stats map[string]docker.Stats
	// listGate, when set, holds Containers until it is closed;
	// listEntered is signaled each time Containers is called.
	listGate    chan struct{}
	diskGate    chan struct{} // holds DiskUsage until closed, when set
	listEntered chan struct{}
	feed        chan docker.Event
	containers  []docker.Container
	diskErr     error
	disk        []docker.DiskUsageRow
	canceled    []chan struct{} // per stream: closed when its context is
	gone        []chan struct{} // per stream: closed when its goroutine exits
	mu          sync.Mutex
	lists       int
	samples     int
	disks       int
	streams     int
}

func newFakePulses() *fakePulses {
	return &fakePulses{
		containers: sampleContainers(),
		stats: map[string]docker.Stats{
			"aaaaaaaaaaaa1111": {CPUPerc: 12.5, MemUsage: 300_000_000, MemLimit: 8_000_000_000, CPUs: 4, OK: true},
			"bbbbbbbbbbbb2222": {CPUPerc: 7.5, MemUsage: 200_000_000, MemLimit: 8_000_000_000, CPUs: 4, OK: true},
		},
		disk: []docker.DiskUsageRow{
			{Type: docker.DiskImages, Size: 4_000_000_000, Reclaimable: 1_000_000_000},
			{Type: docker.DiskContainers, Size: 1_000_000_000, Reclaimable: 500_000_000},
		},
		feed:        make(chan docker.Event),
		listEntered: make(chan struct{}, 64),
	}
}

func (f *fakePulses) RequestContext(def time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), def)
}

func (f *fakePulses) Containers(context.Context, bool) ([]docker.Container, error) {
	f.mu.Lock()
	f.lists++
	gate, cs := f.listGate, f.containers
	f.mu.Unlock()
	f.listEntered <- struct{}{}
	if gate != nil {
		<-gate
	}
	return cs, nil
}

func (f *fakePulses) SampleStats(_ context.Context, ids []string) map[string]docker.Stats {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples++
	out := map[string]docker.Stats{}
	for _, id := range ids {
		if s, ok := f.stats[id]; ok {
			out[id] = s
		}
	}
	return out
}

func (f *fakePulses) DiskUsage(context.Context) ([]docker.DiskUsageRow, error) {
	f.mu.Lock()
	f.disks++
	gate, rows, err := f.diskGate, f.disk, f.diskErr
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return rows, err
}

func (f *fakePulses) StreamEvents(ctx context.Context, _ time.Duration) *docker.EventStream {
	f.mu.Lock()
	f.streams++
	canceled, gone := make(chan struct{}), make(chan struct{})
	f.canceled = append(f.canceled, canceled)
	f.gone = append(f.gone, gone)
	f.mu.Unlock()
	out := make(chan docker.Event)
	go func() {
		defer close(gone)
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				close(canceled)
				return
			case e := <-f.feed:
				select {
				case out <- e:
				case <-ctx.Done():
					close(canceled)
					return
				}
			}
		}
	}()
	return &docker.EventStream{Events: out}
}

func (f *fakePulses) count() (lists, samples, disks, streams int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists, f.samples, f.disks, f.streams
}

// pulseRig runs the dashboard's commands as bubbletea would — each on its
// own goroutine, its message fed back to the app — and knows the one that
// never returns on its own: while the view is subscribed, exactly one
// event drain is blocked waiting for the next event.
type pulseRig struct {
	t           *testing.T
	a           *App
	f           *fakePulses
	results     chan tea.Msg
	outstanding int
}

// newPulseRig is an app whose dashboard reads f. The seam is set before the
// app is built, since NewApp builds the view.
func newPulseRig(t *testing.T, opts Options) *pulseRig {
	t.Helper()
	f := newFakePulses()
	old := views.PulseSourceFor
	views.PulseSourceFor = func(*docker.Client) views.PulseSource { return f }
	t.Cleanup(func() { views.PulseSourceFor = old })
	opts.Version = "test"
	a := NewApp(nil, opts)
	a.splashActive, a.loading = false, false
	step(a, tea.WindowSizeMsg{Width: 120, Height: 40})
	r := &pulseRig{t: t, a: a, f: f, results: make(chan tea.Msg, 64)}
	t.Cleanup(r.leave)
	return r
}

func (r *pulseRig) view() *views.PulsesView {
	return typedView[*views.PulsesView](r.a, style.ViewPulses)
}

func (r *pulseRig) start(c tea.Cmd) {
	if c == nil {
		return
	}
	r.outstanding++
	go func() { r.results <- c() }()
}

func (r *pulseRig) deliver(m tea.Msg) {
	switch m := m.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			r.start(c)
		}
	default:
		r.start(step(r.a, m))
	}
}

// run runs cmd and everything it leads to until nothing is outstanding
// but running event drains, which return only when an event arrives or
// the stream ends.
func (r *pulseRig) run(cmd tea.Cmd) {
	r.t.Helper()
	r.start(cmd)
	deadline := time.After(5 * time.Second)
	for r.outstanding > r.view().Drains() {
		select {
		case m := <-r.results:
			r.outstanding--
			r.deliver(m)
		case <-time.After(time.Millisecond):
			// A drain just started may not have counted itself yet.
		case <-deadline:
			r.t.Fatalf("%d commands still outstanding, %d of them drains", r.outstanding, r.view().Drains())
		}
	}
}

// pumpUntil delivers results until ch fires, reporting whether it did
// within d.
func (r *pulseRig) pumpUntil(ch <-chan struct{}, d time.Duration) bool {
	timeout := time.After(d)
	for {
		select {
		case <-ch:
			return true
		case m := <-r.results:
			r.outstanding--
			r.deliver(m)
		case <-timeout:
			return false
		}
	}
}

// open opens :pulses and runs what that starts.
func (r *pulseRig) open() {
	r.t.Helper()
	msg, cmd := r.a.dispatchCommand("pulses")
	if msg != "" || r.a.view != style.ViewPulses {
		r.t.Fatalf(":pulses opened %v (%q)", r.a.view, msg)
	}
	r.run(cmd)
}

// poll is one tick of the app's poll.
func (r *pulseRig) poll() {
	r.t.Helper()
	r.run(r.a.refreshPolledView())
}

// sendEvents feeds n daemon events and waits for the drain to count them.
func (r *pulseRig) sendEvents(n int) {
	r.t.Helper()
	for range n {
		select {
		case r.f.feed <- docker.Event{Type: "container", Action: "start"}:
		case <-time.After(5 * time.Second):
			r.t.Fatal("no stream took the event")
		}
	}
	select {
	case m := <-r.results:
		r.outstanding--
		r.deliver(m)
	case <-time.After(5 * time.Second):
		r.t.Fatal("the drain never reported")
	}
	r.run(nil)
}

// leave switches to containers, and waits for every command to finish.
func (r *pulseRig) leave() {
	if r.a.view == style.ViewPulses {
		r.a.switchView(style.ViewContainers)
	}
	r.run(nil)
}

// pulseFrame is the rendered frame with the dashboard's rows.
func pulseFrame(t *testing.T, a *App, want ...string) string {
	t.Helper()
	out := render(a)
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("dashboard lacks %q:\n%s", w, out)
		}
	}
	return out
}

// TestPulsesDashboard: :pulses shows running/total, health, CPU and memory
// summed over the running containers against the daemon's CPUs and memory,
// reclaimable disk, and the events of the last interval (#12).
func TestPulsesDashboard(t *testing.T) {
	r := newPulseRig(t, Options{})
	r.open()
	pulseFrame(
		t, r.a,
		"pulses(all)[4]",
		"Containers", "2/4 running",
		"Health", "1/1 healthy",
		"Reclaimable", "1.50GB of 5.00GB",
		"CPU", "20.0% of 4 CPUs",
		"Memory", "500MB of 8.00GB",
		"Events", "counting every 3s",
	)

	r.sendEvents(3)
	r.poll()
	pulseFrame(t, r.a, "3 in the last 3s")
	r.poll()
	pulseFrame(t, r.a, "0 in the last 3s")

	// The open's sample and two polls: three of each, one subscription.
	if lists, samples, _, streams := r.f.count(); lists != 3 || samples != 3 || streams != 1 {
		t.Errorf("lists %d, samples %d, streams %d; want 3, 3, 1", lists, samples, streams)
	}
}

// TestPulsesHealth: any unhealthy container makes the health gauge
// critical, a check still starting only warns; half the disk reclaimable
// warns.
func TestPulsesHealth(t *testing.T) {
	r := newPulseRig(t, Options{})
	r.open()
	if l := r.view().HealthLevel(); l != chart.LevelOK {
		t.Errorf("all healthy: level %v", l)
	}
	if l := r.view().DiskLevel(); l != chart.LevelOK {
		t.Errorf("30%% reclaimable: level %v", l)
	}

	r.f.mu.Lock()
	r.f.containers = append(
		sampleContainers(),
		docker.Container{ID: "e1", Name: "db", State: "running", Health: "unhealthy"},
		docker.Container{ID: "e2", Name: "cache", State: "running", Health: "healthy"},
	)
	r.f.disk = []docker.DiskUsageRow{{Type: docker.DiskImages, Size: 4_000_000_000, Reclaimable: 3_000_000_000}}
	r.f.mu.Unlock()
	r.run(r.a.refreshActiveView()) // r: the list and disk now
	pulseFrame(t, r.a, "4/6 running", "1 unhealthy, 2/3 healthy", "3.00GB of 4.00GB")
	if l := r.view().HealthLevel(); l != chart.LevelCritical {
		t.Errorf("one unhealthy of three: level %v, want critical", l)
	}
	if l := r.view().DiskLevel(); l != chart.LevelWarn {
		t.Errorf("75%% reclaimable: level %v, want warn", l)
	}

	r.f.mu.Lock()
	r.f.containers = append(
		sampleContainers(),
		docker.Container{ID: "e1", Name: "db", State: "running", Health: "starting"},
	)
	r.f.mu.Unlock()
	r.poll()
	pulseFrame(t, r.a, "1/2 healthy, 1 starting")
	if l := r.view().HealthLevel(); l != chart.LevelWarn {
		t.Errorf("one starting: level %v, want warn", l)
	}

	r.f.mu.Lock()
	r.f.containers = []docker.Container{{ID: "x", Name: "plain", State: "exited"}}
	r.f.mu.Unlock()
	r.poll()
	pulseFrame(t, r.a, "0/1 running", "no healthchecks")
	if l := r.view().HealthLevel(); l != chart.LevelOK {
		t.Errorf("no healthchecks: level %v", l)
	}
}

// TestPulsesNoStats: with the CPU/MEM poll off (--no-stats), the CPU and
// memory panels say so and the stats endpoint is never called.
func TestPulsesNoStats(t *testing.T) {
	r := newPulseRig(t, Options{NoStats: true})
	r.open()
	r.poll()
	r.poll()
	out := pulseFrame(t, r.a, "pulses stats off", "CPU/MEM poll is off", "2/4 running")
	if strings.Contains(out, "of 4 CPUs") || strings.Contains(out, "sampling") {
		t.Errorf("stats off still draws a CPU sample:\n%s", out)
	}
	if _, samples, _, _ := r.f.count(); samples != 0 {
		t.Errorf("stats off sampled %d times", samples)
	}

	// A narrow panel cuts the note with a mark, not mid-word.
	step(r.a, tea.WindowSizeMsg{Width: 80, Height: 24})
	if out := render(r.a); !strings.Contains(out, "poll is off (t in contain…") {
		t.Errorf("at 80 columns the note is not cut with a mark:\n%s", out)
	}
	step(r.a, tea.WindowSizeMsg{Width: 120, Height: 40})

	// t in the containers view turns it on; the dashboard follows.
	r.a.switchView(style.ViewContainers)
	r.run(nil)
	step(r.a, key("t"))
	r.open()
	pulseFrame(t, r.a, "20.0% of 4 CPUs")
}

// TestPulsesDiskCadence: disk usage is read on open, then once a minute of
// polls, not on every tick.
func TestPulsesDiskCadence(t *testing.T) {
	r := newPulseRig(t, Options{})
	r.open()
	every := int(time.Minute / pollInterval) // 20 at 3s
	for range every - 1 {
		r.poll()
	}
	if lists, _, disks, _ := r.f.count(); disks != 1 || lists != every {
		t.Fatalf("after %d polls: %d disk reads, %d lists; want 1 and %d", every-1, disks, lists, every)
	}
	r.poll()
	if _, _, disks, _ := r.f.count(); disks != 2 {
		t.Errorf("a minute of polls: %d disk reads, want 2", disks)
	}
}

// TestPulsesSingleFlight: a poll while the last listing (or disk read) is
// outstanding starts nothing.
func TestPulsesSingleFlight(t *testing.T) {
	r := newPulseRig(t, Options{})
	gate := make(chan struct{})
	r.f.mu.Lock()
	r.f.listGate, r.f.diskGate = gate, gate
	r.f.mu.Unlock()

	msg, cmd := r.a.dispatchCommand("pulses")
	if msg != "" {
		t.Fatal(msg)
	}
	r.start(cmd)
	if !r.pumpUntil(r.f.listEntered, 5*time.Second) {
		t.Fatal("opening never listed")
	}
	for range int(time.Minute/pollInterval) + 5 { // past when a disk read would be due
		r.start(r.a.refreshPolledView())
	}
	r.start(r.a.refreshActiveView()) // and r, which reads disk too
	if r.pumpUntil(r.f.listEntered, 100*time.Millisecond) {
		t.Fatal("a poll listed while a listing was outstanding")
	}
	if lists, _, disks, _ := r.f.count(); lists != 1 || disks != 1 {
		t.Errorf("%d lists and %d disk reads while one was outstanding; want 1 and 1", lists, disks)
	}
	close(gate)
	r.run(nil)
	r.poll()
	if lists, _, _, _ := r.f.count(); lists != 2 {
		t.Errorf("the next poll after the listing landed: %d lists, want 2", lists)
	}
}

// TestPulsesStopsWhenLeft: leaving the dashboard cancels its event
// subscription — the stream's goroutine exits and its drain returns — and
// opening it again subscribes afresh with the history reset.
func TestPulsesStopsWhenLeft(t *testing.T) {
	r := newPulseRig(t, Options{})
	r.open()
	r.sendEvents(2)
	r.sendEvents(1) // a second batch: the drain re-arms after each
	r.poll()
	pulseFrame(t, r.a, "3 in the last 3s")
	r.poll()
	if _, _, _, streams := r.f.count(); streams != 1 {
		t.Fatalf("polling subscribed %d times", streams)
	}

	r.a.switchView(style.ViewContainers)
	for i, ch := range []chan struct{}{r.f.canceled[0], r.f.gone[0]} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("leaving left the stream running (check %d)", i)
		}
	}
	r.run(nil) // the drain returns once the stream closes
	if r.outstanding != 0 || r.view().Subscribed() {
		t.Fatalf("after leaving: %d commands outstanding, subscribed %v", r.outstanding, r.view().Subscribed())
	}
	// A poll on another view does not touch the dashboard.
	r.run(r.a.refreshPolledView())
	if r.view().Subscribed() {
		t.Fatal("a poll elsewhere resubscribed the dashboard")
	}

	r.open()
	if _, _, _, streams := r.f.count(); streams != 2 {
		t.Errorf("reopening: %d subscriptions, want 2", streams)
	}
	pulseFrame(t, r.a, "counting every 3s")
}

// TestPulsesFitsNarrowTerminals: at every size the dashboard draws exactly
// its box, every line within the width, with or without errors to show.
func TestPulsesFitsNarrowTerminals(t *testing.T) {
	r := newPulseRig(t, Options{})
	r.open()
	pv := r.view()
	r.f.mu.Lock()
	r.f.diskErr = errors.New("context deadline exceeded while the daemon sized every volume")
	r.f.mu.Unlock()
	r.run(r.a.refreshActiveView())
	pulseFrame(t, r.a, "disk usage: context deadline exceeded")
	for _, sz := range []struct{ w, h int }{
		{w: 200, h: 60}, {w: 120, h: 40}, {w: 80, h: 24}, {w: 60, h: 18}, {w: 40, h: 12}, {w: 20, h: 8}, {w: 3, h: 2},
	} {
		step(r.a, tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
		out := render(r.a)
		for i, l := range strings.Split(out, "\n") {
			if w := xansi.StringWidth(l); w > sz.w {
				t.Errorf("%dx%d: frame line %d is %d wide:\n%s", sz.w, sz.h, i, w, l)
			}
		}
		for _, cw := range []int{100, 39, 25, 10, 1} {
			for _, ch := range []int{30, 12, 6, 3, 1} {
				pv.Resize(cw, ch)
				lines := strings.Split(xansi.Strip(pv.View()), "\n")
				if len(lines) > ch {
					t.Errorf("%dx%d: %d lines", cw, ch, len(lines))
				}
				for i, l := range lines {
					if w := xansi.StringWidth(l); w != cw {
						t.Errorf("%dx%d: line %d is %d wide: %q", cw, ch, i, w, l)
					}
				}
			}
		}
	}
	step(r.a, tea.WindowSizeMsg{Width: 80, Height: 24})
	pulseFrame(t, r.a, "Containers", "CPU", "Memory", "Events")
}

// TestPulsesUnderADrillIn: a view opened over the dashboard (:xray pushes)
// does not take its messages — the events counted meanwhile are still the
// dashboard's when it is back, and its drain is still running.
func TestPulsesUnderADrillIn(t *testing.T) {
	r := newPulseRig(t, Options{})
	r.open()
	if _, cmd := r.a.dispatchCommand("xray"); r.a.view != style.ViewXray {
		t.Fatalf(":xray opened %v", r.a.view)
	} else {
		r.run(cmd)
	}
	r.sendEvents(2)
	step(r.a, key("esc"))
	if r.a.view != style.ViewPulses {
		t.Fatalf("esc went to %v", r.a.view)
	}
	r.sendEvents(1)
	r.poll()
	pulseFrame(t, r.a, "3 in the last 3s")
}
