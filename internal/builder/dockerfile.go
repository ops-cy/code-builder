package builder

import (
	"fmt"
	"os"
	"path/filepath"

	"gitlab-builder/internal/templates"
)

// resolveDockerfile 为已克隆的仓库选择构建配方：
//
//  1. 仓库根目录下自带的 Dockerfile（若存在）；
//  2. 否则使用内置语言模板（Go / Node / Vue / Java / Gradle），由仓库中的标记文件选出，
//     并写到仓库根目录，以保持构建上下文统一。委托给构建脚本的模板还会把该脚本写入
//     templates.ScriptPath。
//
// 返回的 source 描述本次选择，用于构建日志（"repository" 或 "template:<name>"）。
// 既没有自带 Dockerfile、也没有可识别语言标记的仓库会明确报错，而不是靠猜测。
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
