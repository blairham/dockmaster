// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// The Docker CLI's daemon endpoint does not come from the environment
// alone — it comes from the *context store*, a directory of JSON metadata
// under ~/.docker/contexts keyed by the SHA-256 of the context name. The
// Go SDK's client.FromEnv reads only DOCKER_HOST, so an SDK program on a
// machine using Colima, Rancher Desktop, Podman, or a remote context dials
// /var/run/docker.sock and reports "is the docker daemon running?" while
// `docker ps` two lines earlier worked fine.
//
// Everything in this file exists to close that gap, following the same
// precedence the CLI uses.

// Context is one entry from the docker CLI's context store.
type Context struct {
	Metadata map[string]any
	Name     string
	Host     string
	Current  bool
}

// contextMeta is the on-disk shape of contexts/meta/<digest>/meta.json.
type contextMeta struct {
	Metadata  map[string]any `json:"Metadata"`
	Name      string         `json:"Name"`
	Endpoints struct {
		Docker struct {
			Host          string `json:"Host"`
			SkipTLSVerify bool   `json:"SkipTLSVerify"`
		} `json:"docker"`
	} `json:"Endpoints"`
}

// configDir is the docker CLI config directory, honoring DOCKER_CONFIG.
func configDir() string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".docker")
}

// ResolveHost returns the daemon endpoint to dial and the name of the
// context it came from, following the Docker CLI's precedence:
//
//  1. an explicit host (the --host flag)
//  2. $DOCKER_HOST
//  3. $DOCKER_CONTEXT, looked up in the context store
//  4. `currentContext` from ~/.docker/config.json, looked up in the store
//  5. the platform default socket
//
// A missing or unreadable store is not an error — it just means the
// default socket, which is correct for a stock Docker Engine install.
func ResolveHost(explicit string) (host, contextName string) {
	if explicit != "" {
		return explicit, "explicit"
	}
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h, "env"
	}
	name := os.Getenv("DOCKER_CONTEXT")
	if name == "" {
		name = currentContextName()
	}
	if name == "" || name == "default" {
		return "", "default"
	}
	if h := contextHost(name); h != "" {
		return h, name
	}
	return "", name
}

// currentContextName reads `currentContext` out of the CLI config.
func currentContextName() string {
	dir := configDir()
	if dir == "" {
		return ""
	}
	// gosec G304: the path is built from the user's own docker config
	// directory, which they already control.
	path := filepath.Join(dir, "config.json")
	raw, err := os.ReadFile(path) //nolint:gosec // user's own docker config dir
	if err != nil {
		return ""
	}
	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ""
	}
	return cfg.CurrentContext
}

// contextHost looks one context up in the store by name.
func contextHost(name string) string {
	meta, err := readContextMeta(name)
	if err != nil {
		return ""
	}
	return meta.Endpoints.Docker.Host
}

// contextDigest is the store's directory name for a context: the hex
// SHA-256 of the context name.
func contextDigest(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

func readContextMeta(name string) (*contextMeta, error) {
	dir := configDir()
	if dir == "" {
		return nil, fmt.Errorf("no docker config directory")
	}
	path := filepath.Join(dir, "contexts", "meta", contextDigest(name), "meta.json")
	raw, err := os.ReadFile(path) //nolint:gosec // path derived from the user's own docker context store
	if err != nil {
		return nil, fmt.Errorf("reading context %q: %w", name, err)
	}
	var meta contextMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parsing context %q: %w", name, err)
	}
	return &meta, nil
}

// Contexts enumerates the context store so the context-switcher view can
// list them. The implicit `default` context has no store entry — the CLI
// synthesizes it — so it is synthesized here too, otherwise a user who has
// never created a context sees an empty picker.
func Contexts() []Context {
	current := os.Getenv("DOCKER_CONTEXT")
	if current == "" {
		current = currentContextName()
	}
	if current == "" {
		current = "default"
	}

	out := []Context{{
		Name:    "default",
		Host:    defaultHost(),
		Current: current == "default",
	}}

	dir := configDir()
	if dir == "" {
		return out
	}
	entries, err := os.ReadDir(filepath.Join(dir, "contexts", "meta"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(dir, "contexts", "meta", e.Name(), "meta.json")
		raw, err := os.ReadFile(metaPath) //nolint:gosec // user's own context store
		if err != nil {
			continue
		}
		var meta contextMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		out = append(out, Context{
			Name:     meta.Name,
			Host:     meta.Endpoints.Docker.Host,
			Current:  meta.Name == current,
			Metadata: meta.Metadata,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Description renders the context's human description, which docker stores
// under an untyped Metadata map.
func (c Context) Description() string {
	if c.Metadata == nil {
		return ""
	}
	if d, ok := c.Metadata["Description"].(string); ok {
		return d
	}
	return ""
}
