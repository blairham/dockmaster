// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// composeTree is a checkout: shop/ holds a compose file and a README, and a
// hidden directory that a listing leaves out.
func composeTree(t *testing.T) (root, shop, file string) {
	t.Helper()
	root = t.TempDir()
	shop = filepath.Join(root, "shop")
	for _, d := range []string{shop, filepath.Join(root, ".git")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	file = filepath.Join(shop, "compose.yaml")
	for name, body := range map[string]string{file: "services: {}\n", filepath.Join(shop, "README.md"): "hi\n"} {
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, shop, file
}

// openDir runs :dir path.
func openDir(t *testing.T, a *App, dir string) *views.DirView {
	t.Helper()
	errMsg, cmd := a.dispatchCommand("dir " + dir)
	if errMsg != "" {
		t.Fatalf(":dir: %s", errMsg)
	}
	runCmd(a, cmd)
	if a.view != style.ViewDir {
		t.Fatalf(":dir opened %v", a.view)
	}
	return typedView[*views.DirView](a, style.ViewDir)
}

// TestDirBrowse: :dir lists a directory — subdirectories first, hidden
// entries left out — enter walks in, esc walks back up (#15).
func TestDirBrowse(t *testing.T) {
	root, shop, _ := composeTree(t)
	a := newTestApp(t)
	dv := openDir(t, a, root)
	if out := render(a); !strings.Contains(out, "shop/") || strings.Contains(out, ".git") {
		t.Errorf("root listing:\n%s", out)
	}
	runCmd(a, step(a, key("enter")))
	if dv.Dir() != shop {
		t.Fatalf("enter on shop/ went to %s", dv.Dir())
	}
	out := render(a)
	if ci, ri := strings.Index(out, "compose.yaml"), strings.Index(out, "README.md"); ci < 0 || ri < 0 || ci > ri {
		t.Errorf("the compose file is not listed before the other files:\n%s", out)
	}
	runCmd(a, step(a, key("esc")))
	if a.view != style.ViewDir || dv.Dir() != root {
		t.Errorf("esc went to %v %s, want back up to %s", a.view, dv.Dir(), root)
	}
}

// TestDirComposeUp: u on a compose file runs compose up from the file — no
// project name, which a project that never ran does not have; compose
// derives it — and u on anything else says what a compose file is.
func TestDirComposeUp(t *testing.T) {
	_, shop, file := composeTree(t)
	a := newTestApp(t)
	f := &fakeCompose{}
	a.composeRunner = f.run
	openDir(t, a, shop)
	runCmd(a, step(a, key("u")))
	want := "compose --project-directory " + shop + " -f " + file + " up -d"
	if got := f.last(); got != want {
		t.Errorf("compose ran %q, want %q", got, want)
	}

	step(a, key("j")) // README.md
	step(a, key("u"))
	if !strings.Contains(a.errFlash, "not a compose file") {
		t.Errorf("u on README.md: err %q", a.errFlash)
	}
}

// TestDirComposeEdit: e opens the file in $EDITOR and brings it up only
// when it comes back changed.
func TestDirComposeEdit(t *testing.T) {
	_, shop, file := composeTree(t)
	a := newTestApp(t)
	f := &fakeCompose{}
	a.composeRunner = f.run
	fakeEditor(t, a, `echo "  web: {image: nginx}" >> "$1"`)
	openDir(t, a, shop)
	runCmd(a, step(a, key("e")))
	if got := f.last(); !strings.HasSuffix(got, "-f "+file+" up -d") {
		t.Errorf("after a changed edit compose ran %q", got)
	}
}

// TestDirReadonly: --readonly refuses bringing a project up from :dir.
func TestDirReadonly(t *testing.T) {
	_, shop, _ := composeTree(t)
	a := newTestApp(t)
	a.readonly = true
	f := &fakeCompose{}
	a.composeRunner = f.run
	openDir(t, a, shop)
	runCmd(a, step(a, key("u")))
	if f.last() != "" || !strings.Contains(a.errFlash, "readonly") {
		t.Errorf("readonly: compose ran %q, err %q", f.last(), a.errFlash)
	}
}

func TestIsComposeFile(t *testing.T) {
	for name, want := range map[string]bool{
		"compose.yaml": true, "compose.yml": true, "docker-compose.yml": true, "docker-compose.yaml": true,
		"compose.override.yaml": true, "docker-compose.prod.yml": true,
		"compose.json": false, "mycompose.yaml": false, "README.md": false, "compose.yaml.bak": false,
	} {
		if got := views.IsComposeFile(name); got != want {
			t.Errorf("IsComposeFile(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestDirComposeDown: ctrl-d on a compose file confirms, naming the file,
// then runs compose down from it — no project name, as up has none — and a
// "no" runs nothing. On anything else it says what a compose file is, as u
// and e do, rather than paging the cursor.
func TestDirComposeDown(t *testing.T) {
	_, shop, file := composeTree(t)
	a := newTestApp(t)
	f := &fakeCompose{}
	a.composeRunner = f.run
	openDir(t, a, shop)

	step(a, key("ctrl+d"))
	if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), "compose down "+file+"?") {
		t.Fatalf("ctrl-d asked %q (active %v, err %q)", a.confirm.Prompt(), a.confirm.Active(), a.errFlash)
	}
	runCmd(a, step(a, key("n")))
	if f.last() != "" {
		t.Fatalf("compose ran %q after no", f.last())
	}

	step(a, key("ctrl+d"))
	runCmd(a, step(a, key("y")))
	want := "compose --project-directory " + shop + " -f " + file + " down"
	if got := f.last(); got != want {
		t.Errorf("compose ran %q, want %q", got, want)
	}
	if a.flash != "took down shop/compose.yaml" {
		t.Errorf("flash %q", a.flash)
	}

	step(a, key("j")) // README.md
	step(a, key("ctrl+d"))
	if a.confirm.Active() || !strings.Contains(a.errFlash, "not a compose file") {
		t.Errorf("ctrl-d on README.md: confirm %v, err %q", a.confirm.Active(), a.errFlash)
	}
}

// TestDirComposeDownReadonly: --readonly refuses compose down from :dir
// before it asks.
func TestDirComposeDownReadonly(t *testing.T) {
	_, shop, _ := composeTree(t)
	a := newTestApp(t)
	a.readonly = true
	f := &fakeCompose{}
	a.composeRunner = f.run
	openDir(t, a, shop)
	runCmd(a, step(a, key("ctrl+d")))
	if a.confirm.Active() || f.last() != "" || !strings.Contains(a.errFlash, "readonly mode — compose down file refused") {
		t.Errorf("readonly: confirm %v, compose ran %q, err %q", a.confirm.Active(), f.last(), a.errFlash)
	}
}
