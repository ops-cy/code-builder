// Package stack 从仓库根目录识别语言、框架、构建工具与版本。
package stack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"code-builder/internal/config"
)

// Stack 是仓库根目录的技术栈检测结果。
type Stack struct {
	Language       string // go / rust / java / node / unknown
	Framework      string // spring / next / nuxt / vue / react / angular / vite
	BuildTool      string // go / cargo / maven / gradle / npm / pnpm / yarn / bun
	BuildToolChain string // go / rust / java / gradle / node / vue
	GoVersion      string
	RustVersion    string
	JavaVersion    string
	NodeVersion    string
	PackageManager string
}

var (
	javaVersionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<java\.version>\s*([0-9][0-9.]*)\s*</java\.version>`),
		regexp.MustCompile(`(?is)<maven\.compiler\.source>\s*([0-9][0-9.]*)\s*</maven\.compiler\.source>`),
		regexp.MustCompile(`(?is)<maven\.compiler\.target>\s*([0-9][0-9.]*)\s*</maven\.compiler\.target>`),
		regexp.MustCompile(`(?is)<maven\.compiler\.release>\s*([0-9][0-9.]*)\s*</maven\.compiler\.release>`),
		regexp.MustCompile(`(?is)<release>\s*([0-9][0-9.]*)\s*</release>`),
	}
	gradleJavaPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)sourceCompatibility\s*=\s*JavaVersion\.VERSION_([0-9]+)`),
		regexp.MustCompile(`(?i)targetCompatibility\s*=\s*JavaVersion\.VERSION_([0-9]+)`),
		regexp.MustCompile(`(?i)sourceCompatibility\s*=\s*['"]?([0-9][0-9.]*)`),
		regexp.MustCompile(`(?i)targetCompatibility\s*=\s*['"]?([0-9][0-9.]*)`),
		regexp.MustCompile(`(?i)JavaLanguageVersion\.of\(\s*([0-9]+)\s*\)`),
		regexp.MustCompile(`(?i)languageVersion\s*=\s*JavaLanguageVersion\.of\(\s*([0-9]+)\s*\)`),
	}
	nodeVersionPattern = regexp.MustCompile(`([0-9]+)`)
	goVersionPattern   = regexp.MustCompile(`(?m)^\s*go\s+([0-9]+\.[0-9]+)`)
)

// Analyze 检测仓库根目录。只读取根目录标记文件，不做 monorepo 扫描。
func Analyze(repoDir string, cfg config.BuildStackConfig) Stack {
	s := Stack{
		Language:       "unknown",
		BuildToolChain: "",
		GoVersion:      cfg.GoVersion,
		RustVersion:    cfg.RustVersion,
		JavaVersion:    cfg.JavaVersion,
		NodeVersion:    cfg.NodeVersion,
		PackageManager: cfg.PackageManager,
	}
	if s.GoVersion == "" {
		s.GoVersion = "1.25"
	}
	if s.RustVersion == "" {
		s.RustVersion = "1.83"
	}
	if s.PackageManager == "" || s.PackageManager == "auto" {
		s.PackageManager = "auto"
	}

	switch {
	case isFile(repoDir, "go.mod"):
		s.Language = "go"
		s.BuildTool = "go"
		s.BuildToolChain = "go"
		if cfg.GoVersion == "" {
			if data, err := os.ReadFile(filepath.Join(repoDir, "go.mod")); err == nil {
				if m := goVersionPattern.FindSubmatch(data); len(m) > 1 {
					s.GoVersion = string(m[1])
				}
			}
		}
	case isFile(repoDir, "Cargo.toml"):
		s.Language = "rust"
		s.BuildTool = "cargo"
		s.BuildToolChain = "rust"
	case isFile(repoDir, "pom.xml"):
		s.Language = "java"
		s.BuildTool = "maven"
		s.BuildToolChain = "java"
		s.Framework = detectJavaFramework(repoDir)
		if cfg.JavaVersion == "" {
			s.JavaVersion = detectJavaVersion(repoDir, "pom.xml", cfg)
		} else {
			s.JavaVersion = normalizeJavaVersion(cfg.JavaVersion, cfg.JavaMinVersion)
		}
	case isFile(repoDir, "build.gradle") || isFile(repoDir, "build.gradle.kts"):
		s.Language = "java"
		s.BuildTool = "gradle"
		s.BuildToolChain = "gradle"
		s.Framework = detectJavaFramework(repoDir)
		name := "build.gradle"
		if isFile(repoDir, "build.gradle.kts") {
			name = "build.gradle.kts"
		}
		if cfg.JavaVersion == "" {
			s.JavaVersion = detectJavaVersion(repoDir, name, cfg)
		} else {
			s.JavaVersion = normalizeJavaVersion(cfg.JavaVersion, cfg.JavaMinVersion)
		}
	case isFile(repoDir, "package.json"):
		s.Language = "node"
		s.BuildTool = "node"
		s.BuildToolChain = "node"
		analyzeNode(repoDir, &s, cfg)
	}

	if s.Language == "java" && s.JavaVersion == "" {
		s.JavaVersion = "21"
	}
	return s
}

func analyzeNode(repoDir string, s *Stack, cfg config.BuildStackConfig) {
	data, err := os.ReadFile(filepath.Join(repoDir, "package.json"))
	if err != nil {
		return
	}
	var pkg struct {
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
		Volta           struct {
			Node string `json:"node"`
		} `json:"volta"`
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
		PackageManager string `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return
	}

	has := func(name string) bool {
		if _, ok := pkg.Dependencies[name]; ok {
			return true
		}
		_, ok := pkg.DevDependencies[name]
		return ok
	}
	switch {
	case has("next") || isFile(repoDir, "next.config.js") || isFile(repoDir, "next.config.mjs"):
		s.Framework = "next"
	case has("nuxt") || isFile(repoDir, "nuxt.config.js") || isFile(repoDir, "nuxt.config.ts"):
		s.Framework = "nuxt"
	case has("@angular/core") || isFile(repoDir, "angular.json"):
		s.Framework = "angular"
	case has("vue") || isFile(repoDir, "vue.config.js"):
		s.Framework = "vue"
	case has("react"):
		s.Framework = "react"
	case has("svelte") || has("@sveltejs/kit"):
		s.Framework = "svelte"
	case has("vite") || isFile(repoDir, "vite.config.js") || isFile(repoDir, "vite.config.ts"):
		s.Framework = "vite"
	}
	if s.Framework != "" && s.Framework != "next" && s.Framework != "nuxt" {
		s.BuildToolChain = "vue"
	}

	if cfg.NodeVersion == "" {
		if v := nodeVersion(pkg.Volta.Node); v != "" {
			s.NodeVersion = v
		} else if v := nodeVersion(pkg.Engines.Node); v != "" {
			s.NodeVersion = v
		}
	}
	if s.NodeVersion == "" {
		s.NodeVersion = "22"
	}

	if cfg.PackageManager == "" || cfg.PackageManager == "auto" {
		if pkg.PackageManager != "" {
			s.PackageManager = strings.SplitN(pkg.PackageManager, "@", 2)[0]
		} else {
			s.PackageManager = detectPackageManager(repoDir)
		}
	}
	if s.PackageManager == "" {
		s.PackageManager = "auto"
	}
}

func detectPackageManager(repoDir string) string {
	switch {
	case isFile(repoDir, "pnpm-lock.yaml"):
		return "pnpm"
	case isFile(repoDir, "yarn.lock"):
		return "yarn"
	case isFile(repoDir, "bun.lockb") || isFile(repoDir, "bun.lock"):
		return "bun"
	case isFile(repoDir, "package-lock.json"):
		return "npm"
	default:
		return "npm"
	}
}

func detectJavaVersion(repoDir, file string, cfg config.BuildStackConfig) string {
	data, err := os.ReadFile(filepath.Join(repoDir, file))
	if err != nil {
		return ""
	}
	var raw string
	if file == "pom.xml" {
		for _, p := range javaVersionPatterns {
			if m := p.FindSubmatch(data); len(m) > 1 {
				raw = string(m[1])
				break
			}
		}
	} else {
		for _, p := range gradleJavaPatterns {
			if m := p.FindSubmatch(data); len(m) > 1 {
				raw = string(m[1])
				break
			}
		}
	}
	return normalizeJavaVersion(raw, cfg.JavaMinVersion)
}

func normalizeJavaVersion(raw, min string) string {
	major := majorVersion(raw)
	if major == 0 {
		return ""
	}
	supported := []int{8, 11, 17, 21}
	chosen := supported[len(supported)-1]
	for _, v := range supported {
		if major <= v {
			chosen = v
			break
		}
	}
	if m := majorVersion(min); m > chosen {
		for _, v := range supported {
			if v >= m {
				chosen = v
				break
			}
		}
	}
	return strconv.Itoa(chosen)
}

func majorVersion(raw string) int {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "1.") {
		raw = raw[2:]
	}
	m := nodeVersionPattern.FindString(raw)
	if m == "" {
		return 0
	}
	n, _ := strconv.Atoi(m)
	return n
}

func nodeVersion(raw string) string {
	major := majorVersion(raw)
	switch major {
	case 18, 20, 22, 24:
		return strconv.Itoa(major)
	default:
		return ""
	}
}

func detectJavaFramework(repoDir string) string {
	for _, name := range []string{"pom.xml", "build.gradle", "build.gradle.kts"} {
		data, err := os.ReadFile(filepath.Join(repoDir, name))
		content := string(data)
		if err == nil && (strings.Contains(content, "spring-boot") || strings.Contains(content, "org.springframework.boot")) {
			return "spring"
		}
	}
	return ""
}

func isFile(dir, name string) bool {
	st, err := os.Stat(filepath.Join(dir, name))
	return err == nil && !st.IsDir()
}
