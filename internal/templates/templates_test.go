package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  Name
		ok    bool
	}{
		{name: "go module", files: map[string]string{"go.mod": "module x"}, want: NameGo, ok: true},
		{name: "maven", files: map[string]string{"pom.xml": "<project/>"}, want: NameJava, ok: true},
		{name: "gradle groovy", files: map[string]string{"build.gradle": ""}, want: NameGradle, ok: true},
		{name: "gradle kotlin", files: map[string]string{"build.gradle.kts": ""}, want: NameGradle, ok: true},
		{name: "node", files: map[string]string{"package.json": `{"dependencies":{"express":"^4"}}`}, want: NameNode, ok: true},
		{name: "vue dependency", files: map[string]string{"package.json": `{"dependencies":{"vue":"^3"}}`}, want: NameVue, ok: true},
		{name: "vue scoped dependency", files: map[string]string{"package.json": `{"devDependencies":{"@vue/cli":"^5"}}`}, want: NameVue, ok: true},
		{name: "vue prefixed dependency", files: map[string]string{"package.json": `{"dependencies":{"vue-router":"^4"}}`}, want: NameVue, ok: true},
		{name: "nuxt uses node runtime", files: map[string]string{"package.json": `{"dependencies":{"nuxt":"^3"}}`}, want: NameNode, ok: true},
		{name: "go wins over node", files: map[string]string{"go.mod": "module x", "package.json": `{"dependencies":{"vue":"^3"}}`}, want: NameGo, ok: true},
		{name: "unparseable package.json falls back to node", files: map[string]string{"package.json": "not json"}, want: NameNode, ok: true},
		{name: "rust", files: map[string]string{"Cargo.toml": "[package]\nname = \"x\""}, want: NameRust, ok: true},
		{name: "react", files: map[string]string{"package.json": `{"dependencies":{"react":"^18"}}`}, want: NameVue, ok: true},
		{name: "vite", files: map[string]string{"package.json": `{"devDependencies":{"vite":"^5"}}`}, want: NameVue, ok: true},
		{name: "go wins over rust", files: map[string]string{"go.mod": "module x", "Cargo.toml": "[package]"}, want: NameGo, ok: true},
		{name: "no marker", files: map[string]string{"README.md": "x"}, ok: false},
		{name: "empty dir", files: map[string]string{}, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeFile(t, dir, name, content)
			}

			got, ok := Detect(dir)

			if ok != tt.ok {
				t.Fatalf("Detect() ok = %v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				if len(got.Content) != 0 {
					t.Errorf("Detect() content = %q, want empty on no match", got.Content)
				}
				return
			}
			if got.Name != tt.want {
				t.Fatalf("Detect() name = %q, want %q", got.Name, tt.want)
			}
			if len(got.Content) == 0 {
				t.Fatal("Detect() returned empty template content")
			}
			if !strings.Contains(string(got.Content), "FROM ") {
				t.Errorf("template %s has no FROM instruction:\n%s", got.Name, got.Content)
			}
		})
	}
}

func TestDetect_does_not_treat_directory_marker_as_file(t *testing.T) {
	// 准备：名为 go.mod 的目录不应被视为标记文件。
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "go.mod"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 执行
	_, ok := Detect(dir)

	// 断言
	if ok {
		t.Fatal("Detect() ok = true for a go.mod directory, want false")
	}
}

func TestAllTemplates_are_embedded_and_multi_stage(t *testing.T) {
	for _, name := range []Name{NameGo, NameNode, NameVue, NameJava, NameGradle, NameRust} {
		content, err := files.ReadFile(string(name) + ".Dockerfile")
		if err != nil {
			t.Fatalf("template %s not embedded: %v", name, err)
		}
		if !strings.Contains(string(content), "FROM ") {
			t.Errorf("template %s has no FROM instruction", name)
		}
		if strings.Count(string(content), "FROM ") < 2 {
			t.Errorf("template %s is not multi-stage", name)
		}
		if strings.Contains(string(content), "FROM scratch") {
			t.Errorf("template %s uses scratch; runtime must keep a shell", name)
		}
	}
}

func TestEnsureDockerignore_when_Missing_thenCreatesButKeepsBuildDir(t *testing.T) {
	dir := t.TempDir()
	created, err := EnsureDockerignore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("EnsureDockerignore() created = false, want true")
	}
	data, err := os.ReadFile(filepath.Join(dir, ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), ".code-builder") {
		t.Errorf("default .dockerignore must not exclude .code-builder:\n%s", data)
	}
	if DockerignoreExcludesBuildDir(dir) {
		t.Error("DockerignoreExcludesBuildDir() = true for generated file")
	}
}

func TestDockerignoreExcludesBuildDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".dockerignore"), []byte(".git\n.code-builder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !DockerignoreExcludesBuildDir(dir) {
		t.Error("DockerignoreExcludesBuildDir() = false, want true")
	}
}

func TestScript_mapping(t *testing.T) {
	tests := []struct {
		name     Name
		wantOK   bool
		wantIncl string
	}{
		{name: NameGo, wantOK: true, wantIncl: "go build"},
		{name: NameJava, wantOK: true, wantIncl: "clean package"},
		{name: NameNode, wantOK: true, wantIncl: "npm run build"},
		{name: NameVue, wantOK: true, wantIncl: "npm run build"},
		{name: NameGradle, wantOK: true, wantIncl: "bootJar"},
		{name: NameRust, wantOK: true, wantIncl: "cargo build --release"},
	}
	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			content, ok := Script(tt.name)

			if ok != tt.wantOK {
				t.Fatalf("Script(%q) ok = %v, want %v", tt.name, ok, tt.wantOK)
			}
			if !tt.wantOK {
				if content != nil {
					t.Errorf("Script(%q) content = %q, want nil", tt.name, content)
				}
				return
			}
			if !strings.Contains(string(content), tt.wantIncl) {
				t.Errorf("Script(%q) missing %q", tt.name, tt.wantIncl)
			}
		})
	}
}

// TestScripts_regressions 锁定上游脚本嵌入时修复的两个缺陷：
// build-go.sh 必须在两个分支中都设置 CGO_ENABLED，且
// build-maven.sh 不得把一批 jar 通配符复制到单个文件上。
func TestScripts_regressions(t *testing.T) {
	t.Run("build-go sets CGO_ENABLED", func(t *testing.T) {
		content, _ := Script(NameGo)
		if !strings.Contains(string(content), `CGO_ENABLED="${CGO_ENABLED}"`) || !strings.Contains(string(content), "go build") {
			t.Errorf("build-go.sh must honour its cgo argument:\n%s", content)
		}
	})
	t.Run("build-maven avoids multi-jar copy", func(t *testing.T) {
		content, _ := Script(NameJava)
		if strings.Contains(string(content), "cp -f target/*.jar app.jar") {
			t.Errorf("build-maven.sh still copies every jar onto app.jar:\n%s", content)
		}
		if !strings.Contains(string(content), "*-plain.jar") {
			t.Errorf("build-maven.sh must prefer the main jar:\n%s", content)
		}
	})
	t.Run("build-gradle selects a single jar", func(t *testing.T) {
		content, _ := Script(NameGradle)
		if strings.Contains(string(content), "cp -f build/libs/*.jar app.jar") {
			t.Errorf("build-gradle.sh copies every jar onto app.jar:\n%s", content)
		}
		if !strings.Contains(string(content), "*-plain.jar") {
			t.Errorf("build-gradle.sh must prefer the main jar:\n%s", content)
		}
	})
}

func TestTemplates_that_call_scripts_reference_scriptpath(t *testing.T) {
	for _, name := range []Name{NameGo, NameNode, NameVue, NameJava, NameGradle, NameRust} {
		content, err := files.ReadFile(string(name) + ".Dockerfile")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), ScriptPath) {
			t.Errorf("template %s does not invoke %s:\n%s", name, ScriptPath, content)
		}
	}
}
