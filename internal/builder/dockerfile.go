package builder

import (
	"fmt"
	"os"
	"path/filepath"

	"gitlab-builder/internal/templates"
)

// resolveDockerfile picks the build recipe for a cloned repository:
//
//  1. the repository's own Dockerfile at the repo root, when present;
//  2. otherwise a built-in language template (Go / Node / Vue / Java / Gradle)
//     selected from the repo's marker files, materialised at the repo root so
//     the build context stays uniform. Templates that delegate to a build
//     script also get that script written to templates.ScriptPath.
//
// The returned source describes the choice for the build log ("repository" or
// "template:<name>"). A repo with neither its own Dockerfile nor a recognised
// language marker fails explicitly instead of guessing.
func resolveDockerfile(repoDir string) (path, source string, err error) {
	p := filepath.Join(repoDir, "Dockerfile")
	if st, statErr := os.Stat(p); statErr == nil && !st.IsDir() {
		return p, "repository", nil
	}

	tpl, ok := templates.Detect(repoDir)
	if !ok {
		return "", "", fmt.Errorf(
			"no Dockerfile at repository root and no built-in template matched (Go/Node/Vue/Java/Gradle): %s", repoDir)
	}
	if err := os.WriteFile(p, tpl.Content, 0o644); err != nil {
		return "", "", fmt.Errorf("write built-in template %q: %w", tpl.Name, err)
	}
	if script, hasScript := templates.Script(tpl.Name); hasScript {
		scriptAbs := filepath.Join(repoDir, filepath.FromSlash(templates.ScriptPath))
		if err := os.MkdirAll(filepath.Dir(scriptAbs), 0o755); err != nil {
			return "", "", fmt.Errorf("create build-script dir for template %q: %w", tpl.Name, err)
		}
		if err := os.WriteFile(scriptAbs, script, 0o644); err != nil {
			return "", "", fmt.Errorf("write build script for template %q: %w", tpl.Name, err)
		}
	}
	return p, "template:" + string(tpl.Name), nil
}
