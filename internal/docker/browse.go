// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// WebPorts are the container's published TCP ports, one per host port and
// sorted by it: a port bound on both 0.0.0.0 and :: is listed twice by the
// daemon and is still one thing to open, and the daemon's own order is
// arbitrary — a kind control plane lists its ~500 NodePorts shuffled.
func WebPorts(c Container) []PortMapping {
	seen := map[uint16]bool{}
	var out []PortMapping
	for _, p := range c.PortList {
		if p.Public == 0 || (p.Type != "" && p.Type != "tcp") || seen[p.Public] {
			continue
		}
		seen[p.Public] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Public < out[j].Public })
	return out
}

// BrowseHost is the host a published port is reached on from this machine:
// the daemon's own host for a tcp:// or ssh:// endpoint, localhost for a
// local socket. Colima, Docker Desktop, Rancher, OrbStack and Podman all
// forward published ports to the Mac's localhost, so a socket endpoint is
// local even when the daemon runs in a VM.
func BrowseHost(daemonHost string) string {
	u, err := url.Parse(daemonHost)
	if err != nil {
		return "localhost"
	}
	switch u.Scheme {
	case "tcp", "ssh", "http", "https":
		h := u.Hostname()
		if h == "" || h == "0.0.0.0" || h == "::" {
			return "localhost"
		}
		return h
	}
	return "localhost"
}

// tlsPorts are container-side ports conventionally served over TLS: 443,
// 8443, and 6443, the Kubernetes API server a kind node publishes.
var tlsPorts = map[uint16]bool{443: true, 8443: true, 6443: true}

// PortURL is the URL to open for a published port on host: https when the
// container side is a conventional TLS port, http otherwise.
func PortURL(host string, p PortMapping) string {
	scheme := "http"
	if tlsPorts[p.Private] {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(int(p.Public)))
}

// maxPortChoices bounds how many ports a prompt lists; any published port
// can still be typed.
const maxPortChoices = 6

// PortChoices renders ports for a prompt: "8080→80 8443→443", the first
// few and a count of the rest.
func PortChoices(ports []PortMapping) string {
	parts := make([]string, 0, maxPortChoices+1)
	for i, p := range ports {
		if i == maxPortChoices {
			parts = append(parts, fmt.Sprintf("+%d more", len(ports)-maxPortChoices))
			break
		}
		parts = append(parts, strconv.Itoa(int(p.Public))+"→"+strconv.Itoa(int(p.Private)))
	}
	return strings.Join(parts, " ")
}
