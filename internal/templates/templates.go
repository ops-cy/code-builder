// Package templates provides built-in, multi-stage Dockerfiles used as a
// fallback when a repository does not ship one of its own.
//
// Selection is by language marker files at the repository root (see Detect). A
// repository-provided Dockerfile always takes precedence over these templates;
// the builder only consults this package when the repo has none.
package templates

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

//go:embed go.Dockerfile node.Dockerfile vue.Dockerfile java.Dockerfile gradle.Dockerfile scripts/*.sh
var files embed.FS

// Name identifies a built-in template.
type Name string

const (
	NameGo     Name = "go"
	NameNode   Name = "node"
	NameVue    Name = "vue"
	NameJava   Name = "java"
	NameGradle Name = "gradle"
)

// Template is a detected built-in build recipe.
type Template struct {
	Name    Name
	Content []byte
}

// ScriptPath is the fixed location, relative to the repository root, where the
// builder materialises the build script a template invokes. Templates reference
// this path verbatim (e.g. `RUN bash .gitlab-builder/build.sh 0`).
const ScriptPath = ".gitlab-builder/build.sh"

// scriptByTemplate maps a template to the embedded build script it runs. A
// template absent from this map uses no script (e.g. Gradle).
var scriptByTemplate = map[Name]string{
	NameGo:   "scripts/build-go.sh",
	NameJava: "scripts/build-maven.sh",
	NameNode: "scripts/build-node.sh",
	NameVue:  "scripts/build-node.sh",
}

// Script returns the build script that the named template invokes (its content
// is written to ScriptPath by the builder). ok is false when the template uses
// no script.
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

// Detect inspects repoDir for language marker files and returns the matching
// built-in template. It returns ok=false when no marker matches. Detection
// order is Go, Maven, Gradle, then package.json (Vue before plain Node).
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
	}
	return "", false
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// packageHasVue reports whether package.json declares Vue — Vue itself, Nuxt, a
// @vue/* scoped package, or any vue-* helper — in dependencies or
// devDependencies. Unparseable package.json is treated as non-Vue.
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
