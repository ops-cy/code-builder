package builder

import (
	"bytes"
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
// spec.Dockerfile "auto" prefers the repo's own root Dockerfile and otherwise
// detects a built-in template from language markers. When spec.Dockerfile
// names a built-in template, the template is materialised into the context;
// otherwise it is treated as a repo-relative path. Returns the Dockerfile
// path and the effective context.
func ResolveDockerfile(_ context.Context, repoDir string, spec config.BuildSpec) (dockerfilePath, effectiveContext string, err error) {
	contextDir := filepath.Join(repoDir, spec.Context)
	name := spec.Dockerfile
	if name == "auto" {
		if name, err = detectDockerfile(repoDir); err != nil {
			return "", "", err
		}
	}
	if templates.Has(name) {
		content, err := templates.Get(name)
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

	abs := filepath.Join(repoDir, name)
	if _, err := os.Stat(abs); err != nil {
		return "", "", fmt.Errorf("dockerfile not found: %s (repo has no such file and it is not a built-in template %v)", name, templates.Names())
	}
	return abs, contextDir, nil
}

// detectDockerfile resolves dockerfile: auto for one repository. The repo's
// own root Dockerfile always wins; otherwise the first matching language
// marker selects a built-in template: go.mod → Go, Maven/Gradle → Java,
// package.json → Vue when it depends on "vue", else Node.
func detectDockerfile(repoDir string) (string, error) {
	if fileExists(filepath.Join(repoDir, "Dockerfile")) {
		return "Dockerfile", nil
	}
	switch {
	case fileExists(filepath.Join(repoDir, "go.mod")):
		return "go.Dockerfile", nil
	case fileExists(filepath.Join(repoDir, "pom.xml")),
		fileExists(filepath.Join(repoDir, "build.gradle")),
		fileExists(filepath.Join(repoDir, "build.gradle.kts")):
		return "java.Dockerfile", nil
	case fileExists(filepath.Join(repoDir, "package.json")):
		if packageJSONHasVue(filepath.Join(repoDir, "package.json")) {
			return "vue.Dockerfile", nil
		}
		return "node.Dockerfile", nil
	}
	return "", fmt.Errorf("dockerfile auto detection failed: repo root has no Dockerfile, go.mod, pom.xml, build.gradle, or package.json; set build.dockerfile explicitly")
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// packageJSONHasVue reports whether package.json mentions "vue" as a JSON key
// (dependency or devDependency). Good enough to split vue.Dockerfile from
// node.Dockerfile; mixed repos should set build.dockerfile explicitly.
func packageJSONHasVue(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Contains(b, []byte(`"vue"`))
}
