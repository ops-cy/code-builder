package builder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gitlab-builder/internal/config"
	"gitlab-builder/internal/templates"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const sha40 = "a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4"

func Test_imageRefs_rejects_invalid_tag(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		wantErr bool
	}{
		{name: "empty tag", tag: "", wantErr: true},
		{name: "leading dot", tag: ".beta", wantErr: true},
		{name: "leading dash", tag: "-beta", wantErr: true},
		{name: "slash in tag", tag: "v1/beta", wantErr: true},
		{name: "space in tag", tag: "v1 beta", wantErr: true},
		{name: "latest ok", tag: "latest", wantErr: false},
		{name: "sha8 ok", tag: "a1b2c3d4", wantErr: false},
		{name: "semver ok", tag: "v1.2.3", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := imageRefs("app", tt.tag, config.RegistryConfig{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("imageRefs(%q) err = %v, wantErr = %v", tt.tag, err, tt.wantErr)
			}
		})
	}
}

func Test_ResolveDockerfile_when_RepoOwnDockerfile_thenReturnRepoPath(t *testing.T) {
	// 准备：仓库自带 Dockerfile —— 它必须优先于任何模板。
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 执行
	dockerfile, source, err := resolveDockerfile(repoDir)

	// 断言
	if err != nil {
		t.Fatalf("resolveDockerfile() error = %v", err)
	}
	if dockerfile != filepath.Join(repoDir, "Dockerfile") {
		t.Errorf("dockerfile = %q, want repo Dockerfile", dockerfile)
	}
	if source != "repository" {
		t.Errorf("source = %q, want %q", source, "repository")
	}
	content, readErr := os.ReadFile(dockerfile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "FROM scratch\n" {
		t.Errorf("repo Dockerfile was overwritten: %q", content)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, filepath.FromSlash(templates.ScriptPath))); statErr == nil {
		t.Errorf("resolveDockerfile() wrote %s for a repo-owned Dockerfile, want none", templates.ScriptPath)
	}
}

func Test_ResolveDockerfile_when_NoRepoDockerfile_thenUseBuiltinTemplate(t *testing.T) {
	tests := []struct {
		name           string
		markerFile     string
		markerBody     string
		wantSource     string
		wantInFile     string
		wantScript     bool
		wantScriptIncl string
	}{
		{name: "go module", markerFile: "go.mod", markerBody: "module x\n", wantSource: "template:go", wantInFile: "FROM golang:", wantScript: true, wantScriptIncl: "go build"},
		{name: "maven", markerFile: "pom.xml", markerBody: "<project/>\n", wantSource: "template:java", wantInFile: "maven:3.9-eclipse-temurin-21", wantScript: true, wantScriptIncl: "mvn clean package"},
		{name: "gradle", markerFile: "build.gradle", markerBody: "", wantSource: "template:gradle", wantInFile: "gradle:8-jdk21", wantScript: false},
		{name: "node", markerFile: "package.json", markerBody: `{"dependencies":{"express":"^4"}}`, wantSource: "template:node", wantInFile: "node:22-alpine", wantScript: true, wantScriptIncl: "npm run build"},
		{name: "vue", markerFile: "package.json", markerBody: `{"dependencies":{"vue":"^3"}}`, wantSource: "template:vue", wantInFile: "nginx:", wantScript: true, wantScriptIncl: "npm run build"},
		{name: "python", markerFile: "requirements.txt", markerBody: "flask\n", wantSource: "template:python", wantInFile: "python:3.12-slim", wantScript: true, wantScriptIncl: "pip install -r requirements.txt"},
		{name: "php", markerFile: "composer.json", markerBody: "{}\n", wantSource: "template:php", wantInFile: "php:8.2-cli", wantScript: true, wantScriptIncl: "composer install"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 准备：一个只有语言标记文件、没有 Dockerfile 的仓库。
			repoDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(repoDir, tt.markerFile), []byte(tt.markerBody), 0o644); err != nil {
				t.Fatal(err)
			}

			// 执行
			dockerfile, source, err := resolveDockerfile(repoDir)

			// 断言
			if err != nil {
				t.Fatalf("resolveDockerfile() error = %v", err)
			}
			if dockerfile != filepath.Join(repoDir, "Dockerfile") {
				t.Errorf("dockerfile = %q, want materialised %s/Dockerfile", dockerfile, repoDir)
			}
			if source != tt.wantSource {
				t.Errorf("source = %q, want %q", source, tt.wantSource)
			}
			content, readErr := os.ReadFile(dockerfile)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(content), tt.wantInFile) {
				t.Errorf("template %s missing %q:\n%s", source, tt.wantInFile, content)
			}

			scriptPath := filepath.Join(repoDir, filepath.FromSlash(templates.ScriptPath))
			script, scriptErr := os.ReadFile(scriptPath)
			if tt.wantScript {
				if scriptErr != nil {
					t.Fatalf("resolveDockerfile() did not write %s: %v", templates.ScriptPath, scriptErr)
				}
				if !strings.Contains(string(script), tt.wantScriptIncl) {
					t.Errorf("build script for %s missing %q:\n%s", source, tt.wantScriptIncl, script)
				}
			} else if scriptErr == nil {
				t.Errorf("resolveDockerfile() wrote %s for %s, want none", templates.ScriptPath, source)
			}
		})
	}
}

func Test_ResolveDockerfile_when_NoDockerfileAndNoTemplate_thenReturnClearError(t *testing.T) {
	// 准备：既没有 Dockerfile，也没有可识别的语言标记。
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 执行
	_, _, err := resolveDockerfile(repoDir)

	// 断言
	if err == nil {
		t.Fatal("resolveDockerfile() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "no Dockerfile at repository root and no built-in template matched") {
		t.Errorf("error = %v, want explicit missing-recipe message", err)
	}
}

// stubExec 在测试期间替换 execCommand。每次调用都会被记录；
// handler 负责构造 argv 并控制成功/失败。
func stubExec(t *testing.T, handler func(ctx context.Context, name string, args ...string) *exec.Cmd) *[][]string {
	t.Helper()
	var calls [][]string
	orig := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{name}, args...))
		return handler(ctx, name, args...)
	}
	t.Cleanup(func() { execCommand = orig })
	return &calls
}

func Test_dockerBuild_when_Success_ThenPassesExpectedArgsAndStreamsCommand(t *testing.T) {
	// 准备
	logFile := filepath.Join(t.TempDir(), "build.log")
	logw, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	defer logw.Close()

	calls := stubExec(t, func(_ context.Context, _ string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "version" {
			return exec.Command("go", "version") // 任意可成功执行的命令
		}
		return exec.Command("go", "version")
	})

	// 执行
	err = dockerBuild(context.Background(), dockerBuildRequest{
		ContextDir: "/src",
		Dockerfile: "/src/Dockerfile",
		Tags:       []string{"app:a1b2c3d4"},
		Log:        logw,
	})

	// 断言
	if err != nil {
		t.Fatalf("dockerBuild() error = %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("calls = %v, want version probe + build", *calls)
	}
	build := strings.Join((*calls)[1], " ")
	for _, want := range []string{"build", "-f /src/Dockerfile", "-t app:a1b2c3d4", "/src"} {
		if !strings.Contains(build, want) {
			t.Errorf("build args %q missing %q", build, want)
		}
	}
	content, readErr := os.ReadFile(logFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(content), "docker command: docker build") {
		t.Errorf("log = %s, want the exact docker command line", content)
	}
}

func Test_dockerBuild_when_DaemonUnreachable_thenReturnClearError(t *testing.T) {
	// 准备
	stubExec(t, func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		// `go run` 一个不存在的文件会快速以非零码退出。
		return exec.CommandContext(ctx, "go", "run", "definitely-not-a-file.go")
	})

	// 执行
	err := dockerBuild(context.Background(), dockerBuildRequest{
		ContextDir: ".",
		Dockerfile: "Dockerfile",
		Log:        os.Stderr,
	})

	// 断言
	if err == nil {
		t.Fatal("dockerBuild() error = nil, want daemon unreachable error")
	}
	if !strings.Contains(err.Error(), "docker daemon not reachable") {
		t.Errorf("error = %v, want docker daemon not reachable", err)
	}
}

// Test_imageRefs 锁定 tag/推送 的组合规则：始终生成本地 tag，仅当设置了 registry URL
// 时才生成远程 ref，仅当配置了凭据时才登录。
func Test_imageRefs(t *testing.T) {
	tests := []struct {
		name      string
		image     string
		tag       string
		reg       config.RegistryConfig
		wantTags  []string
		wantPush  []string
		wantLogin bool
		wantErr   bool
	}{
		{
			name:     "registry disabled yields only the local tag",
			image:    "app",
			tag:      "latest",
			reg:      config.RegistryConfig{},
			wantTags: []string{"app:latest"},
		},
		{
			name:     "sha8 tag still works",
			image:    "app",
			tag:      "a1b2c3d4",
			reg:      config.RegistryConfig{},
			wantTags: []string{"app:a1b2c3d4"},
		},
		{
			name:     "url only adds a remote ref without login",
			image:    "app",
			tag:      "latest",
			reg:      config.RegistryConfig{URL: "registry.example.com"},
			wantTags: []string{"app:latest", "registry.example.com/app:latest"},
			wantPush: []string{"registry.example.com/app:latest"},
		},
		{
			name:     "project and tolerated slashes become a path segment",
			image:    "app",
			tag:      "latest",
			reg:      config.RegistryConfig{URL: "registry.example.com/", Project: "/tenants/"},
			wantTags: []string{"app:latest", "registry.example.com/tenants/app:latest"},
			wantPush: []string{"registry.example.com/tenants/app:latest"},
		},
		{
			name:      "credentials add a login",
			image:     "app",
			tag:       "latest",
			reg:       config.RegistryConfig{URL: "registry.example.com", Username: "u", Password: "p"},
			wantTags:  []string{"app:latest", "registry.example.com/app:latest"},
			wantPush:  []string{"registry.example.com/app:latest"},
			wantLogin: true,
		},
		{
			name:    "empty tag rejected",
			image:   "app",
			tag:     "",
			reg:     config.RegistryConfig{URL: "registry.example.com"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags, push, login, err := imageRefs(tt.image, tt.tag, tt.reg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("imageRefs() err = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("imageRefs() error = %v", err)
			}
			if !slices.Equal(tags, tt.wantTags) {
				t.Errorf("tags = %v, want %v", tags, tt.wantTags)
			}
			if !slices.Equal(push, tt.wantPush) {
				t.Errorf("pushRefs = %v, want %v", push, tt.wantPush)
			}
			if (login != nil) != tt.wantLogin {
				t.Errorf("login = %v, want present=%v", login, tt.wantLogin)
			}
		})
	}
}

func Test_dockerBuild_when_PushRefs_thenBuildThenPushInOrder(t *testing.T) {
	// 准备
	logFile := filepath.Join(t.TempDir(), "build.log")
	logw, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	defer logw.Close()

	calls := stubExec(t, func(_ context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.Command("go", "version")
	})
	remote := "registry.example.com/tenants/app:a1b2c3d4"

	// 执行
	err = dockerBuild(context.Background(), dockerBuildRequest{
		ContextDir: "/src",
		Dockerfile: "/src/Dockerfile",
		Tags:       []string{"app:a1b2c3d4", remote},
		PushRefs:   []string{remote},
		Log:        logw,
	})

	// 断言
	if err != nil {
		t.Fatalf("dockerBuild() error = %v", err)
	}
	if len(*calls) != 3 {
		t.Fatalf("calls = %v, want version + build + push", *calls)
	}
	if got := (*calls)[1]; got[0] != "docker" || got[1] != "build" {
		t.Errorf("call[1] = %v, want docker build", got)
	}
	if got := (*calls)[2]; got[0] != "docker" || got[1] != "push" || got[2] != remote {
		t.Errorf("call[2] = %v, want docker push %s", got, remote)
	}
}

func Test_dockerBuild_when_Login_thenLoginBeforeBuildAndPasswordNeverLogged(t *testing.T) {
	// 准备
	logFile := filepath.Join(t.TempDir(), "build.log")
	logw, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	defer logw.Close()

	calls := stubExec(t, func(_ context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.Command("go", "version")
	})
	const secret = "s3cr3t-token"

	// 执行
	err = dockerBuild(context.Background(), dockerBuildRequest{
		ContextDir: "/src",
		Dockerfile: "/src/Dockerfile",
		Tags:       []string{"app:a1b2c3d4"},
		Login:      &dockerLogin{Registry: "registry.example.com", Username: "ci", Password: secret},
		Log:        logw,
	})

	// 断言
	if err != nil {
		t.Fatalf("dockerBuild() error = %v", err)
	}
	if len(*calls) != 3 {
		t.Fatalf("calls = %v, want version + login + build", *calls)
	}
	if got := (*calls)[1]; got[0] != "docker" || got[1] != "login" {
		t.Errorf("call[1] = %v, want docker login before build", got)
	}
	content, readErr := os.ReadFile(logFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(content), secret) {
		t.Errorf("build log leaked the registry password:\n%s", content)
	}
}

func Test_WorkspaceDir_when_DifferentReposSameName_ThenIsolated(t *testing.T) {
	// 准备
	ws := t.TempDir()
	a := config.Repository{Key: "example.com/g1/app", Name: "app"}
	b := config.Repository{Key: "example.com/g2/app", Name: "app"}

	// 执行
	da, err := workspaceDir(ws, a, sha40)
	if err != nil {
		t.Fatal(err)
	}
	db, err := workspaceDir(ws, b, sha40)
	if err != nil {
		t.Fatal(err)
	}

	// 断言
	if da == db {
		t.Fatalf("dirs = %q for both repos, want per-repository isolation", da)
	}
	for _, d := range []string{da, db} {
		if !strings.HasPrefix(d, ws) {
			t.Errorf("dir %q not inside workspace %q", d, ws)
		}
	}
}
