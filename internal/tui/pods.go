package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/engines"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// podmanOf finds the podman provider among the detected runtimes.
func podmanOf(providers []engines.Provider) *engines.Podman {
	for _, p := range providers {
		if pm, ok := p.(engines.Podman); ok {
			return &pm
		}
	}
	return nil
}

func (a *App) podman() *engines.Podman {
	rv := a.runtimesView()
	if rv == nil {
		return nil
	}
	return podmanOf(rv.Providers())
}

// podRun runs a pod verb and reports it like any other action.
func (a *App) podRun(key, verb, done string) tea.Cmd {
	pm := a.podman()
	if pm == nil {
		return nil
	}
	machine, id, name := views.SplitPodKey(key)
	a.flash = fmt.Sprintf("%s pod %s…", verb, name)
	return a.run(done, "pod "+name, func(ctx context.Context) error {
		return pm.PodVerb(ctx, machine, id, verb)
	})
}

func (a *App) podInspect(key string) tea.Cmd {
	pm := a.podman()
	if pm == nil {
		return nil
	}
	machine, id, name := views.SplitPodKey(key)
	v := views.NewInspectFetchView(name, func(ctx context.Context) ([]byte, error) {
		return pm.PodInspect(ctx, machine, id)
	})
	a.setView(style.ViewInspect, v)
	a.pushView(style.ViewInspect)
	return v.Init()
}
