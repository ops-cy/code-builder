package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func Test_Load_when_ValidYAMLWithEnvExpansion_thenReturnExpandedConfig(t *testing.T) {
	// Given
	t.Setenv("TEST_WEBHOOK_SECRET", "s3cret")
	path := writeConfig(t, `
server:
  listen: ":9090"
gitlab:
  webhook_secret: "${TEST_WEBHOOK_SECRET}"
repositories:
  - name: app
    url: "ssh://git@example.com/g/app.git"
    branches: ["main"]
    build:
      image: "local/app"
`)

	// When
	cfg, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GitLab.WebhookSecret != "s3cret" {
		t.Errorf("WebhookSecret = %q, want s3cret", cfg.GitLab.WebhookSecret)
	}
	if cfg.Server.Listen != ":9090" {
		t.Errorf("Listen = %q, want :9090", cfg.Server.Listen)
	}
	if cfg.Server.Concurrency != 2 || cfg.Server.QueueSize != 100 {
		t.Errorf("defaults not applied: %+v", cfg.Server)
	}
	if cfg.Server.Workspace != "/app/workspace" || cfg.Server.LogsDir != "/app/logs" {
		t.Errorf("workspace defaults not applied: %+v", cfg.Server)
	}
	if cfg.Repositories[0].Build.Context != "." || cfg.Repositories[0].Build.Dockerfile != "Dockerfile" {
		t.Errorf("build defaults not applied: %+v", cfg.Repositories[0].Build)
	}
}

func Test_Load_when_CommentMentionsPlaceholder_ThenNotExpanded(t *testing.T) {
	// Given: the example config documents ${VAR} syntax inside a comment line.
	t.Setenv("TEST_WEBHOOK_SECRET", "from-env")
	path := writeConfig(t, `
# ${VAR} is documented here and must not be expanded.
gitlab:
  webhook_secret: "${TEST_WEBHOOK_SECRET}"
repositories:
  - name: app
    url: "ssh://git@example.com/g/app.git"
    branches: ["main"]
    build:
      image: "local/app"
`)

	// When
	cfg, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GitLab.WebhookSecret != "from-env" {
		t.Errorf("WebhookSecret = %q, want from-env", cfg.GitLab.WebhookSecret)
	}
}

func Test_Load_when_UnsetEnvVar_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
gitlab:
  webhook_secret: "${DEFINITELY_NOT_SET_VAR_XYZ}"
repositories:
  - name: app
    url: "ssh://git@example.com/g/app.git"
    branches: ["main"]
    build:
      image: "local/app"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want unset env var error")
	}
}

func Test_Load_when_MissingSecret_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
gitlab:
  webhook_secret: ""
repositories:
  - name: app
    url: "ssh://git@example.com/g/app.git"
    branches: ["main"]
    build:
      image: "local/app"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want secret required error")
	}
}

func Test_Load_when_DuplicateRepoName_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
gitlab:
  webhook_secret: "x"
repositories:
  - name: app
    url: "ssh://git@example.com/g/app.git"
    branches: ["main"]
    build:
      image: "local/app"
  - name: app
    url: "ssh://git@example.com/g/app2.git"
    branches: ["main"]
    build:
      image: "local/app2"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want duplicate name error")
	}
}

func Test_Load_when_InvalidBranchRegex_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
gitlab:
  webhook_secret: "x"
repositories:
  - name: app
    url: "ssh://git@example.com/g/app.git"
    branches: ["main"]
    branch_regex: "(["
    build:
      image: "local/app"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want invalid regex error")
	}
}

func Test_Load_when_NoRepositories_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
gitlab:
  webhook_secret: "x"
repositories: []
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want repositories required error")
	}
}
