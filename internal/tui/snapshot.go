// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/tui/style"
	"github.com/blairham/dockmaster/internal/tui/views"
)

// The snapshot actions and commands (#108).
const (
	actCommitForm = "commit_form"
	actExportForm = "export_form"
	actSaveForm   = "save_form"

	cmdLoad   = "load"
	cmdCommit = "commit"
	cmdExport = "export"
	cmdSave   = "save"
)

// snapshotTimeout bounds a commit, export, save or load (#108). Each moves
// a whole filesystem or image set, so the 60 s of a lifecycle call would
// cut off any real one; --request-timeout still overrides it.
const snapshotTimeout = 30 * time.Minute

// commitDoneMsg reports a finished commit, or why it failed.
type commitDoneMsg struct {
	err   error
	image string // the new image's ID
	spec  views.CommitSpec
}

// tarDoneMsg reports a finished export or save, or why it failed.
type tarDoneMsg struct {
	err  error
	path string
	spec views.TarSpec
	size int64
}

// loadDoneMsg reports a finished load: what the daemon loaded, or why it
// failed.
type loadDoneMsg struct {
	err    error
	path   string
	loaded []string
}

// openCommitForm opens docker commit for a container.
func (a *App) openCommitForm(id string) (tea.Model, tea.Cmd) {
	a.setView(style.ViewCommitForm, views.NewCommitForm(id, a.containerName(id)))
	a.pushView(style.ViewCommitForm)
	return a, nil
}

// runCommit commits in the background; the form stays up until it is
// done, so a bad reference lands in it to be fixed.
func (a *App) runCommit(spec views.CommitSpec) tea.Cmd {
	a.flash = "committing " + spec.Name + "…"
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(snapshotTimeout)
		defer cancel()
		id, err := client.Commit(ctx, spec.ID, docker.CommitOptions{
			Ref:     spec.Ref,
			Message: spec.Message,
			Pause:   spec.Pause,
		})
		return commitDoneMsg{spec: spec, image: id, err: err}
	}
}

// handleCommitDone reports the commit: in the form when it failed, and on
// success back where the form was opened, with the image's name.
func (a *App) handleCommitDone(msg commitDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.formError(style.ViewCommitForm, docker.FormatUserError(msg.err).Error())
		return a, nil
	}
	if a.view == style.ViewCommitForm {
		a.popView()
	}
	image := msg.spec.Ref
	if image == "" {
		image = shortImageID(msg.image) + " (untagged)"
	}
	a.flash = "committed " + msg.spec.Name + " → " + image
	cmd := a.refreshImages()
	return a, cmd
}

// openTarForm opens the export or save form, its path prefilled with a
// fresh file under the state directory.
func (a *App) openTarForm(kind string, ids []string, name string) (tea.Model, tea.Cmd) {
	path, err := a.defaultTarPath(name)
	if err != nil {
		a.errFlash = err.Error()
		return a, nil
	}
	a.setView(style.ViewTarForm, views.NewTarForm(views.TarSpec{Kind: kind, IDs: ids, Name: name, Path: path}))
	a.pushView(style.ViewTarForm)
	return a, nil
}

// unsafeFileChars are what a default file name leaves out of a container
// or image name: a reference's / and :, and anything a shell would quote.
var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// defaultTarPath is <state>/exports/<name>-<time>.tar — beside the dumps
// and saved logs, so screenDumpDir moves it with them — written with ~
// for the home directory, as the user would type it.
func (a *App) defaultTarPath(name string) (string, error) {
	root, err := a.dumpRoot()
	if err != nil {
		return "", err
	}
	base := strings.Trim(unsafeFileChars.ReplaceAllString(name, "_"), "_.")
	if base == "" {
		base = "tar"
	}
	p := filepath.Join(root, "exports", base+"-"+time.Now().Format("20060102-150405")+".tar")
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
			p = "~/" + rest
		}
	}
	return p, nil
}

// writeTar checks where an export or save is going: a directory is
// refused in the form, an existing file is replaced only after a yes, and
// anything else starts writing.
func (a *App) writeTar(spec views.TarSpec) tea.Cmd {
	path, err := docker.LocalPath(spec.Path)
	if err != nil {
		a.formError(style.ViewTarForm, "file: "+err.Error())
		return nil
	}
	st, err := os.Stat(path)
	switch {
	case err == nil && st.IsDir():
		a.formError(style.ViewTarForm, "file: "+path+" is a directory — name a file in it")
		return nil
	case err == nil:
		a.confirmDispatch = func(yes bool) (string, tea.Cmd) {
			if !yes {
				return "", nil
			}
			return "", a.runTar(spec, path)
		}
		a.ask("Overwrite", fmt.Sprintf("%s exists — replace it with %s?", path, tarWhat(spec)))
		return nil
	}
	return a.runTar(spec, path)
}

// tarWhat names what a tar holds, for the confirm.
func tarWhat(spec views.TarSpec) string {
	if spec.Kind == views.TarSave {
		return "the image save of " + spec.Name
	}
	return spec.Name + "'s filesystem"
}

// runTar writes the export or save in the background.
func (a *App) runTar(spec views.TarSpec, path string) tea.Cmd {
	a.flash = "exporting " + spec.Name + "…"
	if spec.Kind == views.TarSave {
		a.flash = "saving " + spec.Name + "…"
	}
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(snapshotTimeout)
		defer cancel()
		var n int64
		var err error
		switch {
		case spec.Kind == views.TarSave:
			n, err = client.SaveImages(ctx, spec.IDs, path)
		case len(spec.IDs) == 1:
			n, err = client.ExportContainer(ctx, spec.IDs[0], path)
		default:
			err = fmt.Errorf("export takes one container, not %d", len(spec.IDs))
		}
		return tarDoneMsg{spec: spec, path: path, size: n, err: err}
	}
}

// handleTarDone reports the export or save: in the form when it failed,
// and on success back where the form was opened, with where it went.
func (a *App) handleTarDone(msg tarDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.formError(style.ViewTarForm, docker.FormatUserError(msg.err).Error())
		return a, nil
	}
	if a.view == style.ViewTarForm {
		a.popView()
	}
	verb := "exported"
	if msg.spec.Kind == views.TarSave {
		verb = "saved"
	}
	a.flash = fmt.Sprintf("%s %s → %s (%s)", verb, msg.spec.Name, msg.path, docker.HumanSize(msg.size))
	return a, nil
}

// loadImages is :load <path>: docker load from a tar on this machine.
func (a *App) loadImages(param string) tea.Cmd {
	path, err := docker.LocalPath(param)
	if err != nil {
		a.errFlash = ":load needs a file — " + err.Error()
		return nil
	}
	if st, err := os.Stat(path); err != nil {
		a.errFlash = "load: " + err.Error()
		return nil
	} else if st.IsDir() {
		a.errFlash = "load: " + path + " is a directory — name the tar in it"
		return nil
	}
	a.flash = "loading " + filepath.Base(path) + "…"
	client := a.client
	return func() tea.Msg {
		ctx, cancel := client.RequestContext(snapshotTimeout)
		defer cancel()
		loaded, err := client.LoadImages(ctx, path)
		return loadDoneMsg{path: path, loaded: loaded, err: err}
	}
}

// handleLoadDone reports what a load brought in and refreshes the images.
func (a *App) handleLoadDone(msg loadDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.errFlash = docker.FormatUserError(msg.err).Error()
		return a, nil
	}
	switch len(msg.loaded) {
	case 0:
		a.flash = "loaded " + filepath.Base(msg.path)
	case 1, 2, 3:
		a.flash = "loaded " + strings.Join(msg.loaded, ", ")
	default:
		a.flash = fmt.Sprintf("loaded %d images: %s, …", len(msg.loaded), strings.Join(msg.loaded[:3], ", "))
	}
	cmd := a.refreshImages()
	return a, cmd
}

// formError puts a failure in the form vt when it is on screen, else in
// the flash.
func (a *App) formError(vt style.ViewType, msg string) {
	if a.view != vt {
		a.errFlash = msg
		return
	}
	switch f := a.activeView().(type) {
	case *views.CommitFormView:
		f.SetError(msg)
	case *views.TarFormView:
		f.SetError(msg)
	default:
		a.errFlash = msg
	}
}

// refreshImages refetches the images view, if it has been opened, so a
// new image is there when it is next shown.
func (a *App) refreshImages() tea.Cmd {
	if v := a.viewMap[style.ViewImages]; v != nil {
		return v.Refresh()
	}
	return nil
}

// shortImageID is an image ID as docker images shows it.
func shortImageID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
