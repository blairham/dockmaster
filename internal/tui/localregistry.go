// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// The local registry's commands (#120): :registry up|stop, :catalog [addr].
const (
	cmdRegistryUp   = "registry up"
	cmdRegistryStop = "registry stop"
	cmdCatalog      = "catalog"
)

// verbDeleted is a tag delete's flash.
const verbDeleted = "deleted"

// registryUpTimeout bounds :registry up: it may pull registry:2 first.
const registryUpTimeout = 5 * time.Minute

// The local registry's daemon calls; vars so tests run them against no
// daemon.
var (
	localRegistryUpFn = func(ctx context.Context, c *docker.Client, spec docker.Registry) (
		docker.LocalRegistry, bool, error,
	) {
		if c == nil {
			return docker.LocalRegistry{}, false, errNotConnected
		}
		return c.UpLocalRegistry(ctx, spec)
	}
	registryStopFn = func(ctx context.Context, c *docker.Client, name string) error {
		if c == nil {
			return errNotConnected
		}
		return c.SetRegistryRunning(ctx, name, false)
	}
	registryRemoveFn = func(ctx context.Context, c *docker.Client, name string) error {
		if c == nil {
			return errNotConnected
		}
		return c.RemoveContainer(ctx, name, true, false)
	}
	registryGCFn = func(ctx context.Context, c *docker.Client, name string) (int, error) {
		if c == nil {
			return 0, errNotConnected
		}
		return c.GarbageCollectRegistry(ctx, name)
	}
	registryDeleteFn = func(ctx context.Context, addr, repo, digest string) error {
		return views.NewRegistryClient(addr).Delete(ctx, repo, digest)
	}
)

// localRegistrySpec is the registry :registry up creates: config's name
// and port, else the defaults.
func (a *App) localRegistrySpec() docker.Registry {
	name, port := a.localRegistryName, a.localRegistryPort
	if name == "" {
		name = config.DefaultLocalRegistryName
	}
	if port == 0 {
		port = config.DefaultLocalRegistryPort
	}
	return docker.LocalRegistrySpec(name, strconv.Itoa(port))
}

// registryUp starts the local registry — name's container when one is
// given, else whichever UpLocalRegistry reuses or creates — and refreshes
// what is on screen.
func (a *App) registryUp(name string) tea.Cmd {
	spec := a.localRegistrySpec()
	if name != "" {
		spec.Name = name
	}
	client := a.client
	a.flash = "starting the local registry…"
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(registryUpTimeout)
		defer cancel()
		r, reused, err := localRegistryUpFn(ctx, client, spec)
		if err != nil {
			return actionDoneMsg{err: fmt.Errorf("local registry: %w", err)}
		}
		verb := "local registry up at " + r.Address() + " —"
		switch {
		case reused && r.Kind != docker.RegistryKindDockmaster:
			verb += " reusing " + r.Kind + "'s"
		case reused:
			verb += " started"
		default:
			verb += " created"
		}
		return actionDoneMsg{verb: verb, subject: r.Name}
	}
}

// registryStop stops a registry container.
func (a *App) registryStop(name string) tea.Cmd {
	client := a.client
	return a.run("stopped registry", name, func(ctx context.Context) error {
		return registryStopFn(ctx, client, name)
	})
}

// confirmRegistryRemove asks before removing dockmaster's registry
// container; its volume, and the images in it, stay. A cluster's registry
// is refused: it belongs to the cluster.
func (a *App) confirmRegistryRemove(param string) {
	name, kind, _ := strings.Cut(param, "\x00")
	if kind != docker.RegistryKindDockmaster {
		a.errFlash = name + " is " + kind + "'s registry, not dockmaster's — remove it with what made it"
		return
	}
	a.openConfirm("remove_registry", name, fmt.Sprintf(
		"remove the registry container %s? its images stay in the volume %s-data; :registry up brings it back",
		name, name,
	))
}

// confirmRegistryGC asks before garbage-collecting a registry.
func (a *App) confirmRegistryGC(name string) {
	a.openConfirm("registry_gc", name, fmt.Sprintf(
		"garbage-collect %s? blobs no tag references are deleted for good; a push running meanwhile can lose layers",
		name,
	))
}

// registryGC runs the garbage collection and says how many blobs went.
func (a *App) registryGC(name string) tea.Cmd {
	client := a.client
	a.flash = "garbage-collecting " + name + "…"
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		n, err := registryGCFn(ctx, client, name)
		return actionDoneMsg{verb: "garbage-collected", subject: fmt.Sprintf("%s: %d blobs deleted", name, n), err: err}
	}
}

// confirmRegistryDeleteTag asks before deleting a tag's manifest; it names
// the digest, since every tag on it goes too.
func (a *App) confirmRegistryDeleteTag(param string) {
	t := views.DecodeRegistryTarget(param)
	a.openConfirm("registry_delete_tag", param, fmt.Sprintf(
		"delete %s from the registry? its manifest %s goes, with every tag pointing at it; P frees the space",
		t.Ref(), shortDigest(t.Digest),
	))
}

// registryDeleteTag deletes the manifest by digest.
func (a *App) registryDeleteTag(param string) tea.Cmd {
	t := views.DecodeRegistryTarget(param)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionDoneMsg{verb: verbDeleted, subject: t.Ref(), err: registryDeleteFn(ctx, t.Addr, t.Repo, t.Digest)}
	}
}

func shortDigest(d string) string {
	if len(d) > len("sha256:")+12 {
		return d[:len("sha256:")+12]
	}
	return d
}

// openRegistryCatalog pushes the catalog view of the registry t names.
func (a *App) openRegistryCatalog(t views.RegistryTarget) tea.Cmd {
	a.setView(style.ViewRegistryCatalog, views.NewRegistryCatalogView(t))
	a.pushView(style.ViewRegistryCatalog)
	return a.viewMap[style.ViewRegistryCatalog].Init()
}

// openRegistryTags pushes the tags view of the repository t names.
func (a *App) openRegistryTags(t views.RegistryTarget) tea.Cmd {
	a.setView(style.ViewRegistryTags, views.NewRegistryTagsView(t))
	a.pushView(style.ViewRegistryTags)
	return a.viewMap[style.ViewRegistryTags].Init()
}

// catalogCommand is :catalog [addr]: that registry's repositories, or with
// no address the local registry's — found on the daemon, so this asks it
// first.
func (a *App) catalogCommand(addr string) (string, tea.Cmd) {
	if addr != "" {
		return "", a.openRegistryCatalog(views.RegistryTarget{Addr: addr})
	}
	client := a.client
	return "", func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		regs, err := views.ListLocalRegistries(ctx, client)
		return localCatalogMsg{regs: regs, err: err}
	}
}

// localCatalogMsg is :catalog's look for the local registry.
type localCatalogMsg struct {
	err  error
	regs []docker.LocalRegistry
}

func (a *App) handleLocalCatalog(msg localCatalogMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.errFlash = docker.FormatUserError(msg.err).Error()
		return a, nil
	}
	for _, r := range msg.regs {
		if r.Address() != "" && r.Running {
			cmd := a.openRegistryCatalog(views.RegistryTarget{Addr: r.Address(), Container: r.Name})
			return a, cmd
		}
	}
	a.errFlash = "no local registry running — :registry up starts one, or :catalog <registry>"
	return a, nil
}

// registryStopCommand is :registry stop: dockmaster's own registry, by
// its configured name. A cluster's registry stops with x in :registries.
func (a *App) registryStopCommand() (string, tea.Cmd) {
	_, cmd := a.handleAction(views.ActRegistryStop, a.localRegistrySpec().Name)
	return "", cmd
}
