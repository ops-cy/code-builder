package deploy

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitlab-builder/internal/config"
)

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

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

func okCmd(_ context.Context, _ string, _ ...string) *exec.Cmd {
	return exec.Command("go", "version")
}

func Test_resolve_defaults_project_and_finds_compose(t *testing.T) {
	// Given
	root := t.TempDir()
	dir := filepath.Join(root, "atlas-market")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "docker-compose.yaml"), "services: {}\n")

	// When
	got, err := resolve(Request{
		Service: "atlas-market",
		Config:  config.DeployConfig{ServicesDir: root, ComposeFile: "docker-compose.yaml"},
	})

	// Then
	if err != nil {
		t.Fatalf("resolve() error = %v", err)
	}
	if got.dir != dir {
		t.Errorf("dir = %q, want %q", got.dir, dir)
	}
	if got.service != "atlas-market" {
		t.Errorf("service = %q, want atlas-market", got.service)
	}
	if got.project != "atlas-market" {
		t.Errorf("project = %q, want atlas-market (default = dir name)", got.project)
	}
	if got.composePath != filepath.Join(dir, "docker-compose.yaml") {
		t.Errorf("composePath = %q, want %s", got.composePath, filepath.Join(dir, "docker-compose.yaml"))
	}
}

func Test_resolve_autodetects_standard_compose_names(t *testing.T) {
	// Given: only compose.yaml exists though docker-compose.yaml was configured.
	root := t.TempDir()
	dir := filepath.Join(root, "svc")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "compose.yaml"), "services: {}\n")

	// When
	got, err := resolve(Request{
		Service: "svc",
		Config:  config.DeployConfig{ServicesDir: root, ComposeFile: "docker-compose.yaml"},
	})

	// Then
	if err != nil {
		t.Fatalf("resolve() error = %v", err)
	}
	if filepath.Base(got.composePath) != "compose.yaml" {
		t.Errorf("composePath = %q, want compose.yaml fallback", got.composePath)
	}
}

func Test_resolve_ignores_backup_compose_files(t *testing.T) {
	// Given: only a backup compose file is present.
	root := t.TempDir()
	dir := filepath.Join(root, "ex-backend")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "ow-bak-docker-compose.yaml"), "services: {}\n")

	// When
	_, err := resolve(Request{
		Service: "ex-backend",
		Config:  config.DeployConfig{ServicesDir: root, ComposeFile: "docker-compose.yaml"},
	})

	// Then
	if err == nil {
		t.Fatal("resolve() error = nil, want missing compose error (backup must be ignored)")
	}
	if !strings.Contains(err.Error(), "no compose file") {
		t.Errorf("error = %v, want no compose file", err)
	}
}

func Test_resolve_rejects_dir_escaping_services_dir(t *testing.T) {
	// Given: an override pointing outside the services root.
	root := t.TempDir()

	// When
	_, err := resolve(Request{
		Service: "evil",
		Config: config.DeployConfig{
			ServicesDir: root,
			ComposeFile: "docker-compose.yaml",
			Services:    map[string]config.DeployService{"evil": {Dir: "../evil"}},
		},
	})

	// Then
	if err == nil {
		t.Fatal("resolve() error = nil, want escape error")
	}
	if !strings.Contains(err.Error(), "escapes services_dir") {
		t.Errorf("error = %v, want escapes services_dir", err)
	}
}

func Test_resolve_service_override(t *testing.T) {
	// Given
	root := t.TempDir()
	dir := filepath.Join(root, "backend")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "docker-compose.yaml"), "services: {}\n")

	// When
	got, err := resolve(Request{
		Service: "ex-backend",
		Config: config.DeployConfig{
			ServicesDir: root,
			ComposeFile: "docker-compose.yaml",
			Services: map[string]config.DeployService{
				"ex-backend": {Dir: "backend", Service: "api", ProjectName: "prod"},
			},
		},
	})

	// Then
	if err != nil {
		t.Fatalf("resolve() error = %v", err)
	}
	if got.dir != dir || got.service != "api" || got.project != "prod" {
		t.Errorf("got = %+v, want dir=%s service=api project=prod", got, dir)
	}
}

func Test_Up_when_Resolved_thenComposeUpArgs(t *testing.T) {
	// Given
	root := t.TempDir()
	dir := filepath.Join(root, "atlas-market")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "docker-compose.yaml"), "services: {}\n")
	calls := stubExec(t, okCmd)

	// When
	var logBuf bytes.Buffer
	err := Up(context.Background(), Request{
		Service: "atlas-market",
		Config:  config.DeployConfig{ServicesDir: root, ComposeFile: "docker-compose.yaml"},
		Log:     &logBuf,
	})

	// Then
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %v, want a single docker compose up", *calls)
	}
	got := strings.Join((*calls)[0], " ")
	for _, want := range []string{
		"compose",
		"-f " + filepath.Join(dir, "docker-compose.yaml"),
		"-p atlas-market",
		"up -d",
		"atlas-market",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "--wait") {
		t.Errorf("args %q unexpectedly contain --wait", got)
	}
}

func Test_Up_when_WaitEnabled_thenAddsWaitFlags(t *testing.T) {
	// Given
	root := t.TempDir()
	dir := filepath.Join(root, "svc")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "docker-compose.yaml"), "services: {}\n")
	calls := stubExec(t, okCmd)

	// When
	err := Up(context.Background(), Request{
		Service: "svc",
		Config: config.DeployConfig{
			ServicesDir: root,
			ComposeFile: "docker-compose.yaml",
			Wait:        true,
			WaitTimeout: "30s",
		},
		Log: &bytes.Buffer{},
	})

	// Then
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	got := strings.Join((*calls)[0], " ")
	for _, want := range []string{"--wait", "--wait-timeout 30s"} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
}

func Test_Up_when_ComposeFails_thenReturnClearError(t *testing.T) {
	// Given
	root := t.TempDir()
	dir := filepath.Join(root, "svc")
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "docker-compose.yaml"), "services: {}\n")
	stubExec(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "go", "run", "definitely-not-a-file.go")
	})

	// When
	err := Up(context.Background(), Request{
		Service: "svc",
		Config:  config.DeployConfig{ServicesDir: root, ComposeFile: "docker-compose.yaml"},
		Log:     &bytes.Buffer{},
	})

	// Then
	if err == nil {
		t.Fatal("Up() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "docker compose up -d failed") {
		t.Errorf("error = %v, want docker compose up -d failed", err)
	}
}
