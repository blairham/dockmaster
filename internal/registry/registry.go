// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package registry is a small client for a registry's v2 HTTP API (the
// distribution spec): its catalog, a repository's tags, a tag's manifest
// and config, and deleting a manifest by digest. It answers the
// token-auth challenge with the docker config's credential for the
// registry, so it works on a remote registry the user is logged in to as
// well as on a local one. No bubbletea in here.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/blairham/dockmaster/internal/docker"
)

// Manifest media types the client asks for and understands.
const (
	mediaOCIIndex       = "application/vnd.oci.image.index.v1+json"
	mediaOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	mediaDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaDockerV2       = "application/vnd.docker.distribution.manifest.v2+json"
	headerContentDigest = "Docker-Content-Digest"
)

// manifestAccept is every manifest shape the client reads, an index first
// so a multi-platform tag answers with its index, whose digest is the tag's.
var manifestAccept = []string{mediaOCIIndex, mediaDockerList, mediaOCIManifest, mediaDockerV2}

// localhost is the loopback name, dialed as 127.0.0.1.
const localhost = "localhost"

// pageSize is how many names one catalog or tags page asks for.
const pageSize = 1000

// maxBody caps what the client reads of any one response: a manifest, a
// config or a page of names is small, and a registry is someone else's
// server.
const maxBody = 8 << 20

// ErrDeleteDisabled is a registry that refuses deletes — registry:2
// without REGISTRY_STORAGE_DELETE_ENABLED=true.
var ErrDeleteDisabled = errors.New(
	"the registry does not allow deletes — registry:2 needs REGISTRY_STORAGE_DELETE_ENABLED=true " +
		"(dockmaster's own registry sets it)",
)

// CredentialFunc finds the credential for a registry address, and false
// when there is none (anonymous). docker.ResolveCredential is the default.
type CredentialFunc func(ctx context.Context, server string) (docker.Credential, bool, error)

// Client talks to one registry.
type Client struct {
	http   *http.Client
	creds  CredentialFunc
	tokens map[string]string
	addr   string
	base   string
	basic  string
	mu     sync.Mutex
}

// New is a client for the registry at addr — a host[:port] such as
// localhost:5000 or ghcr.io, a config key such as Docker Hub's, or a URL
// whose scheme then decides. A loopback registry is spoken to over plain
// HTTP, as the daemon treats it as insecure by default; anything else over
// HTTPS. localhost is dialed as 127.0.0.1, which is the address a
// runtime's port forwarding (Docker Desktop, colima, OrbStack) answers on.
func New(addr string) *Client {
	host := docker.RegistryHost(addr)
	if host == "index.docker.io" {
		host = "registry-1.docker.io"
	}
	scheme := "https"
	switch {
	case strings.HasPrefix(strings.TrimSpace(addr), "http://"):
		scheme = "http"
	case strings.HasPrefix(strings.TrimSpace(addr), "https://"):
	case isLoopback(host):
		scheme = "http"
	}
	dial := host
	if h, p, err := net.SplitHostPort(host); err == nil && h == localhost {
		dial = net.JoinHostPort("127.0.0.1", p)
	} else if host == localhost {
		dial = "127.0.0.1"
	}
	return &Client{
		addr:   strings.TrimSpace(addr),
		base:   scheme + "://" + dial,
		http:   &http.Client{Timeout: 60 * time.Second},
		creds:  docker.ResolveCredential,
		tokens: map[string]string{},
	}
}

// WithCredentials replaces how the client finds a credential; for tests.
func (c *Client) WithCredentials(f CredentialFunc) *Client {
	c.creds = f
	return c
}

// Base is the URL the client speaks to, scheme and host.
func (c *Client) Base() string { return c.base }

// isLoopback reports whether host (with or without a port) is this machine.
func isLoopback(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	if h == localhost {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Catalog lists the registry's repositories, every page of them. Docker
// Hub does not serve a catalog; it answers with an error, which is
// returned.
func (c *Client) Catalog(ctx context.Context) ([]string, error) {
	var out []string
	next := fmt.Sprintf("/v2/_catalog?n=%d", pageSize)
	for next != "" {
		var page struct {
			Repositories []string `json:"repositories"`
		}
		link, err := c.getJSON(ctx, next, "registry:catalog:*", &page)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Repositories...)
		next = link
	}
	return out, nil
}

// Tags lists a repository's tags, every page of them.
func (c *Client) Tags(ctx context.Context, repo string) ([]string, error) {
	var out []string
	next := fmt.Sprintf("/v2/%s/tags/list?n=%d", repo, pageSize)
	for next != "" {
		var page struct {
			Tags []string `json:"tags"`
		}
		link, err := c.getJSON(ctx, next, pullScope(repo), &page)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Tags...)
		next = link
	}
	return out, nil
}

// Image is one tag as the registry holds it.
type Image struct {
	Created time.Time
	// Err is why the tag's manifest or config could not be read; the tag
	// still lists.
	Err error
	Tag string
	// Digest is the manifest's — the index's, for a multi-platform tag —
	// which is what a delete takes.
	Digest string
	// MediaType is the manifest's.
	MediaType string
	// Platforms are an index's images, "linux/amd64"; empty for a single
	// image.
	Platforms []string
	// Size is the image's compressed size: its config and layers as the
	// registry stores them. For an index it is the first platform's.
	Size int64
}

// manifest is the parts of an image manifest or an index the client reads.
type manifest struct {
	MediaType string `json:"mediaType"`
	Config    struct {
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"config"`
	Layers []struct {
		Size int64 `json:"size"`
	} `json:"layers"`
	Manifests []struct {
		Platform *struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
			Variant      string `json:"variant"`
		} `json:"platform"`
		Digest    string `json:"digest"`
		MediaType string `json:"mediaType"`
	} `json:"manifests"`
}

// platforms are an index's images as "os/arch[/variant]", attestations
// (unknown/unknown) left out, and the first one's digest.
func (m manifest) platforms() (names []string, first string) {
	for _, sub := range m.Manifests {
		p := sub.Platform
		if p == nil || p.OS == "unknown" {
			continue // an attestation, not an image
		}
		name := p.OS + "/" + p.Architecture
		if p.Variant != "" {
			name += "/" + p.Variant
		}
		names = append(names, name)
		if first == "" {
			first = sub.Digest
		}
	}
	return names, first
}

// Image reads a tag's manifest — and, for an index, its first platform's —
// and its config for the created time.
func (c *Client) Image(ctx context.Context, repo, tag string) Image {
	img := Image{Tag: tag}
	m, digest, err := c.manifest(ctx, repo, tag)
	if err != nil {
		img.Err = err
		return img
	}
	img.Digest, img.MediaType = digest, m.MediaType
	if len(m.Manifests) > 0 {
		var first string
		img.Platforms, first = m.platforms()
		if first == "" {
			return img
		}
		if m, _, err = c.manifest(ctx, repo, first); err != nil {
			img.Err = err
			return img
		}
	}
	img.Size = m.Config.Size
	for _, l := range m.Layers {
		img.Size += l.Size
	}
	if m.Config.Digest != "" {
		var cfg struct {
			Created time.Time `json:"created"`
		}
		if _, err := c.getJSON(ctx, "/v2/"+repo+"/blobs/"+m.Config.Digest, pullScope(repo), &cfg); err != nil {
			img.Err = err
		}
		img.Created = cfg.Created
	}
	return img
}

// Images reads every tag's Image, a few at a time, in the order given.
func (c *Client) Images(ctx context.Context, repo string, tags []string) []Image {
	out := make([]Image, len(tags))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, tag := range tags {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = c.Image(ctx, repo, tag)
		}()
	}
	wg.Wait()
	return out
}

// manifest fetches one manifest and its digest: the registry's
// Docker-Content-Digest, else the body's own sha256, which is what the
// digest is defined as.
func (c *Client) manifest(ctx context.Context, repo, ref string) (manifest, string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v2/"+repo+"/manifests/"+ref, pullScope(repo), manifestAccept)
	if err != nil {
		return manifest{}, "", err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return manifest{}, "", err
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return manifest{}, "", fmt.Errorf("manifest %s:%s: %w", repo, ref, err)
	}
	if m.MediaType == "" {
		m.MediaType = resp.Header.Get("Content-Type")
	}
	digest := resp.Header.Get(headerContentDigest)
	if digest == "" {
		sum := sha256.Sum256(body)
		digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	return m, digest, nil
}

// Delete removes a manifest by digest — every tag pointing at it goes
// with it, as the registry API defines. Its blobs stay until the registry
// garbage-collects.
func (c *Client) Delete(ctx context.Context, repo, digest string) error {
	if !strings.HasPrefix(digest, "sha256:") {
		return fmt.Errorf("deleting from %s: %q is not a digest", repo, digest)
	}
	resp, err := c.do(ctx, http.MethodDelete, "/v2/"+repo+"/manifests/"+digest, deleteScope(repo), nil)
	if err != nil {
		if errors.Is(err, errMethodNotAllowed) {
			return ErrDeleteDisabled
		}
		return err
	}
	resp.Body.Close() //nolint:errcheck,gosec // nothing to read
	return nil
}

func pullScope(repo string) string   { return "repository:" + repo + ":pull" }
func deleteScope(repo string) string { return "repository:" + repo + ":delete" }

// getJSON GETs path and decodes the body into v, returning the next page's
// path from a Link header, if any.
func (c *Client) getJSON(ctx context.Context, path, scope string, v any) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, path, scope, []string{"application/json"})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(v); err != nil {
		return "", fmt.Errorf("%s: %w", trimQuery(path), err)
	}
	return nextLink(resp.Header.Get("Link")), nil
}

// nextLink is the path of a `Link: </v2/...>; rel="next"` header.
func nextLink(h string) string {
	for part := range strings.SplitSeq(h, ",") {
		target, params, ok := strings.Cut(part, ";")
		if !ok || !strings.Contains(params, `rel="next"`) {
			continue
		}
		target = strings.Trim(strings.TrimSpace(target), "<>")
		if u, err := url.Parse(target); err == nil && strings.HasPrefix(u.Path, "/v2/") {
			if u.RawQuery != "" {
				return u.Path + "?" + u.RawQuery
			}
			return u.Path
		}
	}
	return ""
}

func trimQuery(path string) string {
	p, _, _ := strings.Cut(path, "?")
	return p
}

// errMethodNotAllowed is a 405, which a delete turns into ErrDeleteDisabled.
var errMethodNotAllowed = errors.New("method not allowed")

// do sends one request, answering an auth challenge once: a Bearer
// challenge with a token from its realm, a Basic one with the credential
// itself. Any status but 2xx is an error carrying the registry's message.
func (c *Client) do(ctx context.Context, method, path, scope string, accept []string) (*http.Response, error) {
	resp, err := c.send(ctx, method, path, scope, accept)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close() //nolint:errcheck,gosec // replaced by the retry
		if aerr := c.authorize(ctx, challenge, scope); aerr != nil {
			return nil, fmt.Errorf("%s %s: %w", method, trimQuery(path), aerr)
		}
		if resp, err = c.send(ctx, method, path, scope, accept); err != nil {
			return nil, err
		}
	}
	if resp.StatusCode/100 == 2 {
		return resp, nil
	}
	defer resp.Body.Close() //nolint:errcheck // read for the message only
	err = apiError(resp)
	if resp.StatusCode == http.StatusMethodNotAllowed {
		err = fmt.Errorf("%w: %w", errMethodNotAllowed, err)
	}
	return nil, fmt.Errorf("%s %s: %w", method, trimQuery(path), err)
}

func (c *Client) send(ctx context.Context, method, path, scope string, accept []string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, http.NoBody)
	if err != nil {
		return nil, err
	}
	for _, a := range accept {
		req.Header.Add("Accept", a)
	}
	c.mu.Lock()
	switch {
	case c.tokens[scope] != "":
		req.Header.Set("Authorization", "Bearer "+c.tokens[scope])
	case c.basic != "":
		req.Header.Set("Authorization", "Basic "+c.basic)
	}
	c.mu.Unlock()
	resp, err := c.http.Do(req) //nolint:gosec // G704: the registry the user chose
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, c.addr, err)
	}
	return resp, nil
}

// apiError is a registry's error body — {"errors":[{"code","message"}]} —
// as one line, else the status.
func apiError(resp *http.Response) error {
	var body struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err == nil && json.Unmarshal(b, &body) == nil && len(body.Errors) > 0 {
		e := body.Errors[0]
		msg := strings.TrimSpace(e.Code + " " + e.Message)
		return fmt.Errorf("%s: %s", resp.Status, msg)
	}
	return errors.New(resp.Status)
}

// authorize answers a WWW-Authenticate challenge, so the retry carries
// authorization: Basic sends the credential, Bearer trades it (or nothing,
// for an anonymous pull token) at the challenge's realm for a token.
func (c *Client) authorize(ctx context.Context, challenge, scope string) error {
	kind, params := parseChallenge(challenge)
	cred, ok, err := c.creds(ctx, c.addr)
	if err != nil {
		return fmt.Errorf("credential for %s: %w", c.addr, err)
	}
	switch kind {
	case "basic":
		if !ok || cred.Username == "" {
			return fmt.Errorf("%s wants a login — :login %s", c.addr, docker.RegistryHost(c.addr))
		}
		c.mu.Lock()
		c.basic = basicAuth(cred.Username, cred.Password)
		c.mu.Unlock()
		return nil
	case "bearer":
		tok, err := c.fetchToken(ctx, params, scope, cred, ok)
		if err != nil {
			return err
		}
		c.mu.Lock()
		c.tokens[scope] = tok
		c.mu.Unlock()
		return nil
	}
	return fmt.Errorf("unauthorized, and the registry's challenge %q is not one dockmaster speaks", challenge)
}

// fetchToken gets a bearer token from the challenge's realm for scope: the
// scope the challenge names when it names one, else the client's own.
func (c *Client) fetchToken(
	ctx context.Context, params map[string]string, scope string, cred docker.Credential, haveCred bool,
) (string, error) {
	realm := params["realm"]
	if realm == "" {
		return "", errors.New("the registry's token challenge names no realm")
	}
	if s := params["scope"]; s != "" {
		scope = s
	}
	req, err := tokenRequest(ctx, realm, params["service"], scope, cred, haveCred)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	resp, err := c.http.Do(req) //nolint:gosec // G704: the realm the registry named
	if err != nil {
		return "", fmt.Errorf("token from %s: %w", realm, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode/100 != 2 {
		hint := ""
		if !haveCred {
			hint = " — not logged in: :login " + docker.RegistryHost(c.addr)
		}
		return "", fmt.Errorf("token from %s: %w%s", realm, apiError(resp), hint)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&tok); err != nil {
		return "", fmt.Errorf("token from %s: %w", realm, err)
	}
	if tok.Token == "" {
		tok.Token = tok.AccessToken
	}
	if tok.Token == "" {
		return "", fmt.Errorf("token from %s: the answer carries no token", realm)
	}
	return tok.Token, nil
}

// tokenRequest is the request for a token at realm: an identity token is
// an OAuth2 refresh token, POSTed; otherwise a GET, with the username and
// password as basic auth when there are some, anonymous when not.
func tokenRequest(
	ctx context.Context, realm, service, scope string, cred docker.Credential, haveCred bool,
) (*http.Request, error) {
	q := url.Values{}
	if service != "" {
		q.Set("service", service)
	}
	q.Set("scope", scope)
	if haveCred && cred.IdentityToken != "" {
		q.Set("grant_type", "refresh_token")
		q.Set("refresh_token", cred.IdentityToken)
		q.Set("client_id", "dockmaster")
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, realm, strings.NewReader(q.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req, nil
	}
	sep := "?"
	if strings.Contains(realm, "?") {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm+sep+q.Encode(), http.NoBody)
	if err != nil {
		return nil, err
	}
	if haveCred && cred.Username != "" {
		req.SetBasicAuth(cred.Username, cred.Password)
	}
	return req, nil
}

// parseChallenge splits `Bearer realm="…",service="…",scope="…"` into its
// scheme, lowercased, and its parameters. A quoted value may hold commas
// (a scope list does).
func parseChallenge(h string) (string, map[string]string) {
	kind, rest, _ := strings.Cut(strings.TrimSpace(h), " ")
	params := map[string]string{}
	for rest = strings.TrimSpace(rest); rest != ""; {
		key, after, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		key = strings.ToLower(strings.TrimSpace(key))
		var val string
		if strings.HasPrefix(after, `"`) {
			end := strings.Index(after[1:], `"`)
			if end < 0 {
				val, rest = after[1:], ""
			} else {
				val, rest = after[1:1+end], after[2+end:]
			}
		} else {
			val, rest, _ = strings.Cut(after, ",")
			rest = "," + rest
		}
		params[key] = val
		rest = strings.TrimLeft(strings.TrimSpace(rest), ",")
		rest = strings.TrimSpace(rest)
	}
	return strings.ToLower(kind), params
}

func basicAuth(user, pass string) string {
	req := http.Request{Header: http.Header{}}
	req.SetBasicAuth(user, pass)
	return strings.TrimPrefix(req.Header.Get("Authorization"), "Basic ")
}
