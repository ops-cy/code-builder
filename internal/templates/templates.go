// Package templates 提供内置的多阶段 Dockerfile，当仓库未自带
// Dockerfile 时作为回退方案使用。
//
// 选择依据是仓库根目录下的语言标记文件（见 Detect）。仓库自带的 Dockerfile
// 始终优先于这些模板；仅当仓库没有 Dockerfile 时，构建器才会查阅本包。
package templates

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

//go:embed go.Dockerfile node.Dockerfile vue.Dockerfile java.Dockerfile gradle.Dockerfile python.Dockerfile php.Dockerfile scripts/*.sh
var files embed.FS

// Name 标识一个内置模板。
type Name string

const (
	NameGo     Name = "go"
	NameNode   Name = "node"
	NameVue    Name = "vue"
	NameJava   Name = "java"
	NameGradle Name = "gradle"
	NamePython Name = "python"
	NamePhp    Name = "php"
)

// Template 是一个检测到的内置构建配方。
type Template struct {
	Name    Name
	Content []byte
}

// ScriptPath 是构建器写入模板所调用构建脚本的固定位置（相对于仓库根目录）。
// 模板会原样引用该路径（例如 `RUN bash .gitlab-builder/build.sh 0`）。
const ScriptPath = ".gitlab-builder/build.sh"

// scriptByTemplate 将模板映射到其运行的嵌入式构建脚本。不在此映射中的
// 模板不使用脚本（例如 Gradle）。
var scriptByTemplate = map[Name]string{
	NameGo:     "scripts/build-go.sh",
	NameJava:   "scripts/build-maven.sh",
	NameNode:   "scripts/build-node.sh",
	NameVue:    "scripts/build-node.sh",
	NamePython: "scripts/build-python.sh",
	NamePhp:    "scripts/build-php.sh",
}

// Script 返回指定模板所调用的构建脚本（构建器会将其内容写入 ScriptPath）。
// 当该模板不使用脚本时 ok 为 false。
func Script(name Name) (content []byte, ok bool) {
	path, ok := scriptByTemplate[name]
	if !ok {
		return nil, false
	}
	content, err := files.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return content, true
}

// Detect 检查 repoDir 中的语言标记文件并返回匹配的内置模板。没有标记匹配
// 时返回 ok=false。检测顺序为 Go、Maven、Gradle、package.json（Vue 先于
// 纯 Node），然后是 Python，最后是 PHP（composer.json）。
func Detect(repoDir string) (Template, bool) {
	name, ok := detectName(repoDir)
	if !ok {
		return Template{}, false
	}
	content, err := files.ReadFile(string(name) + ".Dockerfile")
	if err != nil {
		return Template{}, false
	}
	return Template{Name: name, Content: content}, true
}

func detectName(repoDir string) (Name, bool) {
	switch {
	case isFile(filepath.Join(repoDir, "go.mod")):
		return NameGo, true
	case isFile(filepath.Join(repoDir, "pom.xml")):
		return NameJava, true
	case isFile(filepath.Join(repoDir, "build.gradle")) || isFile(filepath.Join(repoDir, "build.gradle.kts")):
		return NameGradle, true
	case isFile(filepath.Join(repoDir, "package.json")):
		if packageHasVue(repoDir) {
			return NameVue, true
		}
		return NameNode, true
	case isPythonProject(repoDir):
		return NamePython, true
	case isFile(filepath.Join(repoDir, "composer.json")):
		return NamePhp, true
	}
	return "", false
}

// isPythonProject 报告仓库根目录是否带有构建脚本能够识别的 Python 依赖
// 标记：requirements.txt、Pipfile 或 pyproject.toml。
func isPythonProject(repoDir string) bool {
	for _, marker := range []string{"requirements.txt", "Pipfile", "pyproject.toml"} {
		if isFile(filepath.Join(repoDir, marker)) {
			return true
		}
	}
	return false
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// packageHasVue 报告 package.json 是否在 dependencies 或 devDependencies 中
// 声明了 Vue —— Vue 本身、Nuxt、@vue/* 作用域包，或任意 vue-* 辅助包。
// 无法解析的 package.json 视为非 Vue。
func packageHasVue(repoDir string) bool {
	data, err := os.ReadFile(filepath.Join(repoDir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	for _, deps := range []map[string]json.RawMessage{pkg.Dependencies, pkg.DevDependencies} {
		for dep := range deps {
			switch {
			case dep == "vue", dep == "nuxt",
				strings.HasPrefix(dep, "@vue/"),
				strings.HasPrefix(dep, "vue-"):
				return true
			}
		}
	}
	return false
}
