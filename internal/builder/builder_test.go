package builder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

func Test_TagsFor(t *testing.T) {
	tests := []struct {
		name    string
		image   string
		sha     string
		want    []string
		wantErr bool
	}{
		{
			name:  "tag is first 8 chars of the full commit sha",
			image: "backend-api",
			sha:   sha40,
			want:  []string{"backend-api:a1b2c3d4"},
		},
		{
			name:  "image with path separator keeps its prefix",
			image: "local/app",
			sha:   "0123456789abcdef0123456789abcdef01234567",
			want:  []string{"local/app:01234567"},
		},
		{
			name:    "sha shorter than tag length rejected",
			image:   "app",
			sha:     "abc",
			wantErr: true,
		},
		{
			name:    "non-hex sha rejected",
			image:   "app",
			sha:     "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TagsFor(tt.image, tt.sha)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("TagsFor() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("TagsFor() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("TagsFor() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("TagsFor()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func Test_ResolveDockerfile_when_RepoOwnDockerfile_thenReturnRepoPath(t *testing.T) {
	// Given: repo ships a Dockerfile — it must win over any template.
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// When
	dockerfile, source, err := resolveDockerfile(repoDir)

	// Then
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: a repo with only a language marker and no Dockerfile.
			repoDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(repoDir, tt.markerFile), []byte(tt.markerBody), 0o644); err != nil {
				t.Fatal(err)
			}

			// When
			dockerfile, source, err := resolveDockerfile(repoDir)

			// Then
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
	// Given: neither a Dockerfile nor a recognised language marker.
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// When
	_, _, err := resolveDockerfile(repoDir)

	// Then
	if err == nil {
		t.Fatal("resolveDockerfile() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "no Dockerfile at repository root and no built-in template matched") {
		t.Errorf("error = %v, want explicit missing-recipe message", err)
	}
}

// stubExec replaces execCommand for the duration of the test. Each call is
// recorded; the handler builds the argv and controls success/failure.
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
	// Given
	logFile := filepath.Join(t.TempDir(), "build.log")
	logw, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	defer logw.Close()

	calls := stubExec(t, func(_ context.Context, _ string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "version" {
			return exec.Command("go", "version") // any successful command
		}
		return exec.Command("go", "version")
	})

	// When
	err = dockerBuild(context.Background(), dockerBuildRequest{
		ContextDir: "/src",
		Dockerfile: "/src/Dockerfile",
		Tags:       []string{"app:a1b2c3d4"},
		Log:        logw,
	})

	// Then
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
	// Given
	stubExec(t, func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		// `go run` with a nonexistent file exits non-zero quickly.
		return exec.CommandContext(ctx, "go", "run", "definitely-not-a-file.go")
	})

	// When
	err := dockerBuild(context.Background(), dockerBuildRequest{
		ContextDir: ".",
		Dockerfile: "Dockerfile",
		Log:        os.Stderr,
	})

	// Then
	if err == nil {
		t.Fatal("dockerBuild() error = nil, want daemon unreachable error")
	}
	if !strings.Contains(err.Error(), "docker daemon not reachable") {
		t.Errorf("error = %v, want docker daemon not reachable", err)
	}
}

func Test_WorkspaceDir_when_DifferentReposSameName_ThenIsolated(t *testing.T) {
	// Given
	ws := t.TempDir()
	a := config.Repository{Key: "example.com/g1/app", Name: "app"}
	b := config.Repository{Key: "example.com/g2/app", Name: "app"}

	// When
	da, err := workspaceDir(ws, a, sha40)
	if err != nil {
		t.Fatal(err)
	}
	db, err := workspaceDir(ws, b, sha40)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if da == db {
		t.Fatalf("dirs = %q for both repos, want per-repository isolation", da)
	}
	for _, d := range []string{da, db} {
		if !strings.HasPrefix(d, ws) {
			t.Errorf("dir %q not inside workspace %q", d, ws)
		}
	}
}
