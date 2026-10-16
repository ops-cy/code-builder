package builder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
)

// execCommand is the exec seam; tests override it to stub the docker CLI.
var execCommand = exec.CommandContext

type dockerBuildRequest struct {
	ContextDir string
	Dockerfile string
	Tags       []string
	BuildArgs  map[string]string
	NoCache    bool
	Log        io.Writer
}

// dockerBuild runs `docker build` against the daemon reached via DOCKER_HOST
// (the mounted Docker socket in the service container) and streams progress
// to req.Log.
func dockerBuild(ctx context.Context, req dockerBuildRequest) error {
	if err := dockerAvailable(ctx); err != nil {
		return err
	}

	args := []string{"build", "-f", req.Dockerfile}
	if req.NoCache {
		args = append(args, "--no-cache")
	}
	for _, k := range sortedKeys(req.BuildArgs) {
		args = append(args, "--build-arg", fmt.Sprintf("%s=%s", k, req.BuildArgs[k]))
	}
	for _, tag := range req.Tags {
		args = append(args, "-t", tag)
	}
	args = append(args, req.ContextDir)

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

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
