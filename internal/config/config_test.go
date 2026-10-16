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
  queue_size: 5
code:
  webhook_secret: "${TEST_WEBHOOK_SECRET}"
  domain: "code.gitlab.com"
  branches: ["main"]
`)

	// When
	cfg, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Code.WebhookSecret != "s3cret" {
		t.Errorf("WebhookSecret = %q, want s3cret", cfg.Code.WebhookSecret)
	}
	if cfg.Code.Domain != "code.gitlab.com" {
		t.Errorf("Domain = %q, want code.gitlab.com", cfg.Code.Domain)
	}
	if len(cfg.Code.Branches) != 1 || cfg.Code.Branches[0] != "main" {
		t.Errorf("Branches = %v, want [main]", cfg.Code.Branches)
	}
	if cfg.Server.Listen != ":9090" {
		t.Errorf("Listen = %q, want :9090", cfg.Server.Listen)
	}
	if cfg.Server.QueueSize != 5 {
		t.Errorf("QueueSize = %d, want 5", cfg.Server.QueueSize)
	}
	if cfg.Server.Concurrency != 2 {
		t.Errorf("Concurrency = %d, want default 2", cfg.Server.Concurrency)
	}
	if cfg.Server.Workspace != "/app/workspace" || cfg.Server.LogsDir != "/app/logs" {
		t.Errorf("workspace defaults not applied: %+v", cfg.Server)
	}
}

func Test_Load_when_OmittedOptionalFields_thenReturnDocumentedDefaults(t *testing.T) {
	// Given: branches omitted → default whitelist; queue_size omitted → 10.
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
`)

	// When
	cfg, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.QueueSize != 10 {
		t.Errorf("QueueSize = %d, want default 10", cfg.Server.QueueSize)
	}
	if len(cfg.Code.Branches) != 2 || cfg.Code.Branches[0] != "main" || cfg.Code.Branches[1] != "master" {
		t.Errorf("Branches = %v, want default [main master]", cfg.Code.Branches)
	}
}

func Test_Load_when_CommentMentionsPlaceholder_ThenNotExpanded(t *testing.T) {
	// Given: the example config documents ${VAR} syntax inside a comment line.
	t.Setenv("TEST_WEBHOOK_SECRET", "from-env")
	path := writeConfig(t, `
# ${VAR} is documented here and must not be expanded.
code:
  webhook_secret: "${TEST_WEBHOOK_SECRET}"
  domain: "example.com"
`)

	// When
	cfg, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Code.WebhookSecret != "from-env" {
		t.Errorf("WebhookSecret = %q, want from-env", cfg.Code.WebhookSecret)
	}
}

func Test_Load_when_UnsetEnvVar_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "${DEFINITELY_NOT_SET_VAR_XYZ}"
  domain: "example.com"
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
code:
  webhook_secret: ""
  domain: "example.com"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want secret required error")
	}
}

func Test_Load_when_MissingDomain_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: ""
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want domain required error")
	}
}

func Test_Load_when_EmptyBranchEntry_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
  branches:
    - "main"
    - ""
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want empty branch entry error")
	}
}

func Test_Load_when_RegistryConfigured_thenEnabledWithRemoteRef(t *testing.T) {
	// Given
	t.Setenv("TEST_REGISTRY_PASSWORD", "p@ss")
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
registry:
  url: "registry.example.com"
  project: "tenants"
  username: "ci"
  password: "${TEST_REGISTRY_PASSWORD}"
`)

	// When
	cfg, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Registry.Enabled() {
		t.Fatal("Registry.Enabled() = false, want true when url is set")
	}
	if cfg.Registry.Password != "p@ss" {
		t.Errorf("Registry.Password = %q, want expanded env value", cfg.Registry.Password)
	}
	if got, want := cfg.Registry.RemoteRef("app", "a1b2c3d4"), "registry.example.com/tenants/app:a1b2c3d4"; got != want {
		t.Errorf("RemoteRef() = %q, want %q", got, want)
	}
}

func Test_Load_when_RegistryOmitted_thenDisabled(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
`)

	// When
	cfg, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Registry.Enabled() {
		t.Error("Registry.Enabled() = true, want false when section omitted")
	}
}

func Test_Load_when_RegistryUsernameWithoutPassword_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
registry:
  url: "registry.example.com"
  username: "ci"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want username/password pairing error")
	}
}

func Test_Load_when_RegistryPasswordWithoutUsername_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
registry:
  url: "registry.example.com"
  password: "p@ss"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want username/password pairing error")
	}
}

func Test_Load_when_RegistryCredsWithoutURL_thenReturnError(t *testing.T) {
	// Given: credentials are meaningless without a push target.
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
registry:
  username: "ci"
  password: "p@ss"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want creds-without-url error")
	}
}
