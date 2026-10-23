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

func Test_BuildConfig_ResolveTag(t *testing.T) {
	tests := []struct {
		name   string
		cfg    BuildConfig
		branch string
		sha    string
		want   string
	}{
		{
			name:   "nothing configured keeps the sha8 behaviour",
			cfg:    BuildConfig{},
			branch: "main",
			sha:    "a1b2c3d4e5f6",
			want:   "a1b2c3d4",
		},
		{
			name:   "short sha returned verbatim",
			cfg:    BuildConfig{},
			branch: "main",
			sha:    "abc",
			want:   "abc",
		},
		{
			name:   "global tag applies to every branch",
			cfg:    BuildConfig{Tag: "latest"},
			branch: "any",
			sha:    "a1b2c3d4e5f6",
			want:   "latest",
		},
		{
			name:   "branch override wins over the global tag",
			cfg:    BuildConfig{Tag: "latest", BranchTags: map[string]string{"main": "stable"}},
			branch: "main",
			sha:    "a1b2c3d4e5f6",
			want:   "stable",
		},
		{
			name:   "branch override only affects its own branch",
			cfg:    BuildConfig{Tag: "latest", BranchTags: map[string]string{"main": "stable"}},
			branch: "pre",
			sha:    "a1b2c3d4e5f6",
			want:   "latest",
		},
		{
			name:   "empty branch override falls back to the global tag",
			cfg:    BuildConfig{Tag: "latest", BranchTags: map[string]string{"main": ""}},
			branch: "main",
			sha:    "a1b2c3d4e5f6",
			want:   "latest",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ResolveTag(tt.branch, tt.sha); got != tt.want {
				t.Errorf("ResolveTag(%q, %q) = %q, want %q", tt.branch, tt.sha, got, tt.want)
			}
		})
	}
}

func Test_Load_when_BuildTagsConfigured_thenApplied(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
build:
  tag: "latest"
  branch_tags:
    main: "stable"
`)

	// When
	cfg, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Build.ResolveTag("pre", "a1b2c3d4e5f6"); got != "latest" {
		t.Errorf("pre tag = %q, want latest", got)
	}
	if got := cfg.Build.ResolveTag("main", "a1b2c3d4e5f6"); got != "stable" {
		t.Errorf("main tag = %q, want stable", got)
	}
}

func Test_Load_when_BuildTagInvalid_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
build:
  tag: "-bad"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want invalid build.tag error")
	}
}

func Test_Load_when_BranchTagInvalid_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
build:
  branch_tags:
    main: "v1/beta"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want invalid branch tag error")
	}
}

func Test_Load_when_DeployEnabled_thenDefaultsApplied(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
deploy:
  enabled: true
`)

	// When
	cfg, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Deploy.Enabled {
		t.Error("deploy should be enabled")
	}
	if cfg.Deploy.ServicesDir != "/data/services" {
		t.Errorf("ServicesDir = %q, want /data/services", cfg.Deploy.ServicesDir)
	}
	if cfg.Deploy.ComposeFile != "docker-compose.yaml" {
		t.Errorf("ComposeFile = %q, want docker-compose.yaml", cfg.Deploy.ComposeFile)
	}
	if cfg.Deploy.Tag != "latest" {
		t.Errorf("Tag = %q, want latest", cfg.Deploy.Tag)
	}
	if cfg.Deploy.WaitTimeout != "120s" {
		t.Errorf("WaitTimeout = %q, want 120s", cfg.Deploy.WaitTimeout)
	}
}

func Test_Load_when_DeployDisabled_thenValidationSkipped(t *testing.T) {
	// Given: a bogus duration on a disabled deploy must not fail boot.
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
deploy:
  enabled: false
  wait_timeout: "notaduration"
`)

	// When
	_, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v, want nil while deploy disabled", err)
	}
}

func Test_Load_when_DeployEnabledBadWaitTimeout_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
deploy:
  enabled: true
  wait_timeout: "notaduration"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want invalid wait_timeout error")
	}
}

func Test_Load_when_DeployEnabledBadTag_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
deploy:
  enabled: true
  tag: "-bad"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want invalid deploy.tag error")
	}
}

func Test_Load_when_DeployServiceDirEscapes_thenReturnError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
deploy:
  enabled: true
  services:
    evil:
      dir: "../evil"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want path-element error")
	}
}

// Test_ExampleConfig_Loads guards the shipped config/config.yaml: it must stay
// loadable (valid YAML, resolvable placeholders, defaults applied).
func Test_ExampleConfig_Loads(t *testing.T) {
	// Given: the example references ${GITLAB_WEBHOOK_SECRET}.
	t.Setenv("GITLAB_WEBHOOK_SECRET", "example-secret")

	// When
	cfg, err := Load(filepath.Join("..", "..", "config", "config.yaml"))

	// Then
	if err != nil {
		t.Fatalf("Load(config/config.yaml) error = %v", err)
	}
	if cfg.Build.Tag != "" || len(cfg.Build.BranchTags) != 0 {
		t.Errorf("build = %+v, want empty (tags unset by default)", cfg.Build)
	}
	if cfg.Registry.Enabled() {
		t.Errorf("registry should be disabled by default, got url=%q", cfg.Registry.URL)
	}
	if cfg.Deploy.Enabled {
		t.Error("deploy should be disabled by default")
	}
	if cfg.Deploy.ComposeFile != "docker-compose.yaml" {
		t.Errorf("deploy.compose_file = %q, want docker-compose.yaml default", cfg.Deploy.ComposeFile)
	}
}

func Test_Load_when_NotifyDefaults_thenAllEventsAnd10s(t *testing.T) {
	// Given: notify section omitted entirely.
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
	if cfg.Notify.Enabled {
		t.Error("notify should be disabled by default")
	}
	if !cfg.Notify.Started() || !cfg.Notify.Success() || !cfg.Notify.Failure() {
		t.Error("event switches should default to true")
	}
	if cfg.Notify.Lark.Timeout != "10s" {
		t.Errorf("timeout = %q, want 10s", cfg.Notify.Lark.Timeout)
	}
}

func Test_Load_when_NotifyEnabled_thenSwitchesRespected(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
notify:
  enabled: true
  on_started: false
  lark:
    webhook_url: "https://open.feishu.cn/open-apis/bot/v2/hook/token"
`)

	// When
	cfg, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Notify.LarkEnabled() {
		t.Error("lark should be enabled")
	}
	if cfg.Notify.Started() {
		t.Error("on_started=false must disable the start notification")
	}
	if !cfg.Notify.Success() || !cfg.Notify.Failure() {
		t.Error("omitted switches must stay true")
	}
}

func Test_Load_when_NotifyEnabledWithoutWebhook_thenError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
notify:
  enabled: true
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want missing webhook_url error")
	}
}

func Test_Load_when_NotifyWebhookNotHTTPS_thenError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
notify:
  enabled: true
  lark:
    webhook_url: "http://open.feishu.cn/open-apis/bot/v2/hook/token"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want https requirement error")
	}
}

func Test_Load_when_NotifyAllEventsOff_thenError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
notify:
  enabled: true
  on_started: false
  on_success: false
  on_failure: false
  lark:
    webhook_url: "https://open.feishu.cn/open-apis/bot/v2/hook/token"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want all-events-off error")
	}
}

func Test_Load_when_NotifyBadTimeout_thenError(t *testing.T) {
	// Given
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
notify:
  enabled: true
  lark:
    webhook_url: "https://open.feishu.cn/open-apis/bot/v2/hook/token"
    timeout: "notaduration"
`)

	// When
	_, err := Load(path)

	// Then
	if err == nil {
		t.Fatal("Load() error = nil, want invalid timeout error")
	}
}

func Test_Load_when_NotifyDisabled_thenValidationSkipped(t *testing.T) {
	// Given: bogus values on a disabled notifier must not fail boot.
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
notify:
  enabled: false
  lark:
    webhook_url: "http://insecure"
    timeout: "notaduration"
`)

	// When
	_, err := Load(path)

	// Then
	if err != nil {
		t.Fatalf("Load() error = %v, want nil while notify disabled", err)
	}
}
