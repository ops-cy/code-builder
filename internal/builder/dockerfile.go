package builder

import (
	"fmt"
	"os"
	"path/filepath"
)

// resolveDockerfile requires the repository's own Dockerfile at the repo
// root — the build context root as well. There is no template fallback: a
// repository without a Dockerfile fails with an explicit error.
func resolveDockerfile(repoDir string) (string, error) {
	p := filepath.Join(repoDir, "Dockerfile")
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return "", fmt.Errorf("no Dockerfile at repository root: %s (every repository must provide its own Dockerfile)", p)
	}
	return p, nil
}
