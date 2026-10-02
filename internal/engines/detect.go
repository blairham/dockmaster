// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package engines

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/blairham/dockmaster/internal/colima"
)

// Env is what detection consults, swappable so tests decide what is
// "installed".
type Env struct {
	Run         Runner
	Ping        func(ctx context.Context, host string) bool
	LookPath    func(string) (string, error)
	AppExists   func(name string) bool
	ContextHost func(name string) (string, bool)
	FileExists  func(path string) bool
	Colima      *colima.Client
	Home        string
}

// SystemEnv is the real machine.
func SystemEnv(ping func(context.Context, string) bool, contextHost func(string) (string, bool)) Env {
	home, _ := os.UserHomeDir() //nolint:errcheck // "" just means no fallback sockets
	c, _ := colima.New()        //nolint:errcheck // ErrNotInstalled is the only error, and nil is its answer
	return Env{
		Run: ExecRunner, Ping: ping, LookPath: exec.LookPath, ContextHost: contextHost,
		Home: home, Colima: c,
		FileExists: func(path string) bool { _, err := os.Stat(path); return err == nil },
		AppExists: func(name string) bool {
			if runtime.GOOS != "darwin" {
				return false
			}
			for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
				if _, err := os.Stat(filepath.Join(dir, name+".app")); err == nil {
					return true
				}
			}
			return false
		},
	}
}

// Detect returns a provider for every runtime installed here, in a fixed
// order. A runtime counts as installed when its CLI is on PATH or its app
// is; a docker context left behind by an uninstalled app does not count.
func Detect(ctx context.Context, env Env) []Provider {
	var out []Provider
	has := func(bin string) bool { _, err := env.LookPath(bin); return err == nil }

	if env.Colima != nil {
		out = append(out, Colima{C: env.Colima})
	}
	if has("podman") {
		out = append(out, Podman{Run: env.Run})
	}

	// Docker Desktop: its `docker desktop` CLI plugin (4.37+) when present,
	// else the app. The docker CLI only finds the plugin in its own plugin
	// directories, and a Homebrew docker does not look inside Docker.app, so
	// the plugin binary is also run directly — `docker-desktop desktop
	// start` is what `docker desktop start` execs.
	var ddCmd []string
	if has("docker") {
		if _, err := env.Run(ctx, "docker", "desktop", "version"); err == nil {
			ddCmd = []string{"docker", "desktop"}
		}
	}
	if ddCmd == nil {
		if path := env.desktopPlugin(); path != "" {
			ddCmd = []string{path, "desktop"}
		}
	}
	if ddCmd != nil || env.AppExists("Docker") {
		s := single(env, DockerDesktopName, "desktop-linux", ".docker/run/docker.sock")
		if ddCmd != nil {
			s.StartC = append(append([]string(nil), ddCmd...), "start")
			s.StopC = append(append([]string(nil), ddCmd...), "stop")
			s.StatusC = append(append([]string(nil), ddCmd...), "status", "--format", "json")
			s.ParseStatus = DockerDesktopStatus
			kube := append(append([]string(nil), ddCmd...), "kubernetes", "status", "--format", "json")
			s.K8sStatus = kubeStatus(env.Run, kube, DockerDesktopKubeStatus)
		} else {
			s.StartC, s.StopC = openApp("Docker"), quitApp("Docker")
		}
		out = append(out, s)
	}

	// Rancher Desktop: rdctl when it can be found — on PATH only once the
	// app's first-run setup has added ~/.rd/bin, but shipped inside the app
	// from the start — else the app.
	rdctl := env.bundledRdctl()
	if has("rdctl") {
		rdctl = "rdctl"
	}
	if rdctl != "" || env.AppExists("Rancher Desktop") {
		s := single(env, RancherDesktopName, "rancher-desktop", ".rd/docker.sock")
		if rdctl != "" {
			s.StartC, s.StopC = []string{rdctl, "start"}, []string{rdctl, "shutdown"}
			s.K8sStatus = kubeStatus(env.Run, []string{rdctl, "list-settings"}, RancherKubeStatus)
			s.Resources = resources(env.Run, []string{rdctl, "list-settings"}, RancherResources)
		} else {
			s.StartC, s.StopC = openApp("Rancher Desktop"), quitApp("Rancher Desktop")
		}
		out = append(out, s)
	}

	if has("orb") || env.AppExists("OrbStack") {
		s := single(env, OrbStackName, "orbstack", ".orbstack/run/docker.sock")
		if has("orb") {
			s.StartC, s.StopC = []string{"orb", "start"}, []string{"orb", "stop"}
			s.StatusC, s.ParseStatus = []string{"orb", "status"}, OrbStackStatus
			s.K8sStatus = kubeStatus(env.Run, []string{"orb", "config", "get", "k8s.enable"}, OrbStackKubeStatus)
		} else {
			s.StartC, s.StopC = openApp("OrbStack"), quitApp("OrbStack")
		}
		out = append(out, s)
	}
	return out
}

// desktopPlugin finds Docker Desktop's CLI plugin binary where Docker
// Desktop and the docker CLI put it.
func (env Env) desktopPlugin() string {
	if env.FileExists == nil {
		return ""
	}
	candidates := []string{
		"/Applications/Docker.app/Contents/Resources/cli-plugins/docker-desktop",
		"/usr/local/lib/docker/cli-plugins/docker-desktop",
		"/usr/lib/docker/cli-plugins/docker-desktop",
	}
	if env.Home != "" {
		candidates = append([]string{
			filepath.Join(env.Home, ".docker/cli-plugins/docker-desktop"),
			filepath.Join(env.Home, "Applications/Docker.app/Contents/Resources/cli-plugins/docker-desktop"),
		}, candidates...)
	}
	for _, p := range candidates {
		if env.FileExists(p) {
			return p
		}
	}
	return ""
}

// bundledRdctl is Rancher Desktop's rdctl where the app installs it: in
// ~/.rd/bin once first-run setup has run, and inside the app bundle always.
// "" when there is none.
func (env Env) bundledRdctl() string {
	if env.FileExists == nil {
		return ""
	}
	const inApp = "Rancher Desktop.app/Contents/Resources/resources/darwin/bin/rdctl"
	candidates := []string{filepath.Join("/Applications", inApp)}
	if env.Home != "" {
		candidates = append([]string{
			filepath.Join(env.Home, ".rd/bin/rdctl"),
			filepath.Join(env.Home, "Applications", inApp),
		}, candidates...)
	}
	for _, p := range candidates {
		if env.FileExists(p) {
			return p
		}
	}
	return ""
}

// single builds a single-engine provider, finding its endpoint in the docker
// context store and falling back to the socket the app is known to create.
func single(env Env, name, ctxName, socket string) Single {
	host := ""
	if env.ContextHost != nil {
		host, _ = env.ContextHost(ctxName)
	}
	if host == "" && env.Home != "" {
		host = "unix://" + filepath.Join(env.Home, socket)
	}
	return Single{Run: env.Run, Ping: env.Ping, Engine: name, Context: ctxName, Host: host, SocketExists: socketExists}
}

// socketExists reports whether a socket file is on disk. Anything but a
// clean "does not exist" counts as present, so a permissions error falls
// through to the runtime's own status rather than reporting it stopped.
func socketExists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

func openApp(name string) []string { return []string{"open", "-a", name} }

func quitApp(name string) []string {
	return []string{"osascript", "-e", `quit app "` + name + `"`}
}

// Owner finds the machine that serves a docker endpoint, among every
// detected runtime — how dockmaster knows that a daemon it cannot reach is a
// machine it could start.
func Owner(ctx context.Context, providers []Provider, host string) (Machine, bool) {
	if host == "" {
		return Machine{}, false
	}
	for _, p := range providers {
		ms, err := p.List(ctx)
		if err != nil {
			continue
		}
		for _, m := range ms {
			if m.Host == host {
				return m, true
			}
		}
	}
	return Machine{}, false
}
