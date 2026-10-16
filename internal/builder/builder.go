// Package builder clones a repository at an exact commit and builds a local
// Docker image from it via the docker CLI (talking to the daemon through the
// mounted Docker socket).
package builder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab-builder/internal/config"
	"gitlab-builder/internal/gitx"
)

// Request is the resolved build input.
type Request struct {
	Repo      config.Repository
	Branch    string
	SHA       string
	Log       *os.File
	Workspace string
	// Registry is the optional push target; when its URL is empty the image
	// stays local-only.
	Registry config.RegistryConfig
}

// Build clones the repo at SHA, selects the build recipe (the repository's own
// root Dockerfile, else a built-in language template), computes the sha8 tag,
// and runs docker build. When req.Registry is enabled the image is also tagged
// with a fully-qualified remote ref and pushed after a successful build.
// Progress is written to req.Log: clone timings, the chosen dockerfile source,
// the docker command and its output, the final image tags. Errors never contain
// credentials (URLs are masked, registry passwords are never logged).
func Build(ctx context.Context, req Request) ([]string, error) {
	fmt.Fprintf(req.Log, "==> clone start repo=%s url=%s branch=%s sha=%s\n",
		req.Repo.Name, gitx.MaskURL(req.Repo.URL), req.Branch, req.SHA)

	dest, err := workspaceDir(req.Workspace, req.Repo, req.SHA)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(dest); err != nil {
		return nil, fmt.Errorf("builder: clean workspace %s: %w", dest, err)
	}
	defer func() { _ = os.RemoveAll(dest) }()

	cloneStart := time.Now()
	if err := gitx.CloneAtSHA(ctx, gitx.CloneReq{
		URL:        req.Repo.URL,
		Branch:     req.Branch,
		SHA:        req.SHA,
		Dest:       dest,
		DeployKey:  req.Repo.DeployKeyPath,
		KnownHosts: filepath.Join(req.Workspace, ".ssh", "known_hosts"),
	}, req.Log); err != nil {
		return nil, fmt.Errorf("builder: clone: %w", err)
	}
	fmt.Fprintf(req.Log, "==> clone done duration=%s\n", time.Since(cloneStart).Round(time.Millisecond))

	dockerfileAbs, dockerfileSource, err := resolveDockerfile(dest)
	if err != nil {
		return nil, fmt.Errorf("builder: %w", err)
	}
	fmt.Fprintf(req.Log, "==> dockerfile source=%s path=%s\n", dockerfileSource, dockerfileAbs)

	tags, pushRefs, login, err := imageRefs(req.Repo.Build.Image, req.SHA, req.Registry)
	if err != nil {
		return nil, fmt.Errorf("builder: %w", err)
	}
	if len(pushRefs) > 0 {
		fmt.Fprintf(req.Log, "==> registry push enabled remote=%s\n", pushRefs[0])
	}

	if err := dockerBuild(ctx, dockerBuildRequest{
		ContextDir: dest,
		Dockerfile: dockerfileAbs,
		Tags:       tags,
		PushRefs:   pushRefs,
		Login:      login,
		Log:        req.Log,
	}); err != nil {
		return nil, fmt.Errorf("builder: docker build: %w", err)
	}
	fmt.Fprintf(req.Log, "==> image tags: %s (full sha %s)\n", strings.Join(tags, ", "), req.SHA)
	return tags, nil
}

// imageRefs turns a repository image name and commit sha into the full set of
// docker tag arguments, the subset to push, and the optional registry login. It
// is pure (no docker, no git) so the push composition is unit-testable:
//   - local tag          {image}:{sha8}                     (always)
//   - remote ref         {url}[/{project}]/{image}:{sha8}   (only when enabled)
//   - login              set only when a username is configured
func imageRefs(image, sha string, reg config.RegistryConfig) (tags, pushRefs []string, login *dockerLogin, err error) {
	tags, err = TagsFor(image, sha)
	if err != nil {
		return nil, nil, nil, err
	}
	if !reg.Enabled() {
		return tags, nil, nil, nil
	}
	remote := reg.RemoteRef(image, sha[:8])
	tags = append(tags, remote)
	pushRefs = append(pushRefs, remote)
	if reg.Username != "" {
		login = &dockerLogin{
			Registry: reg.URL,
			Username: reg.Username,
			Password: reg.Password,
		}
	}
	return tags, pushRefs, login, nil
}

// TagsFor computes the single image tag: {image}:{first 8 chars of the full
// commit SHA}. The SHA must already be a full hex object id; the same
// validation that guards webhook acceptance is re-checked defensively here.
func TagsFor(image, sha string) ([]string, error) {
	if len(sha) < 8 {
		return nil, fmt.Errorf("commit sha %q too short for an 8-char tag", sha)
	}
	for _, r := range sha[:8] {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex {
			return nil, fmt.Errorf("commit sha %q is not hexadecimal", sha)
		}
	}
	return []string{fmt.Sprintf("%s:%s", image, sha[:8])}, nil
}

// workspaceDir returns the per-task clone directory:
// {workspace}/{name}-{repoHash}-{sha8}. The name is pre-sanitised, but the
// result is re-checked against workspace root so no event-supplied value can
// ever escape the workspace (path traversal defence in depth).
func workspaceDir(workspace string, repo config.Repository, sha string) (string, error) {
	repoHash := sha256hex(repo.Key)[:8]
	short := sha
	if len(short) > 8 {
		short = short[:8]
	}
	dir := fmt.Sprintf("%s-%s-%s", repo.Name, repoHash, short)
	dest := filepath.Join(workspace, dir)
	root := filepath.Clean(workspace)
	abs, err := filepath.Abs(dest)
	if err != nil {
		return "", fmt.Errorf("builder: resolve workspace path: %w", err)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("builder: resolve workspace root: %w", err)
	}
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("builder: workspace path %q escapes root %q", dest, workspace)
	}
	return dest, nil
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
