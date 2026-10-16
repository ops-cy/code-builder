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
	Tags       []string
	Log        io.Writer
}

// dockerBuild runs `docker build` against the daemon reached via DOCKER_HOST
// (the mounted Docker socket in the service container). The exact command
// and its full output are streamed to req.Log.
func dockerBuild(ctx context.Context, req dockerBuildRequest) error {
	ctx, cancel := context.WithTimeout(ctx, dockerBuildTimeout)
	defer cancel()

	if err := dockerAvailable(ctx); err != nil {
		return err
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
