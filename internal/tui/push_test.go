// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
)

// fakeTagPush records tags and pushes instead of asking a daemon; a push
// reports two progress lines, then pushErr.
func fakeTagPush(t *testing.T, tagErr, pushErr error) (tags, pushes *[]string) {
	t.Helper()
	tags, pushes = &[]string{}, &[]string{}
	oldTag, oldPush := tagImageFn, pushImageFn
	tagImageFn = func(_ context.Context, _ *docker.Client, source, target string) error {
		*tags = append(*tags, source+" -> "+target)
		return tagErr
	}
	pushImageFn = func(_ context.Context, _ *docker.Client, ref string, progress func(string)) (string, error) {
		*pushes = append(*pushes, ref)
		progress("0/2 layers")
		progress("1/2 layers · 1MB/2MB")
		return docker.RegistryForRef(ref), pushErr
	}
	t.Cleanup(func() { tagImageFn, pushImageFn = oldTag, oldPush })
	return tags, pushes
}

// drainPush runs a push's command chain to the end, as the runtime would:
// each progress message is handled and the next one waited for. It returns
// every flash shown along the way.
func drainPush(a *App, cmd tea.Cmd) []string {
	var flashes []string
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			return flashes
		}
		if b, ok := msg.(tea.BatchMsg); ok {
			cmd = nil
			for _, c := range b {
				if c != nil {
					cmd = c
				}
			}
			continue
		}
		cmd = step(a, msg)
		flashes = append(flashes, a.flash+a.errFlash)
		if _, ok := msg.(pushProgressMsg); !ok {
			return flashes
		}
	}
	return flashes
}

// tagOnce runs a tag's command and hands its result to the app, without
// the images refresh that follows — there is no daemon to list.
func tagOnce(a *App, cmd tea.Cmd) {
	if cmd != nil {
		step(a, cmd())
	}
}

// TestTagForm: t on an image opens the tag form on its reference; a bad
// reference is refused in the form; a good one tags it, closes the form
// and says so.
func TestTagForm(t *testing.T) {
	tags, _ := fakeTagPush(t, nil, nil)
	a := newTestApp(t)
	loadImages(a)
	step(a, key("t"))
	if a.view != style.ViewTagForm || !strings.Contains(render(a), "tag nginx:1.27") {
		t.Fatalf("t opened %s:\n%s", style.ViewName(a.view), render(a))
	}
	typeText(a, "::")
	step(a, key("enter"))
	if a.view != style.ViewTagForm || !strings.Contains(render(a), "not a valid repo[:tag]") {
		t.Fatalf("a bad reference was not refused in the form:\n%s", render(a))
	}
	for range len("nginx:1.27::") {
		step(a, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(a, "ghcr.io/me/nginx:v1")
	tagOnce(a, step(a, key("enter")))
	if len(*tags) != 1 || (*tags)[0] != "nginx:1.27 -> ghcr.io/me/nginx:v1" {
		t.Errorf("tags %q", *tags)
	}
	if a.view != style.ViewImages || a.flash != "tagged nginx:1.27 as ghcr.io/me/nginx:v1" {
		t.Errorf("after tag: view %s, flash %q, err %q", style.ViewName(a.view), a.flash, a.errFlash)
	}
}

// TestTagRefusedStaysInTheForm: the daemon's refusal lands in the form.
func TestTagRefusedStaysInTheForm(t *testing.T) {
	fakeTagPush(t, errors.New("No such image: nginx:1.27"), nil)
	a := newTestApp(t)
	loadImages(a)
	step(a, key("t"))
	step(a, tea.KeyPressMsg{Code: tea.KeyBackspace})
	typeText(a, "8")
	tagOnce(a, step(a, key("enter")))
	if a.view != style.ViewTagForm || !strings.Contains(render(a), "No such image") {
		t.Errorf("refusal not in the form: view %s\n%s", style.ViewName(a.view), render(a))
	}
}

// TestTagCommand: :tag <ref> tags the selected image without the form;
// :tag alone opens the form; off the images view it says why.
func TestTagCommand(t *testing.T) {
	tags, _ := fakeTagPush(t, nil, nil)
	a := newTestApp(t)
	if msg, _ := a.dispatchCommand("tag app:v1"); !strings.Contains(msg, "needs a selected image") {
		t.Errorf(":tag off images: %q", msg)
	}
	loadImages(a)
	_, cmd := a.dispatchCommand("tag app:RC1")
	tagOnce(a, cmd)
	if len(*tags) != 1 || (*tags)[0] != "nginx:1.27 -> app:RC1" {
		t.Errorf("tags %q", *tags)
	}
	_, cmd = a.dispatchCommand("tag")
	runCmd(a, cmd)
	if a.view != style.ViewTagForm {
		t.Errorf(":tag alone opened %s", style.ViewName(a.view))
	}
}

// TestPushStreamsProgress: p confirms naming the reference and registry,
// pushes only on yes, and streams progress to the flash until it is done.
func TestPushStreamsProgress(t *testing.T) {
	_, pushes := fakeTagPush(t, nil, nil)
	a := newTestApp(t)
	loadImages(a)
	step(a, key("p"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "push nginx:1.27 to "+docker.DockerHubServer) {
		t.Fatalf("p asked %q (active %v, err %q)", a.confirm.Prompt(), a.confirm.Active(), a.errFlash)
	}
	if len(*pushes) != 0 {
		t.Fatal("pushed before the confirm was answered")
	}
	flashes := drainPush(a, step(a, key("y")))
	if len(*pushes) != 1 || (*pushes)[0] != "nginx:1.27" {
		t.Errorf("pushes %q", *pushes)
	}
	joined := strings.Join(flashes, "\n")
	if !strings.Contains(joined, "pushing nginx:1.27 — 1/2 layers · 1MB/2MB") {
		t.Errorf("progress never reached the flash:\n%s", joined)
	}
	if a.flash != "pushed nginx:1.27 to "+docker.DockerHubServer || a.errFlash != "" {
		t.Errorf("flash %q, err %q", a.flash, a.errFlash)
	}
}

// TestPushFailure: a refused push says so in the error flash.
func TestPushFailure(t *testing.T) {
	fakeTagPush(t, nil, errors.New("denied: requested access to the resource is denied"))
	a := newTestApp(t)
	_, cmd := a.dispatchCommand("push ghcr.io/me/app:v1")
	runCmd(a, cmd)
	if !strings.Contains(a.confirm.Prompt(), "push ghcr.io/me/app:v1 to ghcr.io?") {
		t.Fatalf(":push asked %q", a.confirm.Prompt())
	}
	drainPush(a, step(a, key("y")))
	if !strings.Contains(a.errFlash, "push ghcr.io/me/app:v1 failed") || !strings.Contains(a.errFlash, "denied") {
		t.Errorf("err %q", a.errFlash)
	}
}

// TestPushUntaggedImage: an image with no tag has nothing to push, and p
// says how to give it one.
func TestPushUntaggedImage(t *testing.T) {
	_, pushes := fakeTagPush(t, nil, nil)
	a := newTestApp(t)
	loadImages(a)
	step(a, key("j"))
	step(a, key("p"))
	if a.confirm.Active() || !strings.Contains(a.errFlash, "no tag to push") || len(*pushes) != 0 {
		t.Errorf("confirm %v, err %q, pushes %v", a.confirm.Active(), a.errFlash, *pushes)
	}
}

// TestTagPushReadonly: --readonly refuses the tag form, :tag and the push.
func TestTagPushReadonly(t *testing.T) {
	tags, pushes := fakeTagPush(t, nil, nil)
	a := newTestApp(t)
	loadImages(a)
	a.readonly = true
	for _, k := range []string{"t", "p"} {
		a.errFlash = ""
		step(a, key(k))
		if a.view != style.ViewImages || a.confirm.Active() || !strings.Contains(a.errFlash, "readonly mode") {
			t.Errorf("%s: view %s, confirm %v, err %q", k, style.ViewName(a.view), a.confirm.Active(), a.errFlash)
		}
	}
	_, cmd := a.dispatchCommand("tag app:v1")
	runCmd(a, cmd)
	_, cmd = a.dispatchCommand("push app:v1")
	runCmd(a, cmd)
	if len(*tags)+len(*pushes) != 0 || a.confirm.Active() {
		t.Errorf("ran anyway: %v %v", *tags, *pushes)
	}
}
