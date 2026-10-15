// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// The tag and push commands (#102).
const (
	cmdTag  = "tag"
	cmdPush = "push"

	// actPush is the confirmed push.
	actPush = "push_image"
)

// pushTimeout bounds a push: layers go over the network to the registry,
// so it gets a snapshot's budget, not a single request's.
const pushTimeout = 30 * time.Minute

// pushImageFn pushes ref, reporting progress; a var so tests push without
// a daemon or a registry.
var pushImageFn = func(ctx context.Context, c *docker.Client, ref string, progress func(string)) (string, error) {
	if c == nil {
		return docker.RegistryForRef(ref), errNotConnected
	}
	return c.PushImage(ctx, ref, progress)
}

// tagImageFn tags source as target; a var for tests.
var tagImageFn = func(ctx context.Context, c *docker.Client, source, target string) error {
	if c == nil {
		return errNotConnected
	}
	return c.TagImage(ctx, source, target)
}

// errNotConnected is a tag or push with no daemon to ask.
var errNotConnected = errors.New("not connected to a daemon")

// tagDoneMsg reports a tag, or why it was refused.
type tagDoneMsg struct {
	err            error
	source, target string
}

// pushEvent is one report from a running push: a progress line, or the end.
type pushEvent struct {
	err      error
	line     string
	registry string
	done     bool
}

// pushProgressMsg carries a push's next event and the channel the rest
// come on, so the handler can wait for the one after.
type pushProgressMsg struct {
	ch  <-chan pushEvent
	ref string
	ev  pushEvent
}

// openTagForm opens the tag form on source, prefilled with its reference
// (nothing for an image known only by ID).
func (a *App) openTagForm(source string) {
	prefill := source
	if strings.HasPrefix(source, "sha256:") {
		prefill = ""
	}
	a.setView(style.ViewTagForm, views.NewTagForm(source, prefill))
	a.pushView(style.ViewTagForm)
}

// tagImage runs `docker tag` in the background; the form, if it is up,
// stays until it is done, so a refusal lands in it.
func (a *App) tagImage(param string) tea.Cmd {
	source, target, _ := strings.Cut(param, "\x00")
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(actionTimeout)
		defer cancel()
		return tagDoneMsg{source: source, target: target, err: tagImageFn(ctx, client, source, target)}
	}
}

func (a *App) handleTagDone(msg tagDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		err := docker.FormatUserError(msg.err).Error()
		if f := typedView[*views.TagFormView](a, style.ViewTagForm); f != nil && a.view == style.ViewTagForm {
			f.SetError(err)
		} else {
			a.errFlash = err
		}
		return a, nil
	}
	if a.view == style.ViewTagForm {
		a.popView()
	}
	a.flash = "tagged " + msg.source + " as " + msg.target
	if v := a.viewMap[style.ViewImages]; v != nil {
		return a, v.Refresh()
	}
	return a, nil
}

// confirmPush asks before publishing ref: a push replaces whatever the
// registry holds under that tag.
func (a *App) confirmPush(ref string) {
	if _, _, err := docker.ParseTag(ref); err != nil {
		a.errFlash = err.Error()
		return
	}
	question := "push " + ref + " to " + docker.RegistryForRef(ref) +
		"? the registry's " + tagOf(ref) + " is replaced, with the docker config's credential for it"
	local := a.localRegistryAddr()
	target, err := docker.RetargetRef(ref, local)
	if err != nil || docker.RegistryHost(docker.RegistryForRef(ref)) == docker.RegistryHost(local) {
		a.openConfirm(actPush, ref, question)
		return
	}
	a.openConfirm(actPush, ref, question+" — or L: push to the local registry as "+target)
	a.confirmAlt = &confirmAlt{key: "L", action: actPushLocal, param: ref + "\x00" + target}
}

// tagOf names ref's tag for the confirm: "tag v1", or "latest".
func tagOf(ref string) string {
	_, fam, err := docker.ParseTag(ref)
	if err != nil {
		return "tag"
	}
	if i := strings.LastIndex(fam, ":"); i >= 0 && !strings.Contains(fam[i:], "/") {
		return "tag " + fam[i+1:]
	}
	return "tag"
}

// startPush runs the push in its own goroutine and streams its progress to
// the flash: each event is a message, and handling one waits for the next.
// Progress lines are dropped, never queued, when the UI is behind — only
// the latest matters — but the end always arrives.
func (a *App) startPush(ref string) tea.Cmd { return a.startPushAfter(ref, nil) }

// startPushAfter is startPush with a step run first in the push's
// goroutine — push to local's tag — whose failure is the push's.
func (a *App) startPushAfter(ref string, before func(ctx context.Context, c *docker.Client) error) tea.Cmd {
	ch := make(chan pushEvent, 16)
	client := a.client
	push := pushImageFn
	a.flash = "pushing " + ref + "…"
	go func() {
		defer close(ch)
		ctx, cancel := client.RequestContext(pushTimeout)
		defer cancel()
		if before != nil {
			if err := before(ctx, client); err != nil {
				ch <- pushEvent{done: true, err: err, registry: docker.RegistryForRef(ref)}
				return
			}
		}
		registry, err := push(ctx, client, ref, func(line string) {
			select {
			case ch <- pushEvent{line: line}:
			default:
			}
		})
		ch <- pushEvent{done: true, err: err, registry: registry}
	}()
	return waitPush(ref, ch)
}

func waitPush(ref string, ch <-chan pushEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return pushProgressMsg{ref: ref, ev: ev, ch: ch}
	}
}

func (a *App) handlePushProgress(msg pushProgressMsg) (tea.Model, tea.Cmd) {
	if !msg.ev.done {
		a.flash = "pushing " + msg.ref + " — " + msg.ev.line
		return a, waitPush(msg.ref, msg.ch)
	}
	if msg.ev.err != nil {
		a.flash = ""
		a.errFlash = "push " + msg.ref + " failed — " + docker.FormatUserError(msg.ev.err).Error() +
			localRegistryHint(msg.ev.registry, msg.ev.err)
		return a, nil
	}
	a.flash = "pushed " + msg.ref + " to " + msg.ev.registry
	return a, nil
}

// tagCommand is :tag [ref]: with a reference, tag the selected image as
// it; without, open the tag form on it.
func (a *App) tagCommand(target string) (string, tea.Cmd) {
	act, source := a.activeViewHandleKey("t")
	if act != views.ActTagForm {
		return ":tag needs a selected image — open the images view (1)", nil
	}
	if target == "" {
		_, cmd := a.handleAction(views.ActTagForm, source)
		return "", cmd
	}
	if _, _, err := docker.ParseTag(target); err != nil {
		return err.Error(), nil
	}
	_, cmd := a.handleAction(views.ActTagImage, source+"\x00"+target)
	return "", cmd
}

// pushCommand is :push [ref]: with a reference, push it; without, push the
// selected image's tag.
func (a *App) pushCommand(ref string) (string, tea.Cmd) {
	if ref != "" {
		_, cmd := a.handleAction(views.ActPushImage, ref)
		return "", cmd
	}
	act, param := a.activeViewHandleKey("p")
	if act != views.ActPushImage && act != views.ActNoPushTag {
		return ":push needs a reference or a selected image: :push <repo[:tag]>", nil
	}
	_, cmd := a.handleAction(act, param)
	return "", cmd
}
