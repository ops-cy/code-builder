// Package builder clones a repository at an exact commit and builds a local
// Docker image from it via the docker CLI (talking to the daemon through the
// mounted Docker socket).
package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
}

// Build clones the repo at SHA, resolves the Dockerfile, computes tags, and
// runs docker build. Progress is written to req.Log.
func Build(ctx context.Context, req Request) ([]string, error) {
	short := shortSHA(req.SHA)
	fmt.Fprintf(req.Log, "==> build repo=%s branch=%s sha=%s\n", req.Repo.Name, req.Branch, req.SHA)

	dest := filepath.Join(req.Workspace, req.Repo.Name+"-"+short)
	if err := os.RemoveAll(dest); err != nil {
		return nil, fmt.Errorf("builder: clean workspace %s: %w", dest, err)
	}
	defer func() { _ = os.RemoveAll(dest) }()

	if err := gitx.CloneAtSHA(ctx, gitx.CloneReq{
		URL:       req.Repo.URL,
		Branch:    req.Branch,
		SHA:       req.SHA,
		Dest:      dest,
		DeployKey: req.Repo.DeployKeyPath,
	}, req.Log); err != nil {
		return nil, fmt.Errorf("builder: clone: %w", err)
	}

	dockerfileAbs, effectiveContext, err := ResolveDockerfile(ctx, dest, req.Repo.Build)
	if err != nil {
		return nil, fmt.Errorf("builder: resolve dockerfile: %w", err)
	}

	tags := TagsFor(req.Repo.Build.Image, req.Branch, req.SHA, req.Repo.Build.TagLatest)
	err = dockerBuild(ctx, dockerBuildRequest{
		ContextDir: effectiveContext,
		Dockerfile: dockerfileAbs,
		Tags:       tags,
		BuildArgs:  req.Repo.Build.Args,
		NoCache:    req.Repo.Build.NoCache,
		Log:        req.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("builder: docker build: %w", err)
	}
	fmt.Fprintf(req.Log, "==> built tags: %s\n", strings.Join(tags, ", "))
	return tags, nil
}

// TagsFor computes the image tags: {image}:{branch with / replaced by -}-
// {12-char short SHA}, plus :latest when tagLatest is set.
func TagsFor(image, branch, sha string, tagLatest bool) []string {
	sanitized := strings.ReplaceAll(branch, "/", "-")
	tags := []string{fmt.Sprintf("%s:%s-%s", image, sanitized, shortSHA(sha))}
	if tagLatest {
		tags = append(tags, image+":latest")
	}
	return tags
}

func shortSHA(sha string) string {
	if len(sha) >= 12 {
		return sha[:12]
	}
	return sha
}
