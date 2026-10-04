// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/scan"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// ScanResultMsg carries a finished scan, for the scan that asked.
type ScanResultMsg struct {
	Err     error
	Ref     string
	Scanner string
	Vulns   []scan.Vuln
	Gen     int
}

// RunScan finds a scanner and scans ref on the daemon at host; a var so
// tests can feed the view without one.
var RunScan = func(ctx context.Context, ref, host string) (string, []scan.Vuln, error) {
	s, err := scan.Detect()
	if err != nil {
		return "", nil, err
	}
	vs, err := scan.Run(ctx, s, ref, host)
	return s.Name, vs, err
}

// ScanView shows an image's vulnerabilities, worst first (#11). The scan is
// another program — grype or trivy — and can take minutes, so the view says
// what it is doing while it waits, and leaving it stops the scanner.
type ScanView struct {
	tableSort
	started time.Time
	cancel  context.CancelFunc
	err     error
	ref     string
	host    string
	scanner string
	filter  string
	all     []scan.Vuln
	visible []scan.Vuln
	table   table.Model
	gen     int
	running bool
}

// NewScanView scans image ref on the daemon at host.
func NewScanView(ref, host string) *ScanView {
	t := table.New(
		table.WithColumns(scanColumns()),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
		table.WithKeyMap(tableKeyMap()),
	)
	return &ScanView{tableSort: tableSort{layoutKey: "scan"}, ref: ref, host: host, table: t}
}

func scanColumns() []table.Column {
	return []table.Column{
		{Title: "SEVERITY", Width: 10},
		{Title: "ID", Width: 22},
		{Title: "PACKAGE", Width: 24},
		{Title: "INSTALLED", Width: 16},
		{Title: "FIXED IN", Width: 20},
		{Title: "TITLE", Width: 60},
	}
}

// Title is the scanned image, for the border.
func (v *ScanView) Title() string { return "scan " + v.ref }

// Init starts the scan.
func (v *ScanView) Init() tea.Cmd { return v.Refresh() }

// Refresh starts the scan over, stopping any that is still running.
func (v *ScanView) Refresh() tea.Cmd {
	v.Stop()
	v.gen++
	v.running, v.err, v.started = true, nil, time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	gen, ref, host := v.gen, v.ref, v.host
	return func() tea.Msg {
		name, vs, err := RunScan(ctx, ref, host)
		return ScanResultMsg{Gen: gen, Ref: ref, Scanner: name, Vulns: vs, Err: err}
	}
}

// Stop kills a running scan: leaving the view must not leave the scanner
// running behind it.
func (v *ScanView) Stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	v.running = false
}

// Update folds a finished scan in; a stale one, from a scan since
// restarted, is dropped.
func (v *ScanView) Update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(ScanResultMsg)
	if !ok || m.Gen != v.gen {
		return nil
	}
	v.running, v.cancel = false, nil
	v.err, v.scanner, v.all = m.Err, m.Scanner, m.Vulns
	v.rebuildRows()
	return nil
}

// Status is the count by severity, for the title: "3 critical 23 high".
func (v *ScanView) Status() string {
	if v.running || v.err != nil {
		return ""
	}
	c := scan.Counts(v.all)
	var parts []string
	for _, s := range []scan.Severity{scan.Critical, scan.High, scan.Medium, scan.Low} {
		if c[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c[s], s))
		}
	}
	if v.scanner != "" {
		parts = append(parts, "· "+v.scanner)
	}
	return strings.Join(parts, " ")
}

func (v *ScanView) rebuildRows() {
	f := parseFilter(v.filter)
	rows := make([]table.Row, 0, len(v.all))
	v.visible = v.visible[:0]
	for _, x := range v.all {
		if !f.Empty() && !f.MatchesAny(x.ID, x.Package, x.Severity.String(), x.Title) {
			continue
		}
		rows = append(rows, table.Row{
			severityCell(x.Severity), x.ID, truncate(x.Package, 24), truncate(x.Installed, 16),
			truncate(x.FixedIn, 20), truncate(x.Title, 200),
		})
		v.visible = append(v.visible, x)
	}
	sortRows(&v.tableSort, rows, v.visible)
	setTableRows(&v.table, rows)
}

func severityCell(s scan.Severity) string {
	switch s {
	case scan.Critical:
		return style.StateDead.Bold(true).Render(s.String())
	case scan.High:
		return style.StateDead.Render(s.String())
	case scan.Medium:
		return style.StatePaused.Render(s.String())
	default:
		return style.Muted.Render(s.String())
	}
}

// Selected is the finding under the cursor.
func (v *ScanView) Selected() (scan.Vuln, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.visible) {
		return scan.Vuln{}, false
	}
	return v.visible[i], true
}

// VulnReport is one finding as text, with where to read more.
func VulnReport(x scan.Vuln) string {
	lines := []string{
		x.ID + "  " + x.Severity.String(),
		"",
		"package    " + x.Package,
		"installed  " + x.Installed,
	}
	if x.FixedIn != "" {
		lines = append(lines, "fixed in   "+x.FixedIn)
	} else {
		lines = append(lines, "fixed in   (no fixed version known)")
	}
	if x.Title != "" {
		lines = append(lines, "", x.Title)
	}
	if u := VulnURL(x.ID); u != "" {
		lines = append(lines, "", u)
	}
	return strings.Join(lines, "\n")
}

// VulnURL is where an advisory is published, by its ID's namespace.
func VulnURL(id string) string {
	switch {
	case strings.HasPrefix(id, "CVE-"):
		return "https://nvd.nist.gov/vuln/detail/" + id
	case strings.HasPrefix(id, "GHSA-"):
		return "https://github.com/advisories/" + id
	case strings.HasPrefix(id, "GO-"):
		return "https://pkg.go.dev/vuln/" + id
	default:
		return ""
	}
}

// HandleKey maps a keystroke to an app action.
func (v *ScanView) HandleKey(key string) (string, string) {
	x, ok := v.Selected()
	if !ok || key != KeyEnter {
		return "", ""
	}
	return "vuln_detail", VulnReport(x)
}

// CopyFields is the finding's ID, twice: it is its own name.
func (v *ScanView) CopyFields() (string, string, bool) {
	x, ok := v.Selected()
	return x.ID, x.ID, ok
}

// UpdateTable forwards navigation keys.
func (v *ScanView) UpdateTable(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return cmd
}

// Resize re-lays the table.
func (v *ScanView) Resize(width, height int) {
	v.table.SetWidth(width)
	v.table.SetHeight(height)
	v.table.SetColumns(v.layout(scanColumns(), width))
	v.table.SetStyles(tableStylesWithWidth(width))
}

// Count is the visible row count.
func (v *ScanView) Count() int { return len(v.table.Rows()) }

// Loading is false even while scanning: the view draws its own message,
// which says why it is slow, instead of the generic spinner.
func (v *ScanView) Loading() bool { return false }

// Scanning reports whether the scan is still running.
func (v *ScanView) Scanning() bool { return v.running }

// SetFilter applies a row filter.
func (v *ScanView) SetFilter(f string) {
	v.filter = f
	v.rebuildRows()
}

// View renders the table, or what the scan is doing.
func (v *ScanView) View() string {
	switch {
	case v.running:
		return style.Muted.Render(fmt.Sprintf(
			"  scanning %s … %ds\n\n  The first scan downloads the scanner's vulnerability database, which can take a few minutes.",
			v.ref,
			int(time.Since(v.started).Seconds()),
		))
	case v.err != nil:
		return style.Error.Render("  " + v.err.Error())
	case len(v.all) == 0:
		return style.Success.Render(fmt.Sprintf("  %s: no known vulnerabilities (%s)", v.ref, v.scanner))
	}
	return fixSelectedRow(v.table.View())
}

// Table is the view's table, for the keys every table shares.
func (v *ScanView) Table() *table.Model { return &v.table }

// SortKey sorts the table on shift+←/→.
func (v *ScanView) SortKey(key string) bool { return v.sortKey(key, &v.table, v.rebuildRows) }

// Relayout lays the table out again after the column layouts changed.
func (v *ScanView) Relayout() { v.relayout(&v.table, v.rebuildRows) }
