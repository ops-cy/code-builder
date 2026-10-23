// Package deploy brings a service up from an already-built local image by
// running `docker compose up -d` against the service's compose file.
//
// Compose files live one directory per service under DeployConfig.ServicesDir.
// The directory must be mounted at the same path inside the builder container
// so compose can resolve relative paths (env_file, bind mounts) correctly.
package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab-builder/internal/config"
)

// execCommand is the exec seam; tests override it to stub the docker CLI.
var execCommand = exec.CommandContext

// deployTimeout bounds one `docker compose up -d`.
const deployTimeout = 5 * time.Minute

// composeCandidates are the standard compose file names tried, in priority
// order, when the configured name is absent. Backup files (e.g.
// ow-bak-docker-compose.yaml) are deliberately never considered.
var composeCandidates = []string{
	"docker-compose.yaml",
	"docker-compose.yml",
	"compose.yaml",
	"compose.yml",
}

// Request is one deployment.
type Request struct {
	// Service is the resolved image name, which also defaults to the service
	// directory name and compose service key.
	Service string
	Config  config.DeployConfig
	Log     io.Writer
}

// Up runs `docker compose -f <file> -p <project> up -d <service>` for the
// service, streaming output to the build log. Deployments sharing a compose
// file are serialised.
func Up(ctx context.Context, req Request) error {
	t, err := resolve(req)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, deployTimeout)
	defer cancel()

	unlock := lockFor(t.composePath)
	defer unlock()

	args := []string{"compose", "-f", t.composePath, "-p", t.project, "up", "-d"}
	if req.Config.Wait {
		wait, err := time.ParseDuration(req.Config.WaitTimeout)
		if err != nil {
			return fmt.Errorf("deploy: invalid wait_timeout %q: %w", req.Config.WaitTimeout, err)
		}
		args = append(args, "--wait", "--wait-timeout", wait.String())
	}
	args = append(args, t.service)

	fmt.Fprintf(req.Log, "==> deploy command: docker %s (dir=%s)\n", strings.Join(args, " "), t.dir)

	var out bytes.Buffer
	cmd := execCommand(ctx, "docker", args...)
	cmd.Dir = t.dir
	cmd.Stdout = io.MultiWriter(req.Log, &out)
	cmd.Stderr = io.MultiWriter(req.Log, &out)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose up -d failed: %w: %s", err, tail(out.String(), 2048))
	}
	return nil
}

// target is the resolved deployment location.
type target struct {
	dir         string // service directory (compose working directory)
	service     string // compose service key
	project     string // compose project name
	composePath string // absolute compose file path
}

// resolve maps a service to its directory, compose file and project, refusing
// any directory that escapes ServicesDir.
func resolve(req Request) (target, error) {
	cfg := req.Config
	service := req.Service
	dirName := req.Service
	project := cfg.ProjectName
	composeName := cfg.ComposeFile

	if ov, ok := cfg.Services[req.Service]; ok {
		if ov.Dir != "" {
			dirName = ov.Dir
		}
		if ov.Service != "" {
			service = ov.Service
		}
		if ov.ProjectName != "" {
			project = ov.ProjectName
		}
		if ov.ComposeFile != "" {
			composeName = ov.ComposeFile
		}
	}
	if project == "" {
		// Compose's own default project name is the directory name.
		project = dirName
	}

	base, err := filepath.Abs(cfg.ServicesDir)
	if err != nil {
		return target{}, fmt.Errorf("deploy: resolve services_dir: %w", err)
	}
	dir, err := filepath.Abs(filepath.Join(base, dirName))
	if err != nil {
		return target{}, fmt.Errorf("deploy: resolve service dir: %w", err)
	}
	if dir != base && !strings.HasPrefix(dir, base+string(os.PathSeparator)) {
		return target{}, fmt.Errorf("deploy: service dir %q escapes services_dir %q", dirName, cfg.ServicesDir)
	}

	composePath, err := findCompose(dir, composeName)
	if err != nil {
		return target{}, err
	}
	return target{dir: dir, service: service, project: project, composePath: composePath}, nil
}

// findCompose returns the compose file in dir: the configured name first, then
// the standard candidates.
func findCompose(dir, configured string) (string, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("deploy: service dir %q does not exist", dir)
	}
	if configured != "" {
		p := filepath.Join(dir, configured)
		if isFile(p) {
			return p, nil
		}
	}
	for _, name := range composeCandidates {
		p := filepath.Join(dir, name)
		if isFile(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("deploy: no compose file in %q (tried %q, %s)",
		dir, configured, strings.Join(composeCandidates, ", "))
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// locks serialise deployments per compose file so two repositories sharing one
// project cannot run up -d concurrently.
var (
	lockMu sync.Mutex
	locks  = make(map[string]*sync.Mutex)
)

func lockFor(key string) func() {
	lockMu.Lock()
	m, ok := locks[key]
	if !ok {
		m = &sync.Mutex{}
		locks[key] = m
	}
	lockMu.Unlock()
	m.Lock()
	return m.Unlock
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
