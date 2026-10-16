package builder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitlab-builder/internal/config"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func Test_TagsFor(t *testing.T) {
	tests := []struct {
		name      string
		image     string
		branch    string
		sha       string
		tagLatest bool
		want      []string
	}{
		{
			name:   "simple branch short sha padded to 12",
			image:  "local/app",
			branch: "main",
			sha:    "abcdef1234567890",
			want:   []string{"local/app:main-abcdef123456"},
		},
		{
			name:   "slash in branch becomes dash",
			image:  "local/app",
			branch: "release/1.2",
			sha:    "0123456789ab",
			want:   []string{"local/app:release-1.2-0123456789ab"},
		},
		{
			name:   "short sha used whole",
			image:  "app",
			branch: "main",
			sha:    "abc",
			want:   []string{"app:main-abc"},
		},
		{
			name:      "tag latest appended",
			image:     "app",
			branch:    "main",
			sha:       "abcdef123456",
			tagLatest: true,
			want:      []string{"app:main-abcdef123456", "app:latest"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TagsFor(tt.image, tt.branch, tt.sha, tt.tagLatest)
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

func Test_ResolveDockerfile_when_TemplateName_ThenMaterialiseIntoContext(t *testing.T) {
	// Given
	repoDir := t.TempDir()

	// When
	dockerfile, ctxDir, err := ResolveDockerfile(context.Background(), repoDir, config.BuildSpec{Dockerfile: "go.Dockerfile"})

	// Then
	if err != nil {
		t.Fatalf("ResolveDockerfile() error = %v", err)
	}
	if ctxDir != repoDir {
		t.Errorf("context = %q, want %q", ctxDir, repoDir)
	}
	content, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatalf("read generated: %v", err)
	}
	if !strings.Contains(string(content), "FROM") {
		t.Error("generated dockerfile missing FROM")
	}
	if filepath.Base(dockerfile) != generatedDockerfileName {
		t.Errorf("generated name = %q, want %q", filepath.Base(dockerfile), generatedDockerfileName)
	}
}

func Test_ResolveDockerfile_when_RepoOwnDockerfile_thenReturnRepoPath(t *testing.T) {
	// Given
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// When
	dockerfile, _, err := ResolveDockerfile(context.Background(), repoDir, config.BuildSpec{Dockerfile: "Dockerfile"})

	// Then
	if err != nil {
		t.Fatalf("ResolveDockerfile() error = %v", err)
	}
	if dockerfile != filepath.Join(repoDir, "Dockerfile") {
		t.Errorf("dockerfile = %q, want repo Dockerfile", dockerfile)
	}
}

func Test_ResolveDockerfile_when_MissingEverywhere_thenReturnErrorListingTemplates(t *testing.T) {
	// Given
	repoDir := t.TempDir()

	// When
	_, _, err := ResolveDockerfile(context.Background(), repoDir, config.BuildSpec{Dockerfile: "missing.Dockerfile"})

	// Then
	if err == nil {
		t.Fatal("ResolveDockerfile() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "go.Dockerfile") {
		t.Errorf("error = %v, want it to list available templates", err)
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

func Test_dockerBuild_when_Success_ThenPassesExpectedArgsAndStreamsLog(t *testing.T) {
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
		Tags:       []string{"app:main-abc", "app:latest"},
		BuildArgs:  map[string]string{"B": "2", "A": "1"},
		NoCache:    true,
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
	for _, want := range []string{"build", "-f /src/Dockerfile", "--no-cache", "--build-arg A=1", "--build-arg B=2", "-t app:main-abc", "-t app:latest", "/src"} {
		if !strings.Contains(build, want) {
			t.Errorf("build args %q missing %q", build, want)
		}
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
