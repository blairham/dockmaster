// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import tea "charm.land/bubbletea/v2"

// Compile-time interface checks.
var (
	_ View = (*ContainersView)(nil)
	_ View = (*ImagesView)(nil)
	_ View = (*VolumesView)(nil)
	_ View = (*NetworksView)(nil)
	_ View = (*ProjectsView)(nil)
	_ View = (*LogsView)(nil)
	_ View = (*InspectView)(nil)
	_ View = (*LayersView)(nil)
	_ View = (*ContextsView)(nil)
	_ View = (*RuntimesView)(nil)
	_ View = (*RuntimeFormView)(nil)
	_ View = (*DiskUsageView)(nil)
	_ View = (*PortForwardsView)(nil)
	_ View = (*PodsView)(nil)
	_ View = (*EventsView)(nil)
	_ View = (*PulsesView)(nil)
	_ View = (*AliasesView)(nil)

	_ InputCapturer = (*RuntimeFormView)(nil)

	_ Stoppable = (*LogsView)(nil)
	_ Stoppable = (*EventsView)(nil)
	_ Stoppable = (*PulsesView)(nil)

	_ Poller = (*PulsesView)(nil)
)

// View is what every dockmaster view implements.
//
// HandleKey returns an (action, param) pair rather than a tea.Cmd so the
// app owns every mutation — a view can ask for a container to be killed,
// but it cannot kill one itself. That is what makes the --readonly flag a
// single check in the app rather than a flag threaded through nine views.
type View interface {
	Init() tea.Cmd
	Update(tea.Msg) tea.Cmd
	UpdateTable(tea.Msg) tea.Cmd
	View() string
	Resize(width, height int)
	HandleKey(key string) (action, param string)
	SetFilter(string)
	Count() int
	Loading() bool
	Refresh() tea.Cmd
}

// DigitClaimer is implemented by a view that uses the digit keys itself —
// the log view's time ranges — so in it they do not switch views.
type DigitClaimer interface {
	ClaimsDigits() bool
}

// Backer is a view that handles esc and q itself before the app pops it —
// the volume browser, which climbs a directory first.
type Backer interface {
	Back() (tea.Cmd, bool)
}

// TitleStatus is a view with a note for its border title, after its name —
// a log's time range, the containers hidden from a list.
type TitleStatus interface {
	Status() string
}

// Stoppable is implemented by views holding background goroutines (the log
// tail). The app calls Stop on view switch and at shutdown.
type Stoppable interface {
	Stop()
}

// Titler is implemented by drill-in views that name their subject in the
// border title ("nginx" rather than "logs").
type Titler interface {
	Title() string
}

// Poller is a view whose poll tick is not the same as opening it or r: the
// pulses dashboard closes a sample interval on each tick, and reads disk
// usage only every so many. The app calls Poll on the tick instead of
// Refresh.
type Poller interface {
	Poll() tea.Cmd
}
