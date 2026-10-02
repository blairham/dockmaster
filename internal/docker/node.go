// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// Kubernetes-in-docker nodes are containers running their own container
// runtime (containerd). Their workloads — every pod — live in that runtime,
// not in this daemon, so `docker ps` shows the node and nothing in it. These
// functions reach inside a node with `crictl` over a docker exec.

// Node labels kind and k3d put on their node containers.
const (
	labelKindRole = "io.x-k8s.kind.role"
	labelK3dRole  = "k3d.role"
)

// NodeRole reports whether a container is a kind or k3d Kubernetes node,
// and its role ("control-plane", "worker", "server", "agent"). A load
// balancer or registry the tools also run is not a node: it has no
// container runtime inside.
func NodeRole(c Container) (string, bool) {
	if r, ok := c.Labels[labelKindRole]; ok && r != "" {
		return r, true
	}
	switch r := c.Labels[labelK3dRole]; r {
	case "server", "agent":
		return r, true
	}
	return "", false
}

// NodeContainer is one container inside a Kubernetes node, as crictl
// reports it.
type NodeContainer struct {
	Created   time.Time
	ID        string
	Name      string // the container's name in its pod spec
	Pod       string
	Namespace string
	State     string // running|exited|created|unknown
	Image     string
	Attempt   int // restarts of this container in its pod
}

// NodeContainers lists every container inside node, running or not,
// grouped by namespace and pod.
func (c *Client) NodeContainers(ctx context.Context, node string) ([]NodeContainer, error) {
	out, err := c.Exec(ctx, node, "crictl", "ps", "-a", "-o", "json")
	if err != nil {
		return nil, err
	}
	return parseCrictlPS(out)
}

func parseCrictlPS(out []byte) ([]NodeContainer, error) {
	var ps struct {
		Containers []struct {
			Labels map[string]string `json:"labels"`
			Image  struct {
				Image              string `json:"image"`
				UserSpecifiedImage string `json:"userSpecifiedImage"`
			} `json:"image"`
			ID        string `json:"id"`
			State     string `json:"state"`
			CreatedAt string `json:"createdAt"`
			Metadata  struct {
				Name    string `json:"name"`
				Attempt int    `json:"attempt"`
			} `json:"metadata"`
		} `json:"containers"`
	}
	if err := json.Unmarshal(out, &ps); err != nil {
		return nil, fmt.Errorf("reading crictl ps: %w", err)
	}
	list := make([]NodeContainer, 0, len(ps.Containers))
	for _, p := range ps.Containers {
		image := p.Image.UserSpecifiedImage
		if image == "" {
			image = p.Image.Image
		}
		var created time.Time
		if ns, err := strconv.ParseInt(p.CreatedAt, 10, 64); err == nil {
			created = time.Unix(0, ns)
		}
		list = append(list, NodeContainer{
			Created:   created,
			ID:        p.ID,
			Name:      p.Metadata.Name,
			Pod:       p.Labels["io.kubernetes.pod.name"],
			Namespace: p.Labels["io.kubernetes.pod.namespace"],
			State:     strings.ToLower(strings.TrimPrefix(p.State, "CONTAINER_")),
			Image:     image,
			Attempt:   p.Metadata.Attempt,
		})
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Pod != b.Pod {
			return a.Pod < b.Pod
		}
		return a.Name < b.Name
	})
	return list, nil
}

// NodeRemove removes an exited container inside node (`crictl rm`).
// crictl refuses a running one without --force, and dockmaster never passes
// it: a running container is the kubelet's, and it would only restart it.
func (c *Client) NodeRemove(ctx context.Context, node, id string) error {
	_, err := c.Exec(ctx, node, "crictl", "rm", id)
	return err
}

// NodeInspect is `crictl inspect` of a container inside node.
func (c *Client) NodeInspect(ctx context.Context, node, id string) ([]byte, error) {
	return c.Exec(ctx, node, "crictl", "inspect", id)
}

// nodeLogScript runs `crictl logs -f` and kills it when stdin closes.
// Closing a docker exec stream does not end the process inside the
// container: a bare `crictl logs -f` would outlive every log view opened on
// it, one orphan per visit. The watcher's output goes to /dev/null so it
// does not hold the stream open after crictl exits on its own. The watcher
// reads the exec's stdin through fd 3 on purpose: a non-interactive sh
// gives background jobs /dev/null as stdin, so a plain `cat` would see EOF
// at once and kill crictl before it printed a line.
const nodeLogScript = `exec 3<&0; crictl logs "$@" </dev/null & p=$!; ` +
	`(cat <&3 >/dev/null; kill $p 2>/dev/null) >/dev/null 2>&1 & wait $p`

// StreamNodeLogs is StreamLogs for a container inside node: a follow-mode
// tail read through crictl, stdout and stderr kept apart.
func (c *Client) StreamNodeLogs(
	ctx context.Context, node, id string, tail int, timestamps bool, since time.Time,
) (*LogStream, error) {
	args := nodeLogArgs(tail, timestamps, since)
	args = append(args, id)
	cmd := append([]string{"sh", "-c", nodeLogScript, "crictl-logs"}, args...)
	return c.streamNodeLogs(ctx, node, id, cmd)
}

// nodeLogArgs are crictl logs' flags: follow, then a line count or a start
// time (crictl takes RFC 3339), then timestamps.
func nodeLogArgs(tail int, timestamps bool, since time.Time) []string {
	args := []string{"-f"}
	switch {
	case !since.IsZero():
		args = append(args, "--since="+since.UTC().Format(time.RFC3339))
	case tail > 0:
		args = append(args, "--tail="+strconv.Itoa(tail))
	}
	if timestamps {
		args = append(args, "--timestamps")
	}
	return args
}

func (c *Client) streamNodeLogs(ctx context.Context, node, id string, cmd []string) (*LogStream, error) {
	exec, err := c.api.ExecCreate(ctx, node, client.ExecCreateOptions{
		Cmd: cmd, AttachStdin: true, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("streaming logs for %s in %s: %w", shortID(id), node, err)
	}
	resp, err := c.api.ExecAttach(ctx, exec.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("streaming logs for %s in %s: %w", shortID(id), node, err)
	}

	lines := make(chan LogLine, 512)
	errc := make(chan error, 1)

	// Hang up when the view does: closing stdin is the watcher's cue to
	// kill crictl inside the node.
	go func() {
		<-ctx.Done()
		_ = resp.CloseWrite() //nolint:errcheck // best effort; Close follows
		resp.Close()
	}()

	go func() {
		defer close(lines)
		outR, outW := io.Pipe()
		errR, errW := io.Pipe()
		go func() {
			_, cpErr := stdcopy.StdCopy(outW, errW, resp.Reader)
			outW.CloseWithError(cpErr) //nolint:errcheck // propagated to the scanners
			errW.CloseWithError(cpErr) //nolint:errcheck // propagated to the scanners
			if ctx.Err() != nil {
				cpErr = nil // our own hang-up, not a failure
			}
			errc <- cpErr
		}()
		done := make(chan struct{}, 2)
		go func() { scanLines(ctx, outR, false, lines); done <- struct{}{} }()
		go func() { scanLines(ctx, errR, true, lines); done <- struct{}{} }()
		<-done
		<-done
	}()

	return &LogStream{Lines: lines, Err: errc}, nil
}

// Exec runs argv in container id without a TTY and returns its stdout. A
// non-zero exit is an error carrying the command's stderr, which is where
// crictl and friends say what went wrong.
func (c *Client) Exec(ctx context.Context, id string, argv ...string) ([]byte, error) {
	exec, err := c.api.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd: argv, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("exec %s in %s: %w", argv[0], id, err)
	}
	resp, err := c.api.ExecAttach(ctx, exec.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("exec %s in %s: %w", argv[0], id, err)
	}
	defer resp.Close()

	var stdout, stderr bytes.Buffer
	copied := make(chan error, 1)
	go func() {
		_, cpErr := stdcopy.StdCopy(&stdout, &stderr, resp.Reader)
		copied <- cpErr
	}()
	select {
	case cpErr := <-copied:
		if cpErr != nil {
			return nil, fmt.Errorf("exec %s in %s: %w", argv[0], id, cpErr)
		}
	case <-ctx.Done():
		return nil, fmt.Errorf("exec %s in %s: %w", argv[0], id, ctx.Err())
	}

	insp, err := c.api.ExecInspect(ctx, exec.ID, client.ExecInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("exec %s in %s: %w", argv[0], id, err)
	}
	if insp.ExitCode != 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("exit status %d", insp.ExitCode)
		}
		return nil, fmt.Errorf("%s in %s: %s", argv[0], id, msg)
	}
	return stdout.Bytes(), nil
}
