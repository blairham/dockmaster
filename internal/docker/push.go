// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/distribution/reference"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"
)

// ParseTag checks a tag target as `docker tag` takes it — `repo[:tag]`,
// lowercase repository, no digest — and returns it normalized the way the
// daemon will file it (`app` is `docker.io/library/app:latest`) beside the
// familiar spelling to show (`app:latest`).
func ParseTag(target string) (full, familiar string, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", errors.New("a tag is required: repo[:tag]")
	}
	ref, err := reference.ParseNormalizedNamed(target)
	if err != nil {
		return "", "", fmt.Errorf("%q is not a valid repo[:tag]: %w", target, err)
	}
	if _, ok := ref.(reference.Digested); ok {
		return "", "", fmt.Errorf("%q has a digest — a tag cannot", target)
	}
	ref = reference.TagNameOnly(ref)
	return ref.String(), reference.FamiliarString(ref), nil
}

// TagImage is `docker tag source target`: source is an image reference or
// ID, target a `repo[:tag]` (":latest" when it has none).
func (c *Client) TagImage(ctx context.Context, source, target string) error {
	if _, _, err := ParseTag(target); err != nil {
		return err
	}
	if _, err := c.api.ImageTag(ctx, client.ImageTagOptions{Source: source, Target: target}); err != nil {
		return fmt.Errorf("tagging %s as %s: %w", source, target, err)
	}
	return nil
}

// PushImage is `docker push ref`, with the credential the docker config
// holds for ref's registry (ResolveCredential) — anonymous when it holds
// none, as the CLI's is. progress is handed a one-line summary each time
// the daemon reports on a layer; it may be nil. It returns the registry
// pushed to, for the caller to name.
func (c *Client) PushImage(ctx context.Context, ref string, progress func(string)) (string, error) {
	if _, _, err := ParseTag(ref); err != nil {
		return "", err
	}
	server := RegistryForRef(ref)
	opts := client.ImagePushOptions{}
	cred, ok, err := ResolveCredential(ctx, server)
	if err != nil {
		return server, fmt.Errorf("credential for %s: %w", server, err)
	}
	if ok {
		if opts.RegistryAuth, err = cred.AuthHeader(); err != nil {
			return server, err
		}
	}
	resp, err := c.api.ImagePush(ctx, ref, opts)
	if err != nil {
		return server, fmt.Errorf("pushing %s: %w", ref, err)
	}
	var p PushProgress
	for msg, err := range resp.JSONMessages(ctx) {
		if err != nil {
			return server, fmt.Errorf("pushing %s: %w", ref, err)
		}
		if msg.Error != nil {
			return server, fmt.Errorf("pushing %s: %s", ref, msg.Error.Message)
		}
		if p.Add(msg) && progress != nil {
			progress(p.String())
		}
	}
	return server, nil
}

// PushProgress folds a push's per-layer messages into one line: how many
// layers are done, and the bytes sent of those still going.
type PushProgress struct {
	layers map[string]layerState
	order  []string
}

type layerState struct {
	current, total int64
	done           bool
}

// Add folds one message in and reports whether the summary changed.
func (p *PushProgress) Add(m jsonstream.Message) bool {
	if m.ID == "" {
		return false
	}
	if p.layers == nil {
		p.layers = map[string]layerState{}
	}
	st, seen := p.layers[m.ID]
	if !seen {
		p.order = append(p.order, m.ID)
	}
	status := strings.ToLower(m.Status)
	switch {
	case status == "pushed", status == "layer already exists", strings.HasPrefix(status, "mounted from"):
		st.done = true
		st.current = st.total
	case status == "pushing" && m.Progress != nil:
		st.current, st.total = m.Progress.Current, m.Progress.Total
	case status == "preparing", status == "waiting":
	default:
		// A tag's digest line, or a status this does not know: not a layer.
		if !seen {
			p.order = p.order[:len(p.order)-1]
			return false
		}
	}
	p.layers[m.ID] = st
	return true
}

// String is the summary: "2/5 layers · 12.3MB/40.1MB".
func (p *PushProgress) String() string {
	done := 0
	var cur, total int64
	for _, id := range p.order {
		st := p.layers[id]
		if st.done {
			done++
			continue
		}
		cur += st.current
		total += st.total
	}
	s := fmt.Sprintf("%d/%d layers", done, len(p.order))
	if total > 0 {
		s += " · " + HumanSize(cur) + "/" + HumanSize(total)
	}
	return s
}
