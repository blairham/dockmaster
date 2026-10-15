// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// BuildSpec is one image build: a context directory, the Dockerfile in it
// (or elsewhere), and what `docker build` takes besides.
type BuildSpec struct {
	// Context is the build context, an absolute directory.
	Context string `json:"context"`
	// Dockerfile is the file to build, relative to Context or absolute;
	// "" is the context's own Dockerfile.
	Dockerfile string `json:"dockerfile,omitempty"`
	// Tag names the image; "" leaves it untagged, as `docker build` does.
	Tag    string `json:"tag,omitempty"`
	Target string `json:"target,omitempty"`
	// BuildArgs are KEY=value pairs, as after --build-arg.
	BuildArgs []string `json:"buildArgs,omitempty"`
}

// buildArgKey is what docker accepts before the = of a build arg: the
// shell-variable shape a Dockerfile's ARG can name.
var buildArgKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Check reports what in the spec could never build, before a builder is
// started: a context that is not a directory, a Dockerfile that is not
// there, a build arg without a key, and any value that docker would read
// as a flag.
func (s BuildSpec) Check() error {
	if s.Context == "" {
		return errors.New("context: a directory to build from is needed")
	}
	if st, err := os.Stat(s.Context); err != nil || !st.IsDir() {
		return fmt.Errorf("context: %s is not a directory", s.Context)
	}
	if f := s.DockerfilePath(); f != "" {
		if st, err := os.Stat(f); err != nil || st.IsDir() {
			return fmt.Errorf("dockerfile: %s is not a file", f)
		}
	}
	for name, v := range map[string]string{"tag": s.Tag, "target": s.Target} {
		switch {
		case strings.HasPrefix(v, "-"):
			return fmt.Errorf("%s: %q starts with -", name, v)
		case strings.ContainsAny(v, " \t\n"):
			return fmt.Errorf("%s: %q has a space in it", name, v)
		}
	}
	for _, a := range s.BuildArgs {
		k, _, ok := strings.Cut(a, "=")
		if !ok || !buildArgKey.MatchString(k) {
			return fmt.Errorf("build arg %q: want KEY=value", a)
		}
	}
	return nil
}

// DockerfilePath is the Dockerfile the build reads, absolute: Dockerfile
// as given, joined to the context when it is relative. "" is the
// context's own Dockerfile.
func (s BuildSpec) DockerfilePath() string {
	switch {
	case s.Dockerfile == "":
		return filepath.Join(s.Context, "Dockerfile")
	case filepath.IsAbs(s.Dockerfile):
		return s.Dockerfile
	}
	return filepath.Join(s.Context, s.Dockerfile)
}

// Builder is how a build is run: buildx, or the CLI's own `docker build`
// where the buildx plugin is not installed.
type Builder int

// The builders.
const (
	BuilderBuildx Builder = iota
	BuilderClassic
)

func (b Builder) String() string {
	if b == BuilderClassic {
		return "docker build"
	}
	return "docker buildx build"
}

// DetectBuilder asks the docker CLI at bin whether its buildx plugin is
// there: `docker buildx version` exits zero when it is.
func DetectBuilder(ctx context.Context, bin string) Builder {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if exec.CommandContext(ctx, bin, "buildx", "version").Run() == nil { //nolint:gosec // fixed binary and verb
		return BuilderBuildx
	}
	return BuilderClassic
}

// BuildCommand is the command that runs spec with b against the daemon
// endpoint names (Client.EndpointArgs), as the compose actions are run:
// the docker CLI, so a build is the one the user would get from a shell,
// BuildKit, cache and all. Every value goes after = in its own argument,
// and the context is absolute, so nothing in the spec is read as a flag.
//
// Progress is plain text — the TTY renderer redraws in place, which a log
// cannot show — and buildx is told to --load, so the image lands in the
// daemon whichever driver the current builder uses.
func BuildCommand(bin string, endpoint []string, b Builder, spec BuildSpec) CommandSpec {
	args := append([]string{bin}, endpoint...)
	if b == BuilderBuildx {
		args = append(args, "buildx", "build", "--progress=plain", "--load")
	} else {
		args = append(args, "build")
	}
	if spec.Dockerfile != "" {
		args = append(args, "--file="+spec.DockerfilePath())
	}
	if spec.Tag != "" {
		args = append(args, "--tag="+spec.Tag)
	}
	if spec.Target != "" {
		args = append(args, "--target="+spec.Target)
	}
	for _, a := range spec.BuildArgs {
		args = append(args, "--build-arg="+a)
	}
	args = append(args, spec.Context)
	return CommandSpec{
		Args: args,
		Dir:  spec.Context,
		// The classic builder has no --progress before BuildKit became its
		// default; the variable works for both.
		Env: append([]string{"BUILDKIT_PROGRESS=plain"}, endpointEnv(endpoint)...),
	}
}

// endpointEnv keeps the environment from contradicting the endpoint
// flags: a DOCKER_HOST in it beside --context is an error to the CLI, and
// a plugin (buildx, compose) reads DOCKER_HOST before it reads --host. So
// DOCKER_HOST is set to the --host value, or emptied for a --context.
func endpointEnv(endpoint []string) []string {
	for i := 0; i+1 < len(endpoint); i++ {
		switch endpoint[i] {
		case "--host":
			return []string{"DOCKER_HOST=" + endpoint[i+1]}
		case "--context":
			return []string{"DOCKER_HOST="}
		}
	}
	return nil
}

// BuildTitle names a build for a view's title and a saved log: the tag,
// else the context directory's name.
func (s BuildSpec) BuildTitle() string {
	if s.Tag != "" {
		return s.Tag
	}
	return filepath.Base(s.Context)
}

// dockerfileName matches the names a Dockerfile goes by: Dockerfile and
// Containerfile, and the per-target variants people keep beside them
// (Dockerfile.dev, api.Dockerfile, build.dockerfile).
var dockerfileName = regexp.MustCompile(`(?i)^(dockerfile|containerfile)(\..+)?$|^.+\.(dockerfile|containerfile)$`)

// IsDockerfile reports whether name is a Dockerfile by its name.
func IsDockerfile(name string) bool { return dockerfileName.MatchString(name) }
