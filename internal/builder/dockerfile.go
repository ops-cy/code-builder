package builder

import (
	"fmt"
	"os"
	"path/filepath"

	"code-builder/internal/templates"
)

// resolveDockerfile 为已克隆的仓库选择构建配方：
//
//  1. 仓库根目录下自带的 Dockerfile（若存在）；
//  2. 否则使用内置语言模板（Go / Rust / Java / Gradle / Node / Vue），由仓库中的
//     标记文件选出，并写到仓库根目录；
//  3. 两者都没有时明确报错，而不是靠猜测。
//
// 使用内置模板时还会写入 .code-builder/build.sh；仓库没有 .dockerignore 时
// 自动生成一份默认规则（不会排除 .code-builder）。
func resolveDockerfile(repoDir string) (path, source string, err error) {
	p := filepath.Join(repoDir, "Dockerfile")
	if st, statErr := os.Stat(p); statErr == nil && !st.IsDir() {
		return p, "repository", nil
	}

	tpl, ok := templates.Detect(repoDir)
	if !ok {
		return "", "", fmt.Errorf(
			"no Dockerfile at repository root and no built-in template matched (Go/Rust/Java/Gradle/Node/Vue): %s", repoDir)
	}
	if templates.DockerignoreExcludesBuildDir(repoDir) {
		return "", "", fmt.Errorf(
			"repository .dockerignore excludes %s, which is required by the built-in %q template; "+
				"remove that rule or provide your own Dockerfile", templates.ScriptPath, tpl.Name)
	}
	if _, err := templates.EnsureDockerignore(repoDir); err != nil {
		return "", "", fmt.Errorf("write default .dockerignore: %w", err)
	}
	if err := os.WriteFile(p, tpl.Content, 0o644); err != nil {
		return "", "", fmt.Errorf("write built-in template %q: %w", tpl.Name, err)
	}
	if script, hasScript := templates.Script(tpl.Name); hasScript {
		scriptAbs := filepath.Join(repoDir, filepath.FromSlash(templates.ScriptPath))
		if err := os.MkdirAll(filepath.Dir(scriptAbs), 0o755); err != nil {
			return "", "", fmt.Errorf("create build-script dir for template %q: %w", tpl.Name, err)
		}
		if err := os.WriteFile(scriptAbs, script, 0o755); err != nil {
			return "", "", fmt.Errorf("write build script for template %q: %w", tpl.Name, err)
		}
	}
	if entrypoint, ok := templates.Entrypoint(tpl.Name); ok {
		entryAbs := filepath.Join(repoDir, filepath.FromSlash(templates.EntrypointPath))
		if err := os.WriteFile(entryAbs, entrypoint, 0o755); err != nil {
			return "", "", fmt.Errorf("write entrypoint for template %q: %w", tpl.Name, err)
		}
	}
	return p, "template:" + string(tpl.Name), nil
}
