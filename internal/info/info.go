// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package info is `dockmaster info` (#60), k9s's `k9s info`: where
// dockmaster reads its config and writes its state, and the docker endpoint
// it would dial and why — without dialing it, so it answers with no daemon
// at all. It prints paths and the endpoint only: never an environment
// value beyond the endpoint, and never anything from inside a file.
package info

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/blairham/dockmaster/internal/applog"
	"github.com/blairham/dockmaster/internal/config"
	"github.com/blairham/dockmaster/internal/docker"
	"github.com/blairham/dockmaster/internal/version"
)

// Options are the flags info shares with dockmaster itself, so it reports
// what that same command line would do.
type Options struct {
	Host    string
	Context string
	LogFile string
}

// Command runs `dockmaster info [--host h] [--context c] [--log-file f]`.
func Command(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	fs.SetOutput(out)
	var o Options
	fs.StringVar(&o.Host, "host", "", "the endpoint dockmaster --host would dial")
	fs.StringVar(&o.Context, "context", "", "the docker context dockmaster --context would use")
	fs.StringVar(&o.LogFile, "log-file", "", "the log file dockmaster --log-file would write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: dockmaster info [--host h] [--context c] [--log-file f]")
	}
	return Write(out, o)
}

// Report is everything info prints.
type Report struct {
	Version, Commit, Date string

	ConfigDir, ConfigFile, SkinsDir string
	ConfigNote                      string

	StateDir, Dumps, SavedLogs, LogFile, History string

	Context, Endpoint, Resolved string
}

// Gather builds the report. A config.yaml that does not parse is reported
// rather than fatal: info is what one runs to find out why.
func Gather(o Options) (Report, error) {
	r := Report{Version: version.Version, Commit: version.Commit, Date: version.Date}
	cfgDir, err := config.Dir()
	if err != nil {
		return r, err
	}
	r.ConfigDir = cfgDir
	r.ConfigFile = filepath.Join(cfgDir, config.FileName)
	r.SkinsDir = filepath.Join(cfgDir, config.SkinsDirName)
	cfg, err := config.Load(r.ConfigFile)
	switch {
	case err != nil:
		r.ConfigNote = "does not load: " + err.Error()
		cfg = config.Default()
	case !exists(r.ConfigFile):
		r.ConfigNote = "not present — the defaults apply; `dockmaster config init` writes one"
	}

	state, err := config.StateDir()
	if err != nil {
		return r, err
	}
	r.StateDir = state
	root := state
	if cfg.ScreenDumpDir != "" {
		root = config.ExpandHome(cfg.ScreenDumpDir)
	}
	r.Dumps = filepath.Join(root, "dumps")
	r.SavedLogs = filepath.Join(root, "logs")
	r.History = filepath.Join(state, "history.json")
	if r.LogFile, err = applog.Path(o.LogFile); err != nil {
		return r, err
	}

	r.Context, r.Endpoint, r.Resolved = Endpoint(o, cfg.Context)
	r.Endpoint = redact(r.Endpoint)
	return r, nil
}

// Endpoint is the context, endpoint and how they were chosen, by
// dockmaster's own precedence — main's, then docker.ResolveHost's:
// --host, $DOCKER_HOST, --context, $DOCKER_CONTEXT, config.yaml's context,
// the docker CLI's currentContext, the default socket. It reads the context
// store and never dials.
func Endpoint(o Options, cfgContext string) (name, host, how string) {
	switch {
	case o.Host != "":
		return "", o.Host, "explicit (--host)"
	case o.Context != "":
		return lookup(o.Context, "explicit (--context)")
	case os.Getenv("DOCKER_HOST") != "":
		return "", os.Getenv("DOCKER_HOST"), "env ($DOCKER_HOST)"
	case os.Getenv("DOCKER_CONTEXT") != "":
		return lookup(os.Getenv("DOCKER_CONTEXT"), "env ($DOCKER_CONTEXT)")
	case cfgContext != "":
		return lookup(cfgContext, "context (config.yaml)")
	}
	if _, ctx := docker.ResolveHost(""); ctx != "default" {
		return lookup(ctx, "context (the docker CLI's current context)")
	}
	return lookup("default", "default")
}

// lookup finds name in the context store.
func lookup(name, how string) (string, string, string) {
	for _, c := range docker.Contexts() {
		if c.Name == name {
			return name, c.Host, how
		}
	}
	return name, "", how + " — no such context in the store"
}

// redact drops a password from an endpoint URL; the rest of it is what
// info is for.
func redact(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.User == nil {
		return endpoint
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), "xxxxx")
		return u.String()
	}
	return endpoint
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Write prints the report.
func Write(out io.Writer, o Options) error {
	r, err := Gather(o)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, r.String())
	return err
}

// String is the report as info prints it: one "label: value" per line.
func (r Report) String() string {
	ctx := r.Context
	if ctx == "" {
		ctx = "(none — an endpoint, not a context)"
	}
	endpoint := r.Endpoint
	if endpoint == "" {
		endpoint = "(unknown)"
	}
	cfgFile := r.ConfigFile
	if r.ConfigNote != "" {
		cfgFile += " (" + r.ConfigNote + ")"
	}
	rows := [][2]string{
		{"Version", fmt.Sprintf("%s (%s, built %s)", r.Version, r.Commit, r.Date)},
		{"Config dir", r.ConfigDir},
		{"Config file", cfgFile},
		{"Skins dir", r.SkinsDir},
		{"State dir", r.StateDir},
		{"Dumps", r.Dumps},
		{"Saved logs", r.SavedLogs},
		{"Log file", r.LogFile},
		{"History", r.History},
		{"Context", ctx},
		{"Endpoint", endpoint},
		{"Resolved by", r.Resolved},
	}
	var b strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&b, "%-12s %s\n", row[0]+":", row[1])
	}
	return b.String()
}
