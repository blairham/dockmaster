// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// fakeRegistries stands in for the docker config and its helpers: the
// listing, a login and a logout, none of which touches the user's config
// or keychain. It returns the logins and logouts asked for.
func fakeRegistries(t *testing.T, loginErr error) (logins, logouts *[]string) {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	rows := []docker.RegistryAuth{
		{Server: "ghcr.io", Helper: "osxkeychain", Username: "octo", LoggedIn: true},
		{Server: docker.DockerHubServer},
	}
	logins, logouts = &[]string{}, &[]string{}
	oldList, oldIn, oldOut := views.ListRegistries, registryLoginFn, registryLogoutFn
	views.ListRegistries = func(context.Context) ([]docker.RegistryAuth, error) {
		return append([]docker.RegistryAuth(nil), rows...), nil
	}
	registryLoginFn = func(_ context.Context, _ *docker.Client, server, user, pass string) (string, error) {
		*logins = append(*logins, server+" "+user+" "+pass)
		return docker.NormalizeRegistry(server), loginErr
	}
	registryLogoutFn = func(_ context.Context, server string) error {
		*logouts = append(*logouts, server)
		rows = rows[1:]
		return nil
	}
	t.Cleanup(func() { views.ListRegistries, registryLoginFn, registryLogoutFn = oldList, oldIn, oldOut })
	return logins, logouts
}

// TestRegistriesView: :reg lists each registry with where its credential
// lives and whether it resolves.
func TestRegistriesView(t *testing.T) {
	fakeRegistries(t, nil)
	a := newTestApp(t)
	_, cmd := a.dispatchCommand("reg")
	runCmd(a, cmd)
	if a.view != style.ViewRegistries {
		t.Fatalf(":reg opened %s", style.ViewName(a.view))
	}
	out := render(a)
	for _, want := range []string{"ghcr.io", "octo", "osxkeychain", "logged in", "index.docker.io", "config.json", "not logged in"} {
		if !strings.Contains(out, want) {
			t.Errorf("registries view is missing %q:\n%s", want, out)
		}
	}
}

// TestRegistryLogout: ctrl-d confirms naming the registry, logs out only
// on yes, and refreshes the list.
func TestRegistryLogout(t *testing.T) {
	_, logouts := fakeRegistries(t, nil)
	a := newTestApp(t)
	_, cmd := a.dispatchCommand("registries")
	runCmd(a, cmd)
	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "log out of ghcr.io?") {
		t.Fatalf("ctrl-d asked %q (active %v, err %q)", a.confirm.Prompt(), a.confirm.Active(), a.errFlash)
	}
	if len(*logouts) != 0 {
		t.Fatal("logged out before the confirm was answered")
	}
	runCmd(a, step(a, key("y")))
	if len(*logouts) != 1 || (*logouts)[0] != "ghcr.io" {
		t.Errorf("logouts %v", *logouts)
	}
	if a.flash != "logged out of ghcr.io" || a.errFlash != "" {
		t.Errorf("flash %q, err %q", a.flash, a.errFlash)
	}
	if rv := typedView[*views.RegistriesView](a, style.ViewRegistries); rv.Count() != 1 {
		t.Errorf("the list was not refreshed: %d rows\n%s", rv.Count(), render(a))
	}
}

// TestRegistryLogin: :login <registry> opens the form on it; the password
// is masked on screen; enter hands the credentials to the login, closes
// the form and refreshes the list behind it.
func TestRegistryLogin(t *testing.T) {
	logins, _ := fakeRegistries(t, nil)
	a := newTestApp(t)
	_, cmd := a.dispatchCommand("registries")
	runCmd(a, cmd)
	_, cmd = a.dispatchCommand("login GHCR.io")
	runCmd(a, cmd)
	if a.view != style.ViewLoginForm || !strings.Contains(render(a), "login ghcr.io") {
		t.Fatalf(":login opened %s:\n%s", style.ViewName(a.view), render(a))
	}
	typeText(a, "octo")
	step(a, key("enter"))
	if a.view != style.ViewLoginForm || !strings.Contains(render(a), "password: required") {
		t.Fatalf("an empty password was not refused in the form:\n%s", render(a))
	}
	typeText(a, "s3cret pat")
	if strings.Contains(render(a), "s3cret") {
		t.Errorf("the password is on screen:\n%s", render(a))
	}
	runCmd(a, step(a, key("enter")))
	if len(*logins) != 1 || (*logins)[0] != "ghcr.io octo s3cret pat" {
		t.Errorf("logins %q", *logins)
	}
	if a.view != style.ViewRegistries || a.flash != "logged in to ghcr.io" {
		t.Errorf("after login: view %s, flash %q, err %q", style.ViewName(a.view), a.flash, a.errFlash)
	}
}

// TestRegistryLoginRefused: a refused login stays in the form with the
// reason, so it can be fixed and sent again.
func TestRegistryLoginRefused(t *testing.T) {
	fakeRegistries(t, errors.New("unauthorized: incorrect username or password"))
	a := newTestApp(t)
	_, cmd := a.dispatchCommand("login")
	runCmd(a, cmd)
	if !strings.Contains(render(a), "login "+docker.DockerHubServer) {
		t.Fatalf(":login does not default to Docker Hub:\n%s", render(a))
	}
	typeText(a, "octo")
	step(a, tab)
	typeText(a, "wrong")
	runCmd(a, step(a, key("enter")))
	if a.view != style.ViewLoginForm || !strings.Contains(render(a), "incorrect username or password") {
		t.Errorf("the refusal is not in the form: view %s\n%s", style.ViewName(a.view), render(a))
	}
}

// TestRegistryReadonly: --readonly refuses the login form and the logout,
// and neither runs.
func TestRegistryReadonly(t *testing.T) {
	logins, logouts := fakeRegistries(t, nil)
	a := newTestApp(t)
	a.readonly = true
	_, cmd := a.dispatchCommand("registries")
	runCmd(a, cmd)
	runCmd(a, step(a, key("ctrl+d")))
	if a.confirm.Active() || !strings.Contains(a.errFlash, "readonly mode — registry logout refused") {
		t.Errorf("ctrl-d: confirm %v, err %q", a.confirm.Active(), a.errFlash)
	}
	_, cmd = a.dispatchCommand("login ghcr.io")
	runCmd(a, cmd)
	if a.view == style.ViewLoginForm || !strings.Contains(a.errFlash, "readonly mode") {
		t.Errorf(":login: view %s, err %q", style.ViewName(a.view), a.errFlash)
	}
	if len(*logins)+len(*logouts) != 0 {
		t.Errorf("ran anyway: %v %v", *logins, *logouts)
	}
}
