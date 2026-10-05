// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"errors"
	"slices"
	"strings"
)

// LabelHelper marks a helper container dockmaster starts; its value names
// the helper, so `docker ps --filter label=dockmaster.helper` finds any
// left behind.
const LabelHelper = "dockmaster.helper"

// HelperHostShell is LabelHelper's value on the host-shell helper.
const HelperHostShell = "hostshell"

// HostShellArgs is the docker CLI argv, after the endpoint flags, that
// opens a root shell on the daemon's host (#59): a throwaway privileged
// helper in the host's PID namespace runs nsenter into PID 1's mount, UTS,
// IPC, network and PID namespaces, then the host's own sh.
//
//   - --privileged: setns into another mount namespace needs
//     CAP_SYS_ADMIN, and the default seccomp profile refuses setns.
//   - --pid=host: so that PID 1 is the host's init, not the helper's own.
//   - --net=host: the helper never uses a network of its own, so none is
//     made for it; nsenter -n enters the host's anyway.
//   - --rm: nothing is left behind when the shell exits.
//
// The image is one argument, placed after every flag, and nothing here is
// ever parsed by a shell. An image starting with "-" would be read by
// docker as a flag, so it is refused.
func HostShellArgs(image string) ([]string, error) {
	if err := ValidHostShellImage(image); err != nil {
		return nil, err
	}
	return []string{
		"run", "--rm", "-it",
		"--privileged", "--pid=host", "--net=host",
		"--label", LabelHelper + "=" + HelperHostShell,
		image,
		"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", "sh",
	}, nil
}

// ValidHostShellImage refuses an image docker would not read as one: empty,
// or starting with "-", which docker run would take for a flag.
func ValidHostShellImage(image string) error {
	switch {
	case strings.TrimSpace(image) == "":
		return errors.New("the host-shell image is empty")
	case strings.HasPrefix(image, "-"):
		return errors.New("the host-shell image cannot start with '-': docker would read it as a flag")
	}
	return nil
}

// rootless reports whether a daemon's security options say it runs
// rootless — rootless docker and rootless podman both report
// "name=rootless".
func rootless(securityOptions []string) bool {
	return slices.Contains(securityOptions, "name=rootless")
}
