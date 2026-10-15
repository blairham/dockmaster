// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
)

// NetTarget is one container a network picker acts on: its ID, its name
// for the confirm and the flash, and the networks it is on now.
type NetTarget struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Networks []string `json:"networks,omitempty"`
}

// on reports whether the target is attached to network.
func (t NetTarget) on(network string) bool {
	for _, n := range t.Networks {
		if n == network {
			return true
		}
	}
	return false
}

// NetTargets is what `N` hands the app: the marked containers, or the
// selected one.
func NetTargets(cs []docker.Container) []NetTarget {
	out := make([]NetTarget, 0, len(cs))
	for _, c := range cs {
		t := NetTarget{ID: c.ID, Name: c.Name}
		for _, e := range c.Endpoints {
			t.Networks = append(t.Networks, e.Network)
		}
		out = append(out, t)
	}
	return out
}

// EncodeNetTargets packs targets as an action param.
func EncodeNetTargets(ts []NetTarget) string {
	b, err := json.Marshal(ts)
	if err != nil {
		return ""
	}
	return string(b)
}

// DecodeNetTargets reads EncodeNetTargets' param back.
func DecodeNetTargets(s string) ([]NetTarget, error) {
	var ts []NetTarget
	if err := json.Unmarshal([]byte(s), &ts); err != nil {
		return nil, err
	}
	if len(ts) == 0 {
		return nil, errors.New("no containers to connect")
	}
	return ts, nil
}

// NetChange is a submitted network picker: connect Targets to Network
// (with Aliases and IP), or — Disconnect — detach them from it.
type NetChange struct {
	Network    string      `json:"network"`
	IP         string      `json:"ip,omitempty"`
	Targets    []NetTarget `json:"targets"`
	Aliases    []string    `json:"aliases,omitempty"`
	Disconnect bool        `json:"disconnect,omitempty"`
}

// DecodeNetChange reads a submitted picker back.
func DecodeNetChange(s string) (NetChange, error) {
	var c NetChange
	err := json.Unmarshal([]byte(s), &c)
	return c, err
}

// DisconnectParam is the param of one container's disconnect, which the
// used-by list's ctrl-d and each row of a marked one ask for.
func DisconnectParam(network string, c docker.Container) string {
	b, err := json.Marshal(NetChange{
		Network: network, Disconnect: true,
		Targets: []NetTarget{{ID: c.ID, Name: c.Name}},
	})
	if err != nil {
		return ""
	}
	return string(b)
}

// NetworkPickerView is the network picker (#107): every network, the ones
// the containers are already on marked. Choosing one they are not on
// connects them, with optional aliases and a fixed address; choosing one
// they are all on disconnects them, which the app confirms. It runs
// nothing itself.
type NetworkPickerView struct {
	targets []NetTarget
	names   []string
	form
}

// ActConfirmDisconnect asks to disconnect containers from a network: the
// picker's choice of one they are all on, and ctrl-d in its used-by list.
const ActConfirmDisconnect = "confirm_disconnect_network"

// netField is the picker's network field.
const netField = "network"

// netOnMark marks a network the containers are on in the picker.
const netOnMark = " ● connected"

// NewNetworkPicker builds the picker over networks (by name) for targets.
// It starts on the first network not every target is on — connecting is
// what it is opened for most often.
func NewNetworkPicker(targets []NetTarget, networks []string) *NetworkPickerView {
	v := &NetworkPickerView{targets: targets, names: networks}
	labels := make([]string, len(networks))
	start := -1
	for i, n := range networks {
		labels[i] = n
		switch on := v.onCount(n); {
		case on == len(targets):
			labels[i] += netOnMark
		case on > 0:
			labels[i] += fmt.Sprintf(" ◐ %d of %d", on, len(targets))
			if start < 0 {
				start = i
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	nf := formField{key: netField, label: "Network", choices: labels, choice: max(start, 0)}
	alias := formField{key: "alias", label: "Aliases", input: newFormInput("", 256)}
	ip := formField{key: "ip", label: "IP", input: newFormInput("", 45)}
	v.fields = []formField{nf, alias, ip}
	v.labelWidth = 9
	v.valueWidth = 40
	v.onChoice = func(string) { v.follow() }
	v.follow()
	v.focusField(0)
	return v
}

// onCount is how many targets are on network.
func (v *NetworkPickerView) onCount(network string) int {
	n := 0
	for _, t := range v.targets {
		if t.on(network) {
			n++
		}
	}
	return n
}

// chosen is the network the choice field is on, by name.
func (v *NetworkPickerView) chosen() string {
	if len(v.names) == 0 {
		return ""
	}
	return v.names[v.field(netField).choice]
}

// disconnecting reports whether enter would disconnect: every target is
// on the chosen network.
func (v *NetworkPickerView) disconnecting() bool {
	n := v.chosen()
	return n != "" && v.onCount(n) == len(v.targets)
}

// follow updates the hints, and locks aliases and IP, for the network
// chosen: a disconnect takes neither.
func (v *NetworkPickerView) follow() {
	net := v.field(netField)
	alias, ip := v.field("alias"), v.field("ip")
	dis := v.disconnecting()
	alias.locked, ip.locked = dis, dis
	alias.hint, ip.hint = "optional, comma-separated", "optional, e.g. 172.20.0.9"
	if len(v.targets) > 1 {
		ip.hint = "one container only"
		ip.locked = true
	}
	switch {
	case len(v.names) == 0:
		net.hint = "no networks on this daemon"
	case dis:
		net.hint = "←/→ · enter disconnects (confirms)"
	default:
		net.hint = "←/→ · enter connects"
	}
}

// CapturesInput reports that the form takes every typed key.
func (v *NetworkPickerView) CapturesInput() bool { return true }

// Title names the containers, for the border title.
func (v *NetworkPickerView) Title() string {
	if len(v.targets) == 1 {
		return "networks of " + v.targets[0].Name
	}
	return fmt.Sprintf("networks of %d containers", len(v.targets))
}

// Init has nothing to start.
func (v *NetworkPickerView) Init() tea.Cmd { return nil }

// Update has no messages of its own.
func (v *NetworkPickerView) Update(tea.Msg) tea.Cmd { return nil }

// Resize — the form lays itself out by content.
func (v *NetworkPickerView) Resize(int, int) {}

// Count is the number of fields.
func (v *NetworkPickerView) Count() int { return len(v.fields) }

// Loading — a form has nothing to load.
func (v *NetworkPickerView) Loading() bool { return false }

// SetFilter — a form is not filtered.
func (v *NetworkPickerView) SetFilter(string) {}

// Refresh — nothing to refetch.
func (v *NetworkPickerView) Refresh() tea.Cmd { return nil }

// SetError shows a failed connect in the form, to be fixed.
func (v *NetworkPickerView) SetError(msg string) { v.err = msg }

// View renders the form.
func (v *NetworkPickerView) View() string {
	return v.render("enter connects or disconnects · tab next field · esc cancel")
}

// Change reads the fields: what enter would ask for.
func (v *NetworkPickerView) Change() NetChange {
	c := NetChange{Network: v.chosen(), Disconnect: v.disconnecting()}
	if c.Disconnect {
		c.Targets = v.targets
		return c
	}
	// Connect only the ones not on it yet: the daemon refuses a second
	// endpoint on the same network.
	for _, t := range v.targets {
		if !t.on(c.Network) {
			c.Targets = append(c.Targets, t)
		}
	}
	for a := range strings.SplitSeq(v.field("alias").value(), ",") {
		if a = strings.TrimSpace(a); a != "" {
			c.Aliases = append(c.Aliases, a)
		}
	}
	if !v.field("ip").locked {
		c.IP = v.field("ip").value()
	}
	return c
}

// HandleKey submits on enter. The address is parsed the way the connect
// request is built, so a bad one is reported in the form.
func (v *NetworkPickerView) HandleKey(key string) (string, string) {
	if key != KeyEnter {
		return "", ""
	}
	if len(v.names) == 0 {
		v.err = "no networks to connect to"
		return "", ""
	}
	c := v.Change()
	if _, err := docker.ParseEndpointIP(c.IP); err != nil {
		v.err = "IP: " + err.Error()
		return "", ""
	}
	for _, a := range c.Aliases {
		if strings.ContainsAny(a, " \t") {
			v.err = fmt.Sprintf("alias %q: no spaces", a)
			return "", ""
		}
	}
	b, err := json.Marshal(c)
	if err != nil {
		v.err = err.Error()
		return "", ""
	}
	if c.Disconnect {
		return ActConfirmDisconnect, string(b)
	}
	return "connect_network", string(b)
}
