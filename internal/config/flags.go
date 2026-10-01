package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// FlagValues are the parsed values of the flags yaml also sets.
type FlagValues struct {
	Command        string
	RequestTimeout time.Duration
	Refresh        int
	ReadOnly       bool
	ShowAll        bool
	NoStats        bool
	Logoless       bool
	Splashless     bool
	Headless       bool
	Crumbsless     bool
}

// SetFlags names the flags given on fs's command line — only those, so a
// flag left at its default never overwrites the file.
func SetFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// ApplyFlags lays the flags that were set over cfg. A flag left at its
// default does not touch the file's value — otherwise every bool in
// yaml would be overwritten by an unset flag's false.
func ApplyFlags(cfg Config, set map[string]bool, v FlagValues) Config {
	if set["c"] || set["command"] {
		cfg.DefaultView = v.Command
	}
	if set["r"] || set["refresh"] {
		cfg.RefreshRate = v.Refresh
	}
	if set["request-timeout"] {
		cfg.RequestTimeout = v.RequestTimeout
	}
	bools := []struct {
		dst  *bool
		name string
		val  bool
	}{
		{dst: &cfg.ReadOnly, name: "readonly", val: v.ReadOnly},
		{dst: &cfg.ShowAll, name: "all", val: v.ShowAll},
		{dst: &cfg.NoStats, name: "no-stats", val: v.NoStats},
		{dst: &cfg.UI.Logoless, name: "logoless", val: v.Logoless},
		{dst: &cfg.UI.Splashless, name: "splashless", val: v.Splashless},
		{dst: &cfg.UI.Headless, name: "headless", val: v.Headless},
		{dst: &cfg.UI.Crumbsless, name: "crumbsless", val: v.Crumbsless},
	}
	for _, b := range bools {
		if set[b.name] {
			*b.dst = b.val
		}
	}
	return cfg
}

// Command runs `dockmaster config path|init`; args follow "config".
func Command(args []string, out io.Writer) error {
	path, err := Path()
	if err != nil {
		return err
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "path":
		_, err := fmt.Fprintln(out, path)
		return err
	case "init":
		return writeSample(path, out)
	}
	return errors.New("usage: dockmaster config path|init")
}

// writeSample writes the commented defaults, refusing to replace a file
// that is already there.
func writeSample(path string, out io.Writer) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists — not overwriting it", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(Sample), 0o600); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "wrote %s\n", path)
	return err
}
