// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
)

// DockerHubServer is the key the docker CLI files Docker Hub's credentials
// under, in config.json and in every credential helper.
const DockerHubServer = "https://index.docker.io/v1/"

// helperTokenUser is the username a credential helper stores beside an
// identity token instead of a password — the docker CLI's convention.
const helperTokenUser = "<token>"

// hubHost is Docker Hub's hostname, the one its aliases fold to.
const hubHost = "index.docker.io"

// localhost is a registry host with neither a dot nor a port.
const localhost = "localhost"

// RegistryAuth is one registry the docker config knows: where its credential
// lives and whether one resolves. It never carries the secret.
type RegistryAuth struct {
	// Server is the key in config.json (Docker Hub's is DockerHubServer).
	Server string
	// Helper names the credential helper holding it ("osxkeychain"), empty
	// when the credential is in config.json itself.
	Helper string
	// Username is who the credential is for, when one resolves.
	Username string
	// Err is why the credential did not resolve, when it did not.
	Err string
	// LoggedIn reports that a credential resolved.
	LoggedIn bool
}

// Credential is a resolved login: what a push hands the daemon.
type Credential struct {
	Server        string
	Username      string
	Password      string
	IdentityToken string
}

// AuthHeader encodes the credential as the X-Registry-Auth value the
// Engine API takes for a push or pull.
func (c Credential) AuthHeader() (string, error) {
	b, err := json.Marshal(registry.AuthConfig{ //nolint:gosec // G117: encoding the secret for the daemon is the point
		Username:      c.Username,
		Password:      c.Password,
		ServerAddress: c.Server,
		IdentityToken: c.IdentityToken,
	})
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// RegistryHost is the hostname a registry address names: the scheme and
// any path dropped, Docker Hub's spellings folded to index.docker.io —
// the CLI's ConvertToHostname plus Hub's aliases, which is how it matches
// an address to a config key.
func RegistryHost(addr string) string {
	h := strings.TrimSpace(addr)
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	h, _, _ = strings.Cut(h, "/")
	h = strings.ToLower(h)
	switch h {
	case "", "docker.io", hubHost, "registry-1.docker.io":
		return hubHost
	}
	return h
}

// NormalizeRegistry is the key `docker login <addr>` files a credential
// under: DockerHubServer for Docker Hub (and for no address at all), else
// the bare hostname.
func NormalizeRegistry(addr string) string {
	h := RegistryHost(addr)
	if h == hubHost {
		return DockerHubServer
	}
	return h
}

// RegistryForRef is the registry an image reference pushes to: the part
// before the first slash when it looks like a host (has a dot or a colon,
// or is localhost), else Docker Hub — reference.ParseNormalizedNamed's
// rule.
func RegistryForRef(ref string) string {
	first, _, ok := strings.Cut(ref, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == localhost) {
		return NormalizeRegistry(first)
	}
	return DockerHubServer
}

// dockerConfig is config.json, with the parts this file reads decoded and
// everything else kept as it was, so a write never loses a key.
type dockerConfig struct {
	raw         map[string]json.RawMessage
	auths       map[string]authEntry
	credHelpers map[string]string
	credsStore  string
	path        string
}

// authEntry is one `auths` entry. Extra keys (email, ...) are kept.
type authEntry struct {
	Auth          string `json:"auth,omitempty"`
	IdentityToken string `json:"identitytoken,omitempty"`
	Email         string `json:"email,omitempty"`
}

// loadDockerConfig reads config.json from the docker config directory; a
// missing file is an empty config.
func loadDockerConfig() (*dockerConfig, error) {
	dir := configDir()
	if dir == "" {
		return nil, errors.New("no docker config directory: neither DOCKER_CONFIG nor a home directory is set")
	}
	cfg := &dockerConfig{
		path:        filepath.Join(dir, "config.json"),
		raw:         map[string]json.RawMessage{},
		auths:       map[string]authEntry{},
		credHelpers: map[string]string{},
	}
	b, err := os.ReadFile(cfg.path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", cfg.path, err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(b, &cfg.raw); err != nil {
		return nil, fmt.Errorf("reading %s: %w", cfg.path, err)
	}
	if a, ok := cfg.raw["auths"]; ok {
		if err := json.Unmarshal(a, &cfg.auths); err != nil {
			return nil, fmt.Errorf("reading %s: auths: %w", cfg.path, err)
		}
	}
	if h, ok := cfg.raw["credHelpers"]; ok {
		if err := json.Unmarshal(h, &cfg.credHelpers); err != nil {
			return nil, fmt.Errorf("reading %s: credHelpers: %w", cfg.path, err)
		}
	}
	if s, ok := cfg.raw["credsStore"]; ok {
		if err := json.Unmarshal(s, &cfg.credsStore); err != nil {
			return nil, fmt.Errorf("reading %s: credsStore: %w", cfg.path, err)
		}
	}
	return cfg, nil
}

// save writes config.json back, every key it did not touch as it was. Like
// the CLI it writes a temporary file beside the real one and renames it
// over, resolving a symlinked config.json first so the link survives, and
// keeps the file private.
func (c *dockerConfig) save() error {
	b, err := c.marshal()
	if err != nil {
		return err
	}
	target := c.path
	if resolved, lerr := filepath.EvalSymlinks(target); lerr == nil {
		target = resolved
	}
	if merr := os.MkdirAll(filepath.Dir(target), 0o700); merr != nil {
		return merr
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename; best effort otherwise
	if _, werr := tmp.Write(b); werr != nil {
		tmp.Close() //nolint:errcheck,gosec // the write error is the one to report
		return werr
	}
	if cerr := tmp.Close(); cerr != nil {
		return cerr
	}
	if cerr := os.Chmod(tmp.Name(), 0o600); cerr != nil {
		return cerr
	}
	return os.Rename(tmp.Name(), target)
}

// marshal is config.json's new content: the raw keys with auths and
// credsStore replaced, tab-indented as the CLI writes it.
func (c *dockerConfig) marshal() ([]byte, error) {
	auths, err := json.Marshal(c.auths)
	if err != nil {
		return nil, err
	}
	c.raw["auths"] = auths
	if c.credsStore != "" {
		store, serr := json.Marshal(c.credsStore)
		if serr != nil {
			return nil, serr
		}
		c.raw["credsStore"] = store
	}
	b, err := json.MarshalIndent(c.raw, "", "\t")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// helperFor is the credential helper holding server's credential: its
// credHelpers entry, else credsStore, else none (config.json itself).
func (c *dockerConfig) helperFor(server string) string {
	if h, ok := c.credHelpers[RegistryHost(server)]; ok {
		return h
	}
	if h, ok := c.credHelpers[server]; ok {
		return h
	}
	return c.credsStore
}

// authKey is the auths key that names server, matched by hostname as the
// CLI does, or "" when there is none.
func (c *dockerConfig) authKey(server string) string {
	if _, ok := c.auths[server]; ok {
		return server
	}
	host := RegistryHost(server)
	keys := make([]string, 0, len(c.auths))
	for k := range c.auths {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if RegistryHost(k) == host {
			return k
		}
	}
	return ""
}

// helperCredential is a credential helper's JSON, in both directions.
type helperCredential struct {
	ServerURL string `json:"ServerURL"`
	Username  string `json:"Username"`
	Secret    string `json:"Secret"`
}

// errNoCredential is a helper that has nothing stored for the server.
var errNoCredential = errors.New("not logged in")

// runHelper runs docker-credential-<helper> <verb> with stdin, the
// credential-helper protocol the docker CLI speaks. No shell sees any of
// it, and the secret travels only on stdin and stdout.
func runHelper(ctx context.Context, helper, verb string, stdin []byte) ([]byte, error) {
	name := "docker-credential-" + helper
	bin, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("credential helper %s is not on PATH", name)
	}
	cmd := exec.CommandContext(ctx, bin, verb) //nolint:gosec // the helper the user's config names, fixed verb
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stdout.String())
		if msg == "" {
			msg = strings.TrimSpace(stderr.String())
		}
		if strings.Contains(strings.ToLower(msg), "credentials not found") {
			return nil, errNoCredential
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s %s: %s", name, verb, lastMeaningfulLine(msg))
	}
	return stdout.Bytes(), nil
}

// resolve finds server's credential: from its helper when it has one,
// else from config.json's auths.
func (c *dockerConfig) resolve(ctx context.Context, server string) (Credential, error) {
	key := cmpOr(c.authKey(server), server)
	if helper := c.helperFor(key); helper != "" {
		out, err := runHelper(ctx, helper, "get", []byte(key))
		if err != nil {
			return Credential{}, err
		}
		var hc helperCredential
		if err := json.Unmarshal(out, &hc); err != nil {
			return Credential{}, fmt.Errorf("docker-credential-%s get: %w", helper, err)
		}
		if hc.Username == "" && hc.Secret == "" {
			return Credential{}, errNoCredential
		}
		cred := Credential{Server: key, Username: hc.Username, Password: hc.Secret}
		if hc.Username == helperTokenUser {
			cred.Username, cred.Password, cred.IdentityToken = "", "", hc.Secret
		}
		return cred, nil
	}
	e, ok := c.auths[key]
	if !ok || (e.Auth == "" && e.IdentityToken == "") {
		return Credential{}, errNoCredential
	}
	cred := Credential{Server: key, IdentityToken: e.IdentityToken}
	if e.Auth != "" {
		dec, err := base64.StdEncoding.DecodeString(e.Auth)
		if err != nil {
			return Credential{}, fmt.Errorf("%s: auth is not base64", key)
		}
		user, pass, ok := strings.Cut(string(dec), ":")
		if !ok {
			return Credential{}, fmt.Errorf("%s: auth is not user:password", key)
		}
		cred.Username, cred.Password = user, pass
	}
	return cred, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Registries lists every registry the docker config names — its auths and
// its credHelpers — and whether a credential resolves for each. Asking a
// helper is a process per row, so this belongs in a command, never the
// render path.
func Registries(ctx context.Context) ([]RegistryAuth, error) {
	cfg, err := loadDockerConfig()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var servers []string
	for k := range cfg.auths {
		if !seen[RegistryHost(k)] {
			seen[RegistryHost(k)] = true
			servers = append(servers, k)
		}
	}
	for k := range cfg.credHelpers {
		if !seen[RegistryHost(k)] {
			seen[RegistryHost(k)] = true
			servers = append(servers, k)
		}
	}
	slices.Sort(servers)
	out := make([]RegistryAuth, 0, len(servers))
	for _, s := range servers {
		r := RegistryAuth{Server: s, Helper: cfg.helperFor(s)}
		cred, err := cfg.resolve(ctx, s)
		switch {
		case err == nil:
			r.LoggedIn = true
			r.Username = cred.Username
			if cred.IdentityToken != "" && r.Username == "" {
				r.Username = helperTokenUser
			}
		case errors.Is(err, errNoCredential):
		default:
			r.Err = err.Error()
		}
		out = append(out, r)
	}
	return out, nil
}

// ResolveCredential is the credential for registry server (an address or
// a config key), and false when the config has none for it — a push then
// goes anonymous, as the CLI's does.
func ResolveCredential(ctx context.Context, server string) (Credential, bool, error) {
	cfg, err := loadDockerConfig()
	if err != nil {
		return Credential{}, false, err
	}
	cred, err := cfg.resolve(ctx, NormalizeRegistry(server))
	if errors.Is(err, errNoCredential) {
		return Credential{}, false, nil
	}
	if err != nil {
		return Credential{}, false, err
	}
	return cred, true, nil
}

// DefaultCredsStore is the helper `docker login` adopts on a config with no
// credsStore — the platform's native store, when its helper is on PATH —
// so a login from dockmaster lands where one from the CLI would rather
// than in plain text. A var so tests can turn it off: they must never
// reach the real keychain.
var DefaultCredsStore = func() string {
	name := map[string]string{"darwin": "osxkeychain", "linux": "secretservice", "windows": "wincred"}[runtime.GOOS]
	if name == "" {
		return ""
	}
	if _, err := exec.LookPath("docker-credential-" + name); err != nil {
		return ""
	}
	return name
}

// Login checks a username and password against server through the daemon
// (RegistryLogin, as `docker login` does) and, when the registry accepts
// them, stores them the way the CLI would: in the server's helper, else
// the config's credsStore, else the platform's native store (recorded as
// credsStore), else config.json. An identity token the registry hands back
// is stored instead of the password. server is normalized as `docker
// login` does; the key it was filed under is returned.
func (c *Client) Login(ctx context.Context, server, username, password string) (string, error) {
	key := NormalizeRegistry(server)
	res, err := c.api.RegistryLogin(ctx, client.RegistryLoginOptions{
		Username: username, Password: password, ServerAddress: key,
	})
	if err != nil {
		return key, fmt.Errorf("logging in to %s: %w", key, err)
	}
	return key, StoreCredential(ctx, Credential{
		Server: key, Username: username, Password: password, IdentityToken: res.Auth.IdentityToken,
	})
}

// StoreCredential files a credential that has been validated, by the
// rules Login documents.
func StoreCredential(ctx context.Context, cred Credential) error {
	cfg, err := loadDockerConfig()
	if err != nil {
		return err
	}
	if old := cfg.authKey(cred.Server); old != "" && old != cred.Server {
		delete(cfg.auths, old)
	}
	helper := cfg.helperFor(cred.Server)
	if helper == "" {
		if def := DefaultCredsStore(); def != "" {
			cfg.credsStore, helper = def, def
		}
	}
	if helper == "" {
		e := authEntry{IdentityToken: cred.IdentityToken}
		if cred.IdentityToken == "" {
			e.Auth = base64.StdEncoding.EncodeToString([]byte(cred.Username + ":" + cred.Password))
		} else {
			e.Auth = base64.StdEncoding.EncodeToString([]byte(cred.Username + ":"))
		}
		cfg.auths[cred.Server] = e
		return cfg.save()
	}
	hc := helperCredential{ServerURL: cred.Server, Username: cred.Username, Secret: cred.Password}
	if cred.IdentityToken != "" {
		hc.Username, hc.Secret = helperTokenUser, cred.IdentityToken
	}
	in, err := json.Marshal(hc) //nolint:gosec // G117: the helper protocol carries the secret, on stdin only
	if err != nil {
		return err
	}
	if _, err := runHelper(ctx, helper, "store", in); err != nil {
		return err
	}
	// The CLI keeps an empty auths entry beside a helper's credential, so
	// the registry is listed; the secret stays in the helper.
	cfg.auths[cred.Server] = authEntry{}
	return cfg.save()
}

// Logout removes server's credential: erases it from its helper and drops
// its auths entry, as `docker logout` does. A helper with nothing stored
// is not an error — the point was for there to be nothing.
func Logout(ctx context.Context, server string) error {
	cfg, err := loadDockerConfig()
	if err != nil {
		return err
	}
	key := cmpOr(cfg.authKey(server), server)
	if helper := cfg.helperFor(key); helper != "" {
		if _, err := runHelper(ctx, helper, "erase", []byte(key)); err != nil && !errors.Is(err, errNoCredential) {
			return err
		}
	}
	if _, ok := cfg.auths[key]; !ok {
		return nil
	}
	delete(cfg.auths, key)
	return cfg.save()
}
