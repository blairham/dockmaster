// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/blairham/tuikit/chart"
	"github.com/charmbracelet/x/ansi"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

const (
	// pulseHistory is how far back the sparklines remember, at the poll
	// rate; the series is sized from it and the interval.
	pulseHistory = 10 * time.Minute
	// pulseMinSamples keeps a fast poll from leaving a wide terminal's
	// sparkline half empty for want of capacity.
	pulseMinSamples = 120
	// pulseDiskEvery is how often disk usage is read again. It is
	// `docker system df`, which walks every volume (daemon-latency.md), so
	// it is read on open and then once a minute, never on every tick.
	pulseDiskEvery = time.Minute
	// pulseEventWindow is how long a drain keeps collecting once the first
	// event of a burst arrives. A quiet daemon costs no wakeups at all: the
	// drain blocks until there is something to count.
	pulseEventWindow = 250 * time.Millisecond
	// pulseListTimeout matches the containers view's `docker ps`.
	pulseListTimeout = 20 * time.Second
)

// PulseSource is what the dashboard reads from the daemon: *docker.Client,
// or a fake in tests.
type PulseSource interface {
	RequestContext(def time.Duration) (context.Context, context.CancelFunc)
	Containers(ctx context.Context, all bool) ([]docker.Container, error)
	SampleStats(ctx context.Context, ids []string) map[string]docker.Stats
	DiskUsage(ctx context.Context) ([]docker.DiskUsageRow, error)
	StreamEvents(ctx context.Context, since time.Duration) *docker.EventStream
}

// PulseSourceFor is where a dashboard gets its daemon; a var so tests can
// hand it a fake. A nil client is no source: the view draws, empty.
var PulseSourceFor = func(c *docker.Client) PulseSource {
	if c == nil {
		return nil
	}
	return c
}

// PulsesListMsg carries the dashboard's `docker ps -a`.
type PulsesListMsg struct {
	Err        error
	from       *PulsesView
	Containers []docker.Container
}

// PulsesStatsMsg carries one CPU/MEM sample of the running containers.
type PulsesStatsMsg struct {
	from  *PulsesView
	Stats map[string]docker.Stats
}

// PulsesDiskMsg carries a disk-usage read.
type PulsesDiskMsg struct {
	Err  error
	from *PulsesView
	Rows []docker.DiskUsageRow
}

// PulsesEventsMsg carries how many daemon events a drain counted, or that
// the stream ended.
type PulsesEventsMsg struct {
	Err    error
	from   *PulsesView
	Gen    int
	Count  int
	Closed bool
}

// PulsesView is k9s's pulses for Docker (#12): containers running, their
// health and reclaimable disk as gauges, and total CPU, memory and the
// daemon's event rate as sparklines over the last minutes.
//
// Everything it reads is single-flight: one `docker ps -a` and its stats
// sample at a time, one disk-usage read at a time and only once a minute,
// and one event subscription while the view is open, canceled when it is
// left (Stop). The history starts over each time the view is opened.
type PulsesView struct {
	diskErr         error
	src             PulseSource
	evErr           error
	listErr         error
	healthGauge     *chart.Gauge
	memLine         *chart.Sparkline
	cpu             *chart.Series
	mem             *chart.Series
	events          *chart.Series
	containersGauge *chart.Gauge
	// drains counts event drains running, on their own goroutines: one
	// while subscribed, none once the view is left and its drain returned.
	drains       *atomic.Int32
	diskGauge    *chart.Gauge
	cpuLine      *chart.Sparkline
	stream       *docker.EventStream
	eventLine    *chart.Sparkline
	cpuOff       *pulseNote
	memOff       *pulseNote
	gauges       *chart.Grid
	usage        *chart.Grid
	rate         *chart.Grid
	cancel       context.CancelFunc
	cpuNow       float64
	pending      int // events counted since the last sample
	memNow       int64
	memTotal     int64
	diskSize     int64
	diskFree     int64
	running      int
	total        int
	healthy      int
	unhealthy    int
	checks       int
	starting     int
	cpus         int
	interval     time.Duration
	ticks        int // polls since the last disk read
	gen          int
	width        int
	height       int
	open         bool
	statsOn      bool
	listInFlight bool
	diskInFlight bool
	loading      bool
	sampled      bool
	diskSeen     bool
}

// NewPulsesView builds the dashboard. statsOn is the CPU/MEM poll (off
// under --no-stats); interval is the app's poll, which sizes the history
// and spaces the disk reads.
func NewPulsesView(client *docker.Client, statsOn bool, interval time.Duration) *PulsesView {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	n := max(int(pulseHistory/interval), pulseMinSamples)
	v := &PulsesView{
		src:      PulseSourceFor(client),
		statsOn:  statsOn,
		interval: interval,
		cpu:      chart.NewSeries(n),
		mem:      chart.NewSeries(n),
		events:   chart.NewSeries(n),
		drains:   new(atomic.Int32),
		loading:  true,
	}
	v.build()
	return v
}

// build makes the widgets in the current theme. Resize calls it, so a skin
// reload (which resizes the active view) repaints them.
func (v *PulsesView) build() {
	t := Theme()
	v.containersGauge = chart.NewGauge(t)
	v.containersGauge.SetTitle("Containers")
	v.healthGauge = chart.NewGauge(t)
	v.healthGauge.SetTitle("Health")
	v.diskGauge = chart.NewGauge(t)
	v.diskGauge.SetTitle("Reclaimable")
	// Half the disk Docker holds being reclaimable is worth a prune; it is
	// never an error, so there is no critical level.
	v.diskGauge.SetHighThresholds(0.5, math.NaN())

	v.cpuLine = chart.NewSparkline(t, v.cpu)
	v.cpuLine.SetTitle("CPU")
	v.memLine = chart.NewSparkline(t, v.mem)
	v.memLine.SetTitle("Memory")
	v.eventLine = chart.NewSparkline(t, v.events)
	v.eventLine.SetTitle("Events")
	v.cpuOff = &pulseNote{title: "CPU", note: statsOffNote}
	v.memOff = &pulseNote{title: "Memory", note: statsOffNote}

	v.gauges = chart.NewGrid(t, 3, v.containersGauge, v.healthGauge, v.diskGauge)
	v.rate = chart.NewGrid(t, 1, v.eventLine)
	v.layoutUsage()
}

// layoutUsage puts the CPU and memory sparklines, or the notes saying the
// poll is off, in the middle row.
func (v *PulsesView) layoutUsage() {
	if v.statsOn {
		v.usage = chart.NewGrid(Theme(), 2, v.cpuLine, v.memLine)
	} else {
		v.usage = chart.NewGrid(Theme(), 2, v.cpuOff, v.memOff)
	}
}

const statsOffNote = "the CPU/MEM poll is off (t in containers, --no-stats)"

// SetStatsEnabled follows the app's CPU/MEM poll: with it off, the CPU and
// memory panels say so instead of sampling.
func (v *PulsesView) SetStatsEnabled(on bool) {
	if v.statsOn == on {
		return
	}
	v.statsOn = on
	v.layoutUsage()
}

// SetInterval follows a change to the app's poll rate.
func (v *PulsesView) SetInterval(d time.Duration) {
	if d > 0 {
		v.interval = d
	}
}

// Title names the view in the border.
func (v *PulsesView) Title() string { return "pulses" }

// Status names the CPU/MEM poll being off in the border title.
func (v *PulsesView) Status() string {
	if !v.statsOn {
		return "stats off"
	}
	return ""
}

// Init opens the dashboard.
func (v *PulsesView) Init() tea.Cmd { return v.Refresh() }

// Refresh is opening the view, or r: a closed view starts over — fresh
// history and an event subscription — and either way the list and disk
// usage are read now, each only if it is not already outstanding.
func (v *PulsesView) Refresh() tea.Cmd {
	var cmds []tea.Cmd
	if !v.open {
		v.open = true
		v.cpu.Reset()
		v.mem.Reset()
		v.events.Reset()
		v.pending, v.ticks = 0, 0
		cmds = append(cmds, v.startEvents())
	}
	return tea.Batch(append(cmds, v.refreshList(), v.refreshDisk())...)
}

// Poll is the app's tick: it closes one interval of the event count,
// re-lists the containers (and samples their stats), reads disk usage once
// every pulseDiskEvery, and re-subscribes to events if the stream ended.
func (v *PulsesView) Poll() tea.Cmd {
	if !v.open {
		return v.Refresh()
	}
	v.events.Push(float64(v.pending))
	v.pending = 0
	cmds := []tea.Cmd{v.refreshList()}
	if v.stream == nil {
		cmds = append(cmds, v.startEvents())
	}
	v.ticks++
	if v.ticks >= v.diskEvery() && !v.diskInFlight {
		v.ticks = 0
		cmds = append(cmds, v.refreshDisk())
	}
	return tea.Batch(cmds...)
}

// diskEvery is the polls between disk reads.
func (v *PulsesView) diskEvery() int {
	return max(int((pulseDiskEvery+v.interval-1)/v.interval), 1)
}

// Stop cancels the event subscription. Safe to call repeatedly; the next
// Refresh opens the view afresh.
func (v *PulsesView) Stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	v.stream = nil
	v.open = false
}

// refreshList is `docker ps -a`, then, when the poll is on, one stats
// sample of the running containers through the shared sampler. The two are
// one flight: no list starts while the last one's stats are outstanding.
func (v *PulsesView) refreshList() tea.Cmd {
	if v.listInFlight || v.src == nil {
		return nil
	}
	v.listInFlight = true
	src, from := v.src, v
	return func() tea.Msg {
		ctx, cancel := src.RequestContext(pulseListTimeout)
		defer cancel()
		cs, err := src.Containers(ctx, true)
		return PulsesListMsg{from: from, Containers: cs, Err: err}
	}
}

func (v *PulsesView) sampleStats(cs []docker.Container) tea.Cmd {
	ids := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.Running() {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	src, from := v.src, v
	return func() tea.Msg {
		ctx, cancel := src.RequestContext(pulseListTimeout)
		defer cancel()
		return PulsesStatsMsg{from: from, Stats: src.SampleStats(ctx, ids)}
	}
}

// refreshDisk is `docker system df`, single-flight.
func (v *PulsesView) refreshDisk() tea.Cmd {
	if v.diskInFlight || v.src == nil {
		return nil
	}
	v.diskInFlight = true
	src, from := v.src, v
	return func() tea.Msg {
		ctx, cancel := src.RequestContext(diskUsageTimeout)
		defer cancel()
		rows, err := src.DiskUsage(ctx)
		return PulsesDiskMsg{from: from, Rows: rows, Err: err}
	}
}

// startEvents subscribes to the daemon's events from now on, canceling
// any earlier subscription.
func (v *PulsesView) startEvents() tea.Cmd {
	if v.src == nil {
		return nil
	}
	if v.cancel != nil {
		v.cancel()
	}
	v.gen++
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	v.stream = v.src.StreamEvents(ctx, 0)
	return v.drain()
}

// drain counts events: it blocks until one arrives (or the stream ends),
// then keeps counting for pulseEventWindow so a burst is one message.
func (v *PulsesView) drain() tea.Cmd {
	st, gen, from, drains := v.stream, v.gen, v, v.drains
	if st == nil {
		return nil
	}
	return func() tea.Msg {
		drains.Add(1)
		defer drains.Add(-1)
		n, closed, err := countBurst(st.Events, st.Err)
		return PulsesEventsMsg{from: from, Gen: gen, Count: n, Closed: closed, Err: err}
	}
}

// countBurst counts events until pulseEventWindow after the first, or until
// the stream ends (closed) or fails.
func countBurst[T any](events <-chan T, errs <-chan error) (n int, closed bool, err error) {
	var timer *time.Timer
	var window <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return n, true, nil
			}
			n++
			if timer == nil {
				timer = time.NewTimer(pulseEventWindow)
				window = timer.C
			}
		case e, ok := <-errs:
			switch {
			case !ok:
				errs = nil // closed: only the events channel is left to end it
			case e != nil:
				return n, true, e
			}
		case <-window:
			return n, false, nil
		}
	}
}

// Update folds the dashboard's messages in. A message from another
// instance — one built for a context since switched away from — is
// dropped, and so is an event count from a stream since restarted.
func (v *PulsesView) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case PulsesListMsg:
		if m.from == v {
			return v.onList(m)
		}
	case PulsesStatsMsg:
		if m.from == v {
			v.listInFlight = false
			if v.statsOn {
				v.pushUsage(m.Stats)
			}
		}
	case PulsesDiskMsg:
		if m.from == v {
			v.onDisk(m)
		}
	case PulsesEventsMsg:
		if m.from == v && m.Gen == v.gen {
			return v.onEvents(m)
		}
	}
	return nil
}

// onList folds a listing in and samples the running containers' stats.
func (v *PulsesView) onList(m PulsesListMsg) tea.Cmd {
	v.loading = false
	v.listErr = m.Err
	if m.Err != nil {
		v.listInFlight = false
		return nil
	}
	v.count(m.Containers)
	if !v.statsOn {
		v.listInFlight = false
		return nil
	}
	cmd := v.sampleStats(m.Containers)
	if cmd == nil {
		// Nothing running: nothing to sample, and nothing in use.
		v.listInFlight = false
		v.pushUsage(nil)
	}
	return cmd
}

// onDisk folds the disk usage in.
func (v *PulsesView) onDisk(m PulsesDiskMsg) {
	v.diskInFlight = false
	v.diskErr = m.Err
	if m.Err != nil {
		return
	}
	v.diskSeen = true
	v.diskSize, v.diskFree = 0, 0
	for _, r := range m.Rows {
		v.diskSize += r.Size
		v.diskFree += r.Reclaimable
	}
}

// onEvents adds an event count, re-arming the drain while the stream is
// open.
func (v *PulsesView) onEvents(m PulsesEventsMsg) tea.Cmd {
	v.pending += m.Count
	if !m.Closed {
		return v.drain()
	}
	if m.Err != nil && !strings.Contains(m.Err.Error(), "context canceled") {
		v.evErr = m.Err
	}
	// The next poll subscribes again.
	v.stream = nil
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	return nil
}

// count tallies the listing for the gauges.
func (v *PulsesView) count(cs []docker.Container) {
	v.total, v.running = len(cs), 0
	v.healthy, v.unhealthy, v.starting, v.checks = 0, 0, 0, 0
	for _, c := range cs {
		if c.Running() {
			v.running++
		}
		switch c.Health {
		case "healthy":
			v.healthy++
		case "unhealthy":
			v.unhealthy++
		case "starting":
			v.starting++
		default:
			continue
		}
		v.checks++
	}
}

// pushUsage adds one CPU and memory sample: the sums over the running
// containers. CPU is docker stats' CPU %, summed, so 100 is one CPU; the
// sparkline's full height is every CPU the daemon has. Memory's full
// height is the largest limit seen, which for an unlimited container is
// the daemon host's memory.
func (v *PulsesView) pushUsage(stats map[string]docker.Stats) {
	v.cpuNow, v.memNow = 0, 0
	for _, s := range stats {
		if !s.OK {
			continue
		}
		v.cpuNow += s.CPUPerc
		v.memNow += s.MemUsage
		v.cpus = max(v.cpus, s.CPUs)
		v.memTotal = max(v.memTotal, s.MemLimit)
	}
	v.sampled = true
	v.cpu.Push(v.cpuNow)
	v.mem.Push(float64(v.memNow))
}

// UpdateTable has nothing to scroll.
func (v *PulsesView) UpdateTable(tea.Msg) tea.Cmd { return nil }

// Resize rebuilds the widgets at the new size, in the current theme.
func (v *PulsesView) Resize(width, height int) {
	v.width, v.height = width, height
	v.build()
}

// HandleKey binds nothing: r (refresh) and the view keys are the app's.
func (v *PulsesView) HandleKey(string) (string, string) { return "", "" }

// SetFilter does nothing: the dashboard is not a list.
func (v *PulsesView) SetFilter(string) {}

// Count is the containers on the daemon, for the border title.
func (v *PulsesView) Count() int { return v.total }

// Loading reports whether the first listing is outstanding.
func (v *PulsesView) Loading() bool { return v.loading && v.src != nil }

// Subscribed reports whether the view holds an event subscription: from
// opening it until it is left, or until the stream ends and the next poll
// subscribes again.
func (v *PulsesView) Subscribed() bool { return v.stream != nil }

// Drains is how many event drains are running: 1 while the view is
// subscribed, and 0 once it is left and the last drain has returned —
// what a test checks to know nothing was left behind.
func (v *PulsesView) Drains() int { return int(v.drains.Load()) }

// HealthLevel is the health gauge's state, for tests.
func (v *PulsesView) HealthLevel() chart.Level {
	v.setGauges()
	return v.healthGauge.Level()
}

// DiskLevel is the disk gauge's state, for tests.
func (v *PulsesView) DiskLevel() chart.Level {
	v.setGauges()
	return v.diskGauge.Level()
}

// setGauges puts the current numbers into the widgets.
func (v *PulsesView) setGauges() {
	v.containersGauge.Set(float64(v.running), float64(v.total))
	v.containersGauge.SetLabel(fmt.Sprintf("%d/%d running", v.running, v.total))

	v.healthGauge.Set(float64(v.healthy), float64(v.checks))
	switch {
	case v.checks == 0:
		v.healthGauge.ClearThresholds()
		v.healthGauge.SetLabel("no healthchecks")
	case v.unhealthy > 0:
		// Any unhealthy container is critical, however many are fine: a
		// ratio below 2 always holds.
		v.healthGauge.SetLowThresholds(math.NaN(), 2)
		v.healthGauge.SetLabel(fmt.Sprintf("%d unhealthy, %d/%d healthy", v.unhealthy, v.healthy, v.checks))
	case v.starting > 0:
		v.healthGauge.SetLowThresholds(1, math.NaN())
		v.healthGauge.SetLabel(fmt.Sprintf("%d/%d healthy, %d starting", v.healthy, v.checks, v.starting))
	default:
		v.healthGauge.ClearThresholds()
		v.healthGauge.SetLabel(fmt.Sprintf("%d/%d healthy", v.healthy, v.checks))
	}

	if v.diskSeen {
		v.diskGauge.Set(float64(v.diskFree), float64(v.diskSize))
		v.diskGauge.SetLabel(docker.HumanSize(v.diskFree) + " of " + docker.HumanSize(v.diskSize))
	} else {
		v.diskGauge.Set(0, 0)
		v.diskGauge.SetLabel("reading…")
	}

	if v.sampled {
		v.cpuLine.SetLabel(fmt.Sprintf("%.1f%% of %d CPUs", v.cpuNow, max(v.cpus, 1)))
		v.memLine.SetLabel(docker.HumanSize(v.memNow) + " of " + docker.HumanSize(v.memTotal))
	} else {
		v.cpuLine.SetLabel("sampling…")
		v.memLine.SetLabel("sampling…")
	}
	v.cpuLine.SetMax(float64(max(v.cpus, 1) * 100))
	v.memLine.SetMax(float64(v.memTotal))
	if v.events.Len() > 0 {
		v.eventLine.SetLabel(fmt.Sprintf("%.0f in the last %s", v.events.Last(), v.interval))
	} else {
		v.eventLine.SetLabel(fmt.Sprintf("counting every %s", v.interval))
	}
}

// View draws the dashboard: the three gauges across the top, CPU and
// memory side by side under them, and the event rate across the bottom —
// exactly the view's size, every line its width.
func (v *PulsesView) View() string {
	w, h := v.width, v.height
	if w <= 0 || h <= 0 {
		return ""
	}
	v.setGauges()

	var notes []string
	for _, e := range []struct {
		err  error
		what string
	}{{v.listErr, "containers"}, {v.diskErr, "disk usage"}, {v.evErr, "events"}} {
		if e.err != nil {
			notes = append(notes, e.what+": "+docker.FormatUserError(e.err).Error())
		}
	}
	noteLines := min(len(notes), max(h-3, 0))

	avail := h - noteLines
	gaugeH := 3
	if avail < 12 {
		gaugeH = 2
	}
	gaugeH = min(gaugeH, avail)
	rest := avail - gaugeH
	sep := 0
	if rest >= 6 {
		sep = 1
	}
	rest -= sep
	rateH := rest / 3
	usageH := rest - rateH

	var parts []string
	add := func(g *chart.Grid, height int) {
		if height <= 0 {
			return
		}
		g.Resize(w, height)
		parts = append(parts, g.View())
	}
	add(v.gauges, gaugeH)
	if sep > 0 {
		parts = append(parts, strings.Repeat(" ", w))
	}
	add(v.usage, usageH)
	add(v.rate, rateH)
	for _, n := range notes[:noteLines] {
		parts = append(parts, style.Error.Render(fitText(n, w)))
	}
	return strings.Join(parts, "\n")
}

// fitText is s cut or padded to exactly w cells.
func fitText(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

// pulseNote is a panel with a title and a line of text instead of a chart:
// what CPU and memory show while the stats poll is off.
type pulseNote struct {
	title, note   string
	width, height int
}

func (n *pulseNote) Resize(width, height int) { n.width, n.height = width, height }

func (n *pulseNote) View() string {
	if n.width <= 0 || n.height <= 0 {
		return ""
	}
	// The grid pads and cuts each line to the cell; the note is cut here
	// so a narrow panel ends it in "…" rather than mid-word.
	lines := []string{Theme().Title.Render(n.title)}
	if n.height > 1 {
		lines = append(lines, style.Muted.Render(fitText(n.note, n.width)))
	}
	return strings.Join(lines, "\n")
}
