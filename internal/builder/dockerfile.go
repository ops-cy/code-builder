package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gitlab-builder/internal/config"
	"gitlab-builder/internal/templates"
)

// generatedDockerfileName is the file the built-in template is written to
// inside the build context.
const generatedDockerfileName = ".gitlab-builder.Dockerfile"

// ResolveDockerfile decides which Dockerfile to build for the repo at repoDir.
// When spec.Dockerfile names a built-in template, the template is
// materialised into the context; otherwise spec.Dockerfile is treated as a
// repo-relative path. Returns the Dockerfile path and the effective context.
func ResolveDockerfile(_ context.Context, repoDir string, spec config.BuildSpec) (dockerfilePath, effectiveContext string, err error) {
	contextDir := filepath.Join(repoDir, spec.Context)
	if templates.Has(spec.Dockerfile) {
		content, err := templates.Get(spec.Dockerfile)
		if err != nil {
			return "", "", err
		}
		if err := os.MkdirAll(contextDir, 0o755); err != nil {
			return "", "", fmt.Errorf("mkdir context %s: %w", contextDir, err)
		}
		out := filepath.Join(contextDir, generatedDockerfileName)
		if err := os.WriteFile(out, content, 0o644); err != nil {
			return "", "", fmt.Errorf("write generated dockerfile: %w", err)
		}
		return out, contextDir, nil
	}

	abs := filepath.Join(repoDir, spec.Dockerfile)
	if _, err := os.Stat(abs); err != nil {
		return "", "", fmt.Errorf("dockerfile not found: %s (repo has no such file and it is not a built-in template %v)", spec.Dockerfile, templates.Names())
	}
	return abs, contextDir, nil
}
