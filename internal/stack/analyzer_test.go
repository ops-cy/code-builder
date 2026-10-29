package stack

import (
	"os"
	"path/filepath"
	"testing"

	"code-builder/internal/config"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyze_go(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/app\n\ngo 1.22\n")
	s := Analyze(dir, config.BuildStackConfig{})
	if s.Language != "go" || s.BuildToolChain != "go" || s.GoVersion != "1.22" {
		t.Errorf("stack = %+v", s)
	}
}

func TestAnalyze_mavenJava8AndMin(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pom.xml", `<project><properties><java.version>1.8</java.version></properties></project>`)
	s := Analyze(dir, config.BuildStackConfig{})
	if s.Language != "java" || s.BuildTool != "maven" || s.JavaVersion != "8" {
		t.Errorf("stack = %+v", s)
	}

	s = Analyze(dir, config.BuildStackConfig{JavaMinVersion: "17"})
	if s.JavaVersion != "17" {
		t.Errorf("java version = %q, want min 17", s.JavaVersion)
	}
}

func TestAnalyze_mavenSpringJava17(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pom.xml", `<project><properties><maven.compiler.release>17</maven.compiler.release></properties>
		<artifactId>spring-boot-starter-parent</artifactId></project>`)
	s := Analyze(dir, config.BuildStackConfig{})
	if s.JavaVersion != "17" || s.Framework != "spring" {
		t.Errorf("stack = %+v", s)
	}
}

func TestAnalyze_gradle(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "build.gradle", `plugins { id 'org.springframework.boot' version '3.2.0' }
java { sourceCompatibility = JavaVersion.VERSION_11 }`)
	s := Analyze(dir, config.BuildStackConfig{})
	if s.BuildTool != "gradle" || s.JavaVersion != "11" || s.Framework != "spring" {
		t.Errorf("stack = %+v", s)
	}
}

func TestAnalyze_nodeFrameworksAndPackageManager(t *testing.T) {
	tests := []struct {
		name      string
		pkg       string
		files     map[string]string
		wantChain string
		wantFrame string
		wantPM    string
		wantNode  string
	}{
		{
			name:      "next pnpm",
			pkg:       `{"dependencies":{"next":"14"},"volta":{"node":"20.11.0"},"packageManager":"pnpm@8.15.0"}`,
			wantChain: "node",
			wantFrame: "next",
			wantPM:    "pnpm",
			wantNode:  "20",
		},
		{
			name:      "nuxt yarn",
			pkg:       `{"dependencies":{"nuxt":"3"},"engines":{"node":">=18"}}`,
			files:     map[string]string{"yarn.lock": ""},
			wantChain: "node",
			wantFrame: "nuxt",
			wantPM:    "yarn",
			wantNode:  "18",
		},
		{
			name:      "vue pnpm lock",
			pkg:       `{"dependencies":{"vue":"3"}}`,
			files:     map[string]string{"pnpm-lock.yaml": ""},
			wantChain: "vue",
			wantFrame: "vue",
			wantPM:    "pnpm",
			wantNode:  "22",
		},
		{
			name:      "angular npm",
			pkg:       `{"dependencies":{"@angular/core":"17"}}`,
			wantChain: "vue",
			wantFrame: "angular",
			wantPM:    "npm",
			wantNode:  "22",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "package.json", tt.pkg)
			for name := range tt.files {
				write(t, dir, name, "")
			}
			s := Analyze(dir, config.BuildStackConfig{})
			if s.BuildToolChain != tt.wantChain || s.Framework != tt.wantFrame || s.PackageManager != tt.wantPM || s.NodeVersion != tt.wantNode {
				t.Errorf("stack = %+v", s)
			}
		})
	}
}

func TestAnalyze_configOverrides(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pom.xml", `<project><properties><java.version>8</java.version></properties></project>`)
	write(t, dir, "package.json", `{"dependencies":{"vue":"3"}}`)
	s := Analyze(dir, config.BuildStackConfig{
		GoVersion:      "1.24",
		JavaVersion:    "17",
		NodeVersion:    "20",
		PackageManager: "yarn",
	})
	if s.JavaVersion != "17" {
		t.Errorf("java = %q, want configured 17", s.JavaVersion)
	}
}
