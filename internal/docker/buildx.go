// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

// CLIRunner runs the docker CLI with args and returns its stdout. On a
// non-zero exit the error carries the last line of stderr. A var-shaped
// seam so tests never run the CLI.
type CLIRunner func(ctx context.Context, args ...string) ([]byte, error)

// Buildx drives `docker buildx` for the builders view (#119): list, inspect,
// start, stop, create, remove, use and prune. There is no Engine API for
// builders — they live in the CLI's own store (~/.docker/buildx) and some
// of them are not on the daemon at all (a kubernetes or remote builder) —
// so the CLI is the only faithful way to reach them, as for compose.
type Buildx struct {
	run      CLIRunner
	endpoint []string
}

// ErrNoBuildx is returned when the docker CLI is not on PATH.
var ErrNoBuildx = errors.New("builders need the `docker` CLI and its buildx plugin on PATH")

// NewBuildx finds the docker CLI. endpoint is Client.EndpointArgs, passed to
// every invocation, with DOCKER_HOST set to agree with it (endpointEnv):
// the buildx plugin reads DOCKER_HOST before --host, and which builder is
// current is kept per endpoint, so a builder listed or used here is the one
// for the daemon on screen.
func NewBuildx(endpoint []string) (*Buildx, error) {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return nil, ErrNoBuildx
	}
	return NewBuildxWithRunner(cliExec(bin, endpointEnv(endpoint)), endpoint), nil
}

// NewBuildxWithRunner builds a Buildx over an arbitrary runner.
func NewBuildxWithRunner(run CLIRunner, endpoint []string) *Buildx {
	return &Buildx{run: run, endpoint: endpoint}
}

// cliExec runs bin with args, env added to dockmaster's environment, and
// no shell.
func cliExec(bin string, env []string) CLIRunner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary; argv built here, no shell
		if len(env) > 0 {
			cmd.Env = append(os.Environ(), env...)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if line := lastMeaningfulLine(stderr.String()); line != "" {
				return nil, errors.New(strings.TrimPrefix(line, "ERROR: "))
			}
			return nil, err
		}
		return stdout.Bytes(), nil
	}
}

// buildx runs `docker <endpoint> buildx <args>`, naming the verb in an error.
func (b *Buildx) buildx(ctx context.Context, verb string, args ...string) ([]byte, error) {
	argv := append(slices.Clone(b.endpoint), "buildx", verb)
	out, err := b.run(ctx, append(argv, args...)...)
	if err != nil {
		return nil, fmt.Errorf("buildx %s: %w", verb, err)
	}
	return out, nil
}

// BuildxBuilder is one builder as `docker buildx ls --format json` reports
// it: one record per builder, its nodes nested.
type BuildxBuilder struct {
	LastActivity time.Time    `json:"LastActivity"`
	Name         string       `json:"Name"`
	Driver       string       `json:"Driver"`
	Err          string       `json:"Err"`
	Nodes        []BuildxNode `json:"Nodes"`
	Current      bool         `json:"Current"`
	Dynamic      bool         `json:"Dynamic"`
}

// BuildxNode is one node of a builder: where BuildKit runs for it.
type BuildxNode struct {
	Name      string   `json:"Name"`
	Endpoint  string   `json:"Endpoint"`
	Status    string   `json:"Status"`
	Version   string   `json:"Version"`
	Err       string   `json:"Err"`
	Platforms []string `json:"Platforms"`
}

// The builder states Status reads specially, and the one driver that takes
// an endpoint of its own.
const (
	builderRunning = "running"
	builderError   = "error"
	DriverRemote   = "remote"
)

// ParseBuilders reads `buildx ls --format json`: one JSON object per line.
// A builder that cannot be loaded is still a record, with its Err set.
func ParseBuilders(out []byte) ([]BuildxBuilder, error) {
	var bs []BuildxBuilder
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), maxStreamLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var b BuildxBuilder
		if err := json.Unmarshal(line, &b); err != nil {
			return nil, fmt.Errorf("reading buildx ls: %w", err)
		}
		bs = append(bs, b)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading buildx ls: %w", err)
	}
	return bs, nil
}

// Builders lists every builder the CLI knows: `buildx ls --format json`.
func (b *Buildx) Builders(ctx context.Context) ([]BuildxBuilder, error) {
	out, err := b.buildx(ctx, "ls", "--format=json")
	if err != nil {
		return nil, err
	}
	return ParseBuilders(out)
}

// ContextBuilder reports whether the builder is a docker context's own —
// the docker driver, which buildx makes for every context and will not
// remove (remove the context instead). One that could not be loaded has no
// driver at all; only a context's builder is listed that way, since a
// created builder's driver is in the CLI's store.
func (bb BuildxBuilder) ContextBuilder() bool { return bb.Driver == "docker" || bb.Driver == "" }

// Status is the builder's state: error when it or any node failed to load,
// running when every node is, else the first node's that is not.
func (bb BuildxBuilder) Status() string {
	if bb.Err != "" {
		return builderError
	}
	status := ""
	for _, n := range bb.Nodes {
		switch {
		case n.Err != "" || n.Status == builderError:
			return builderError
		case status == "" || status == builderRunning:
			status = n.Status
		}
	}
	if status == "" {
		return "unknown"
	}
	return status
}

// Reason is why the builder or its first failing node could not be loaded.
func (bb BuildxBuilder) Reason() string {
	if bb.Err != "" {
		return bb.Err
	}
	for _, n := range bb.Nodes {
		if n.Err != "" {
			return n.Err
		}
	}
	return ""
}

// Endpoint is the first node's endpoint, with how many more there are.
func (bb BuildxBuilder) Endpoint() string {
	return firstOfNodes(bb.Nodes, func(n BuildxNode) string { return n.Endpoint })
}

// BuildKit is the first node's BuildKit version, with how many more nodes.
func (bb BuildxBuilder) BuildKit() string {
	return firstOfNodes(bb.Nodes, func(n BuildxNode) string { return n.Version })
}

// Platforms is every platform any node builds for, in the order the nodes
// list them, each once.
func (bb BuildxBuilder) Platforms() []string {
	var ps []string
	for _, n := range bb.Nodes {
		for _, p := range n.Platforms {
			if !slices.Contains(ps, p) {
				ps = append(ps, p)
			}
		}
	}
	return ps
}

func firstOfNodes(nodes []BuildxNode, f func(BuildxNode) string) string {
	if len(nodes) == 0 {
		return ""
	}
	v := f(nodes[0])
	if len(nodes) > 1 && v != "" {
		v += fmt.Sprintf(" (+%d)", len(nodes)-1)
	}
	return v
}

// Inspect is `buildx inspect <name>`, as the CLI prints it.
func (b *Buildx) Inspect(ctx context.Context, name string) ([]byte, error) {
	return b.buildx(ctx, "inspect", "--", name)
}

// Start boots a builder: `buildx inspect --bootstrap`, the CLI's own way
// (there is no `buildx start`). A docker-container builder may pull its
// BuildKit image first.
func (b *Buildx) Start(ctx context.Context, name string) error {
	_, err := b.buildx(ctx, "inspect", "--bootstrap", "--", name)
	return err
}

// Stop is `buildx stop <name>`.
func (b *Buildx) Stop(ctx context.Context, name string) error {
	_, err := b.buildx(ctx, "stop", "--", name)
	return err
}

// Remove is `buildx rm <name>`: the builder, its node containers and its
// cache. A context's own builder is refused by the caller first.
func (b *Buildx) Remove(ctx context.Context, name string) error {
	_, err := b.buildx(ctx, "rm", "--", name)
	return err
}

// Use is `buildx use <name>`: the builder a plain `docker build` (and
// dockmaster's :build) uses from now on, for the daemon on screen.
func (b *Buildx) Use(ctx context.Context, name string) error {
	_, err := b.buildx(ctx, "use", "--", name)
	return err
}

// Prune is `buildx prune --builder <name> --force`: that builder's build
// cache. It returns the CLI's summary line ("Total: 1.2GB"), "" if none.
func (b *Buildx) Prune(ctx context.Context, name string) (string, error) {
	out, err := b.buildx(ctx, "prune", "--builder="+name, "--force")
	if err != nil {
		return "", err
	}
	return lastMeaningfulLine(string(out)), nil
}

// BuilderSpec is a builder to create, as the builders view's form takes it.
type BuilderSpec struct {
	// Name is the builder's name; "" lets buildx pick one.
	Name string `json:"name,omitempty"`
	// Driver is docker-container (the default), kubernetes or remote.
	Driver string `json:"driver"`
	// Platform is a comma-separated list of platforms, "" for the node's own.
	Platform string `json:"platform,omitempty"`
	// Endpoint is the remote driver's BuildKit address; the others take it
	// from the context, so it is "" for them.
	Endpoint string `json:"endpoint,omitempty"`
}

// BuilderDrivers are the drivers `buildx create` accepts. The docker driver
// is not one: buildx makes it for each context itself.
var BuilderDrivers = []string{"docker-container", "kubernetes", DriverRemote}

// builderName is what buildx accepts as a builder's name.
var builderName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// Check reports what in the spec buildx would refuse, or would read as a
// flag, before anything runs.
func (s BuilderSpec) Check() error {
	if s.Name != "" && !builderName.MatchString(s.Name) {
		return fmt.Errorf("name: %q — letters, digits, and . _ - after the first", s.Name)
	}
	if !slices.Contains(BuilderDrivers, s.Driver) {
		return fmt.Errorf("driver: %q is not one of %s", s.Driver, strings.Join(BuilderDrivers, ", "))
	}
	for _, p := range strings.Split(s.Platform, ",") {
		if p = strings.TrimSpace(p); strings.HasPrefix(p, "-") || strings.ContainsAny(p, " \t") {
			return fmt.Errorf("platform: %q — want os/arch, comma-separated", s.Platform)
		}
	}
	switch {
	case s.Driver == DriverRemote && s.Endpoint == "":
		return errors.New("endpoint: the remote driver needs BuildKit's address (tcp://host:1234)")
	case s.Driver != DriverRemote && s.Endpoint != "":
		return errors.New("endpoint: only the remote driver takes one; the others use the daemon on screen")
	case strings.HasPrefix(s.Endpoint, "-") || strings.ContainsAny(s.Endpoint, " \t"):
		return fmt.Errorf("endpoint: %q is not an address", s.Endpoint)
	}
	return nil
}

// CreateArgs is the argv after `buildx`: every value after = in its own
// argument, so nothing in the spec is read as a flag.
func (s BuilderSpec) CreateArgs() []string {
	args := []string{"--driver=" + s.Driver}
	if s.Name != "" {
		args = append(args, "--name="+s.Name)
	}
	if p := strings.ReplaceAll(s.Platform, " ", ""); p != "" {
		args = append(args, "--platform="+p)
	}
	if s.Endpoint != "" {
		args = append(args, "--", s.Endpoint)
	}
	return args
}

// Create is `buildx create`; it returns the new builder's name, which
// buildx prints (and picks when the spec has none).
func (b *Buildx) Create(ctx context.Context, s BuilderSpec) (string, error) {
	if err := s.Check(); err != nil {
		return "", err
	}
	out, err := b.buildx(ctx, "create", s.CreateArgs()...)
	if err != nil {
		return "", err
	}
	if name := lastMeaningfulLine(string(out)); name != "" {
		return name, nil
	}
	return s.Name, nil
}
