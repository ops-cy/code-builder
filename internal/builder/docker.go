package builder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// execCommand is the exec seam; tests override it to stub the docker CLI.
var execCommand = exec.CommandContext

// dockerBuildTimeout bounds the docker availability probe plus one build so a
// wedged daemon or huge context cannot occupy a worker slot forever.
const dockerBuildTimeout = 25 * time.Minute

type dockerBuildRequest struct {
	ContextDir string
	Dockerfile string
	Tags       []string // every -t argument (local, plus remote when pushing)
	PushRefs   []string // fully-qualified refs to push after a successful build
	Login      *dockerLogin
	Log        io.Writer
}

// dockerLogin is an optional registry credential; when non-nil a
// `docker login` runs before the build so the subsequent push is authorised.
// The password is fed on stdin and never written to the log.
type dockerLogin struct {
	Registry string
	Username string
	Password string
}

// dockerBuild runs `docker build` against the daemon reached via DOCKER_HOST
// (the mounted Docker socket in the service container), then pushes each
// PushRefs entry when the registry is enabled. The exact commands and their
// full output are streamed to req.Log.
func dockerBuild(ctx context.Context, req dockerBuildRequest) error {
	ctx, cancel := context.WithTimeout(ctx, dockerBuildTimeout)
	defer cancel()

	if err := dockerAvailable(ctx); err != nil {
		return err
	}

	if req.Login != nil {
		if err := dockerLoginRun(ctx, req.Login, req.Log); err != nil {
			return err
		}
	}

	args := []string{"build", "-f", req.Dockerfile}
	for _, tag := range req.Tags {
		args = append(args, "-t", tag)
	}
	args = append(args, req.ContextDir)

	fmt.Fprintf(req.Log, "==> docker command: docker %s\n", strings.Join(args, " "))

	var out bytes.Buffer
	cmd := execCommand(ctx, "docker", args...)
	cmd.Stdout = io.MultiWriter(req.Log, &out)
	cmd.Stderr = io.MultiWriter(req.Log, &out)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w: %s", err, tail(out.String(), 2048))
	}

	for _, ref := range req.PushRefs {
		if err := dockerPush(ctx, ref, req.Log); err != nil {
			return err
		}
	}
	return nil
}

// dockerLoginRun authenticates to the registry with the password on stdin.
func dockerLoginRun(ctx context.Context, l *dockerLogin, logw io.Writer) error {
	fmt.Fprintf(logw, "==> docker login %s (user=%s)\n", l.Registry, l.Username)
	cmd := execCommand(ctx, "docker", "login", l.Registry, "-u", l.Username, "--password-stdin")
	cmd.Stdin = strings.NewReader(l.Password + "\n")
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(logw, &out)
	cmd.Stderr = io.MultiWriter(logw, &out)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login %s failed: %w: %s", l.Registry, err, tail(out.String(), 1024))
	}
	return nil
}

// dockerPush pushes one fully-qualified image reference.
func dockerPush(ctx context.Context, ref string, logw io.Writer) error {
	fmt.Fprintf(logw, "==> docker push %s\n", ref)
	cmd := execCommand(ctx, "docker", "push", ref)
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(logw, &out)
	cmd.Stderr = io.MultiWriter(logw, &out)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker push %s failed: %w: %s", ref, err, tail(out.String(), 2048))
	}
	return nil
}

// dockerAvailable probes the daemon once with a clear error on failure.
func dockerAvailable(ctx context.Context) error {
	var out bytes.Buffer
	cmd := execCommand(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker daemon not reachable (is DOCKER_HOST set / the socket mounted?): %w: %s", err, tail(out.String(), 1024))
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
