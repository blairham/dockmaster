// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package kind creates kind clusters the way the user's own was set up:
// a control-plane and a worker, with containerd reading per-registry
// config from /etc/containerd/certs.d so kind's local-registry recipe
// works. No bubbletea in here.
package kind

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DefaultName is the cluster name, as the user's: its kubectl context is
// kind-k8s.
const DefaultName = "k8s"

// Config is the cluster config `kind create cluster` reads: two nodes, and
// the containerd patch kind's local-registry recipe needs — the same
// setting, checked, as a working cluster's /etc/containerd/config.toml.
const Config = `kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/etc/containerd/certs.d"
`

// Runner runs kind with an environment and stdin; a var in Create's caller
// so tests never create a cluster.
type Runner func(ctx context.Context, env []string, stdin string, args ...string) ([]byte, error)

// ExecRunner runs the real kind binary.
func ExecRunner(ctx context.Context, env []string, stdin string, args ...string) ([]byte, error) {
	bin, err := exec.LookPath("kind")
	if err != nil {
		return nil, errors.New("kind is not installed — brew install kind")
	}
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary; args are dockmaster's own
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("kind %s: %w: %s", strings.Join(args, " "), err, lastLine(out.String()))
	}
	return out.Bytes(), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// Create makes a cluster called name on the daemon at host, writing its
// kubectl context (kind-<name>) and making it current, as kind does.
func Create(ctx context.Context, run Runner, host, name string) error {
	_, err := run(ctx, []string{"DOCKER_HOST=" + host}, Config,
		"create", "cluster", "--name", name, "--config", "-")
	return err
}

// Name picks the cluster's name: DefaultName, unless kubeconfig already
// has its context (kind-k8s) — another machine's cluster — and then
// "k8s-<machine>", so creating one never overwrites another's context.
func Name(contexts []string, machine string) string {
	for _, c := range contexts {
		if c == "kind-"+DefaultName {
			return DefaultName + "-" + machine
		}
	}
	return DefaultName
}

// Contexts lists the context names in the user's kubeconfig: $KUBECONFIG's
// first file, else ~/.kube/config. No kubeconfig is no contexts.
func Contexts() ([]string, error) {
	var path string
	if k := os.Getenv("KUBECONFIG"); k != "" {
		path = filepath.SplitList(k)[0]
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".kube", "config")
	}
	b, err := os.ReadFile(path) //nolint:gosec // the user's own kubeconfig
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var kc struct {
		Contexts []struct {
			Name string `yaml:"name"`
		} `yaml:"contexts"`
	}
	if err := yaml.Unmarshal(b, &kc); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	names := make([]string, 0, len(kc.Contexts))
	for _, c := range kc.Contexts {
		names = append(names, c.Name)
	}
	return names, nil
}
