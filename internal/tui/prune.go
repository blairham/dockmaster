package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockyard/internal/docker"
)

// pruneStep is one stage of a combined prune: what it removes, and how.
type pruneStep struct {
	run  func(context.Context) (int, uint64, error)
	kind string
}

// pruneAllSteps is `docker system prune`, in its order: stopped containers
// first, because removing them is what frees their networks, images and
// build cache for the steps after. Volumes come last and only on request —
// Docker leaves them out by default too, since that is where data lives.
func (a *App) pruneAllSteps(volumes bool) []pruneStep {
	steps := []pruneStep{
		{kind: "containers", run: func(ctx context.Context) (int, uint64, error) { return a.client.PruneContainers(ctx) }},
		{kind: "networks", run: func(ctx context.Context) (int, uint64, error) {
			n, err := a.client.PruneNetworks(ctx)
			return n, 0, err
		}},
		{kind: "images", run: func(ctx context.Context) (int, uint64, error) { return a.client.PruneImages(ctx, true) }},
		{kind: "build cache", run: func(ctx context.Context) (int, uint64, error) { return a.client.PruneBuildCache(ctx) }},
	}
	if volumes {
		steps = append(steps, pruneStep{kind: "volumes", run: func(ctx context.Context) (int, uint64, error) {
			return a.client.PruneVolumes(ctx)
		}})
	}
	return steps
}

// pruneOnly keeps the steps of the given kinds, in their order.
func pruneOnly(steps []pruneStep, kinds ...string) []pruneStep {
	var out []pruneStep
	for _, s := range steps {
		for _, k := range kinds {
			if s.kind == k {
				out = append(out, s)
			}
		}
	}
	return out
}

// runPruneSteps runs every step in order and summarizes them. A failed step
// does not stop the rest — pruning the networks is still worth doing when
// the image prune was refused — and every failure is reported.
func runPruneSteps(ctx context.Context, steps []pruneStep) (string, error) {
	var (
		parts     []string
		failed    []error
		reclaimed uint64
	)
	for _, s := range steps {
		n, bytes, err := s.run(ctx)
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", s.kind, err))
			continue
		}
		reclaimed += bytes
		parts = append(parts, fmt.Sprintf("%d %s", n, pruneNoun(s.kind, n)))
	}
	summary := strings.Join(parts, ", ")
	if reclaimed > 0 {
		summary += ", " + docker.HumanSize(int64(reclaimed)) + " reclaimed" //nolint:gosec // daemon-reported byte count
	}
	return summary, errors.Join(failed...)
}

// pruneNoun keeps "1 containers" out of the summary; build cache counts
// entries.
func pruneNoun(kind string, n int) string {
	if kind == "build cache" {
		if n == 1 {
			return "build-cache entry"
		}
		return "build-cache entries"
	}
	if n == 1 {
		return strings.TrimSuffix(kind, "s")
	}
	return kind
}

// pruneAllTimeout bounds a combined prune. Each step is a daemon call that
// walks its whole store; on a slow VM-backed daemon the image and build-cache
// steps alone can outlast actionTimeout.
const pruneAllTimeout = 10 * time.Minute

// runPruneAll runs the steps in the background and reports as an
// actionDoneMsg, so the flash, error surface and refresh are the usual ones.
// A partial failure still reports what was pruned before naming what failed.
func (a *App) runPruneAll(steps []pruneStep) tea.Cmd {
	a.flash = "pruning…"
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pruneAllTimeout)
		defer cancel()
		summary, err := runPruneSteps(ctx, steps)
		if err != nil {
			if summary != "" {
				err = fmt.Errorf("pruned %s — but %w", summary, err)
			}
			return actionDoneMsg{verb: "prune", err: err}
		}
		return actionDoneMsg{verb: "pruned", subject: summary}
	}
}
