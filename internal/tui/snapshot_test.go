// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// snapshotDaemon answers commit, export, save and load as the Engine API
// does, recording each as "METHOD /path?query" and each load's body.
func snapshotDaemon(t *testing.T, loadReply string) (*docker.Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	version := regexp.MustCompile(`^/v[0-9.]+`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		path := version.ReplaceAllString(r.URL.Path, "")
		if path == "/_ping" {
			_, _ = w.Write([]byte("OK"))
			return
		}
		call := r.Method + " " + path
		if r.URL.RawQuery != "" {
			call += "?" + r.URL.RawQuery
		}
		if path == "/images/load" {
			b, _ := io.ReadAll(r.Body)
			call += " body=" + string(b)
		}
		mu.Lock()
		calls = append(calls, call)
		mu.Unlock()
		switch {
		case path == "/commit":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"sha256:0123456789abcdef0123"}`))
		case strings.HasSuffix(path, "/export"):
			_, _ = w.Write([]byte("container-tar"))
		case path == "/images/get":
			_, _ = w.Write([]byte("image-tar"))
		case path == "/images/load":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(loadReply))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := docker.New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

// snapshotApp is the containers view on the sample containers against a
// snapshot daemon, its saves under a temp directory.
func snapshotApp(t *testing.T, loadReply string) (*App, func() []string) {
	t.Helper()
	a := newTestApp(t)
	a.dumpDir = t.TempDir()
	c, calls := snapshotDaemon(t, loadReply)
	a.client = c
	loadContainers(a)
	return a, calls
}

// setPath replaces the tar form's prefilled path with p.
func setPath(t *testing.T, a *App, p string) {
	t.Helper()
	f := typedView[*views.TarFormView](a, style.ViewTarForm)
	if f == nil || a.view != style.ViewTarForm {
		t.Fatalf("not on the tar form: %s", style.ViewName(a.view))
	}
	clearField(a, len([]rune(f.Spec().Path)))
	typeText(a, p)
}

// TestCommitForm: M opens the commit form on the container; enter commits
// with the reference, message and pause typed, and the result is flashed
// back on the containers view.
func TestCommitForm(t *testing.T) {
	a, calls := snapshotApp(t, "")
	step(a, key("M"))
	if a.view != style.ViewCommitForm || !strings.Contains(render(a), "commit web") {
		t.Fatalf("M opened %s:\n%s", style.ViewName(a.view), render(a))
	}
	typeText(a, "snap/web:broken")
	step(a, tab)
	typeText(a, "before removing it")
	runOnce(a, step(a, key("enter")))

	got := strings.Join(calls(), "|")
	for _, want := range []string{
		"POST /commit?", "container=aaaaaaaaaaaa1111", "repo=docker.io%2Fsnap%2Fweb", "tag=broken",
		"comment=before+removing+it",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("commit request %q is missing %q", got, want)
		}
	}
	// The daemon pauses unless told pause=0, as docker commit does.
	if strings.Contains(got, "pause=0") {
		t.Errorf("a paused commit asked %q", got)
	}
	if a.view != style.ViewContainers || a.flash != "committed web → snap/web:broken" {
		t.Errorf("after the commit: view %s, flash %q", style.ViewName(a.view), a.flash)
	}

	// Leave it running: the pause choice flips to no.
	b, calls := snapshotApp(t, "")
	step(b, key("M"))
	step(b, tab)
	step(b, tab)
	step(b, keyRight)
	runOnce(b, step(b, key("enter")))
	if got := strings.Join(calls(), "|"); !strings.Contains(got, "pause=0") {
		t.Errorf("an unpaused commit asked %q", got)
	} else if !strings.Contains(b.flash, "(untagged)") {
		t.Errorf("an untagged commit flashed %q", b.flash)
	}
}

// TestCommitAndLoadAreReadonly: both make an image, so --readonly refuses
// them before any form or request; export only reads, and still opens.
func TestCommitAndLoadAreReadonly(t *testing.T) {
	a, calls := snapshotApp(t, "")
	a.readonly = true
	step(a, key("M"))
	if a.view != style.ViewContainers || !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("M under readonly: view %s, flash %q", style.ViewName(a.view), a.errFlash)
	}
	tarFile := filepath.Join(t.TempDir(), "x.tar")
	if err := os.WriteFile(tarFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.errFlash = ""
	_, cmd := a.dispatchCommand("load " + tarFile)
	runOnce(a, cmd)
	if !strings.Contains(a.errFlash, "readonly") || len(calls()) != 0 {
		t.Errorf(":load under readonly: flash %q, calls %q", a.errFlash, calls())
	}
	step(a, key("E"))
	if a.view != style.ViewTarForm {
		t.Errorf("E under readonly opened %s; export only reads the daemon", style.ViewName(a.view))
	}
}

// TestExportWritesTheTarAndConfirmsAnOverwrite: E prefills a path under
// the state directory; a new file is written at once, 0600; the same
// path again asks first, and no leaves the old file alone.
func TestExportWritesTheTarAndConfirmsAnOverwrite(t *testing.T) {
	a, calls := snapshotApp(t, "")
	step(a, key("E"))
	f := typedView[*views.TarFormView](a, style.ViewTarForm)
	if f == nil || a.view != style.ViewTarForm {
		t.Fatalf("E opened %s", style.ViewName(a.view))
	}
	if p := f.Spec().Path; !strings.Contains(p, filepath.Join("exports", "web-")) || !strings.HasSuffix(p, ".tar") {
		t.Errorf("default path %q, want <state>/exports/web-<time>.tar", p)
	}

	dst := filepath.Join(t.TempDir(), "web.tar")
	setPath(t, a, dst)
	runOnce(a, step(a, key("enter")))
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != "container-tar" {
		t.Fatalf("export wrote %q, %v", b, err)
	}
	if st, _ := os.Stat(dst); st.Mode().Perm() != 0o600 {
		t.Errorf("export is %v, want 0600", st.Mode().Perm())
	}
	if a.view != style.ViewContainers || !strings.Contains(a.flash, "exported web → "+dst) {
		t.Errorf("after the export: view %s, flash %q", style.ViewName(a.view), a.flash)
	}

	if err := os.WriteFile(dst, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	n := len(calls())
	step(a, key("E"))
	setPath(t, a, dst)
	step(a, key("enter"))
	if !a.confirm.Active() || !strings.Contains(render(a), "exists") {
		t.Fatalf("an existing file was not confirmed:\n%s", render(a))
	}
	if len(calls()) != n {
		t.Fatalf("exported before the answer: %q", calls()[n:])
	}
	step(a, key("n"))
	if b, _ := os.ReadFile(dst); string(b) != "keep me" || a.view != style.ViewTarForm {
		t.Fatalf("no replaced the file (%q) or left the form (%s)", b, style.ViewName(a.view))
	}
	step(a, key("enter"))
	runOnce(a, step(a, key("y")))
	if b, _ := os.ReadFile(dst); string(b) != "container-tar" {
		t.Errorf("yes left %q", b)
	}

	// A directory is refused in the form.
	step(a, key("E"))
	setPath(t, a, t.TempDir())
	step(a, key("enter"))
	if a.view != style.ViewTarForm || !strings.Contains(render(a), "is a directory") {
		t.Errorf("a directory was not refused in the form:\n%s", render(a))
	}
}

// TestSaveMarkedImages: E on an image saves it; with images marked it
// saves every marked one into one tar, by ID for an untagged one.
func TestSaveMarkedImages(t *testing.T) {
	a, calls := snapshotApp(t, "")
	loadImages(a)
	step(a, key("space"))
	step(a, key("down"))
	step(a, key("space"))
	step(a, key("E"))
	f := typedView[*views.TarFormView](a, style.ViewTarForm)
	if f == nil || a.view != style.ViewTarForm || f.Title() != "save 2 images" {
		t.Fatalf("E with two marked opened %s", style.ViewName(a.view))
	}
	dst := filepath.Join(t.TempDir(), "imgs.tar")
	setPath(t, a, dst)
	runOnce(a, step(a, key("enter")))
	got := strings.Join(calls(), "|")
	if !strings.Contains(got, "GET /images/get?names=nginx%3A1.27&names=sha256%3Abbbb") {
		t.Errorf("save asked %q", got)
	}
	if b, _ := os.ReadFile(dst); string(b) != "image-tar" {
		t.Errorf("save wrote %q", b)
	}
	if !strings.Contains(a.flash, "saved 2 images") {
		t.Errorf("flash %q", a.flash)
	}

	// :save is E on the row: one image, by its reference.
	b, _ := snapshotApp(t, "")
	loadImages(b)
	b.dispatchCommand("save")
	if f := typedView[*views.TarFormView](b, style.ViewTarForm); f == nil || f.Title() != "save nginx:1.27" {
		t.Errorf(":save opened %s", style.ViewName(b.view))
	}
}

// TestLoadImages: :load sends the file and flashes what the daemon says it
// loaded; an error in the stream is the error.
func TestLoadImages(t *testing.T) {
	src := filepath.Join(t.TempDir(), "in.tar")
	if err := os.WriteFile(src, []byte("tar-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, calls := snapshotApp(
		t,
		`{"stream":"Loaded image: nginx:1.27\n"}`+"\n"+`{"stream":"Loaded image ID: sha256:feed\n"}`,
	)
	_, cmd := a.dispatchCommand("LOAD " + src)
	runOnce(a, cmd)
	if got := strings.Join(calls(), "|"); !strings.Contains(got, "POST /images/load?quiet=1 body=tar-bytes") {
		t.Errorf("load asked %q", got)
	}
	if a.flash != "loaded nginx:1.27, sha256:feed" || a.errFlash != "" {
		t.Errorf("flash %q, error %q", a.flash, a.errFlash)
	}

	b, _ := snapshotApp(t, `{"errorDetail":{"message":"unexpected EOF"},"error":"unexpected EOF"}`)
	_, cmd = b.dispatchCommand("load " + src)
	runOnce(b, cmd)
	if !strings.Contains(b.errFlash, "unexpected EOF") {
		t.Errorf("a failed load flashed %q / %q", b.flash, b.errFlash)
	}

	c, calls := snapshotApp(t, "")
	_, cmd = c.dispatchCommand("load " + filepath.Join(t.TempDir(), "missing.tar"))
	runOnce(c, cmd)
	if c.errFlash == "" || len(calls()) != 0 {
		t.Errorf("a missing file: flash %q, calls %q", c.errFlash, calls())
	}
	if msg, _ := c.dispatchCommand("load"); !strings.Contains(msg, ":load <path>") {
		t.Errorf("bare :load said %q", msg)
	}
}
