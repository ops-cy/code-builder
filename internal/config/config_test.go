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
  host: "example.com"
  webhook_secret: "${TEST_WEBHOOK_SECRET}"
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
	if cfg.GitLab.Host != "example.com" {
		t.Errorf("Host = %q, want example.com", cfg.GitLab.Host)
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
	if cfg.Build.Context != "." || cfg.Build.Dockerfile != "auto" {
		t.Errorf("build defaults not applied: %+v", cfg.Build)
	}
}

func Test_Load_when_CommentMentionsPlaceholder_ThenNotExpanded(t *testing.T) {
	// Given: the example config documents ${VAR} syntax inside a comment line.
	t.Setenv("TEST_WEBHOOK_SECRET", "from-env")
	path := writeConfig(t, `
# ${VAR} is documented here and must not be expanded.
gitlab:
  host: "example.com"
  webhook_secret: "${TEST_WEBHOOK_SECRET}"
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
  host: "example.com"
  webhook_secret: "${DEFINITELY_NOT_SET_VAR_XYZ}"
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
  host: "example.com"
  webhook_secret: ""
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want secret required error")
	}
}

func Test_Load_when_InvalidBranchRegex_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
gitlab:
  host: "example.com"
  webhook_secret: "x"
build:
  branch_regex: "(["
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want invalid regex error")
	}
}

func Test_Load_when_MissingHost_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
gitlab:
  webhook_secret: "x"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want gitlab.host required error")
	}
}
