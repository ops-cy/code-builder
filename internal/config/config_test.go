package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	// 前提
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

	// 操作
	cfg, err := Load(path)

	// 断言
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
	// 前提：省略 branches → 使用默认白名单；省略 queue_size → 10。
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
`)

	// 操作
	cfg, err := Load(path)

	// 断言
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
	// 前提：示例配置在注释行中演示了 ${VAR} 语法。
	t.Setenv("TEST_WEBHOOK_SECRET", "from-env")
	path := writeConfig(t, `
# 这里演示 ${VAR} 语法，不能被展开。
code:
  webhook_secret: "${TEST_WEBHOOK_SECRET}"
  domain: "example.com"
`)

	// 操作
	cfg, err := Load(path)

	// 断言
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Code.WebhookSecret != "from-env" {
		t.Errorf("WebhookSecret = %q, want from-env", cfg.Code.WebhookSecret)
	}
}

func Test_Load_when_UnsetEnvVar_thenReturnError(t *testing.T) {
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "${DEFINITELY_NOT_SET_VAR_XYZ}"
  domain: "example.com"
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want unset env var error")
	}
}

func Test_Load_when_MissingSecret_thenReturnError(t *testing.T) {
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: ""
  domain: "example.com"
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want secret required error")
	}
}

func Test_Load_when_MissingDomain_thenReturnError(t *testing.T) {
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: ""
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want domain required error")
	}
}

func Test_Load_when_EmptyBranchEntry_thenReturnError(t *testing.T) {
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
  branches:
    - "main"
    - ""
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want empty branch entry error")
	}
}

func Test_Load_when_RegistryConfigured_thenEnabledWithRemoteRef(t *testing.T) {
	// 前提
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

	// 操作
	cfg, err := Load(path)

	// 断言
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
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
`)

	// 操作
	cfg, err := Load(path)

	// 断言
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Registry.Enabled() {
		t.Error("Registry.Enabled() = true, want false when section omitted")
	}
}

func Test_Load_when_RegistryUsernameWithoutPassword_thenReturnError(t *testing.T) {
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
registry:
  url: "registry.example.com"
  username: "ci"
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want username/password pairing error")
	}
}

func Test_Load_when_RegistryPasswordWithoutUsername_thenReturnError(t *testing.T) {
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
registry:
  url: "registry.example.com"
  password: "p@ss"
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want username/password pairing error")
	}
}

func Test_Load_when_RegistryCredsWithoutURL_thenReturnError(t *testing.T) {
	// 前提：没有推送目标时，凭据没有意义。
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
registry:
  username: "ci"
  password: "p@ss"
`)

	// 操作
	_, err := Load(path)

	// 断言
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
			name:   "nothing configured defaults to latest",
			cfg:    BuildConfig{},
			branch: "main",
			sha:    "a1b2c3d4e5f6",
			want:   "latest",
		},
		{
			name:   "short sha still defaults to latest",
			cfg:    BuildConfig{},
			branch: "main",
			sha:    "abc",
			want:   "latest",
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
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
build:
  tag: "latest"
  branch_tags:
    main: "stable"
`)

	// 操作
	cfg, err := Load(path)

	// 断言
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
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
build:
  tag: "-bad"
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want invalid build.tag error")
	}
}

func Test_Load_when_BranchTagInvalid_thenReturnError(t *testing.T) {
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
build:
  branch_tags:
    main: "v1/beta"
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want invalid branch tag error")
	}
}

// Test_ExampleConfig_Loads 守护随附的 config/config.yaml：它必须保持
// 可加载（YAML 合法、占位符可解析、默认值已应用）。
func Test_ExampleConfig_Loads(t *testing.T) {
	// 前提：示例配置引用了 ${WEBHOOK_SECRET}。
	t.Setenv("WEBHOOK_SECRET", "example-secret")

	// 操作
	cfg, err := Load(filepath.Join("..", "..", "config", "config.yaml"))

	// 断言
	if err != nil {
		t.Fatalf("Load(config/config.yaml) error = %v", err)
	}
	if cfg.Build.Tag != "" || len(cfg.Build.BranchTags) != 0 {
		t.Errorf("build = %+v, want empty (tags unset by default)", cfg.Build)
	}
	if cfg.Registry.Enabled() {
		t.Errorf("registry should be disabled by default, got url=%q", cfg.Registry.URL)
	}
}

func Test_Load_when_NotifyDefaults_thenAllEventsAnd10s(t *testing.T) {
	// 前提：完全省略 notify 配置段。
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
`)

	// 操作
	cfg, err := Load(path)

	// 断言
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
	// 前提
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

	// 操作
	cfg, err := Load(path)

	// 断言
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
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
notify:
  enabled: true
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want missing webhook_url error")
	}
}

func Test_Load_when_NotifyWebhookNotHTTPS_thenError(t *testing.T) {
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
notify:
  enabled: true
  lark:
    webhook_url: "http://open.feishu.cn/open-apis/bot/v2/hook/token"
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want https requirement error")
	}
}

func Test_Load_when_NotifyAllEventsOff_thenError(t *testing.T) {
	// 前提
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

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want all-events-off error")
	}
}

func Test_Load_when_NotifyBadTimeout_thenError(t *testing.T) {
	// 前提
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

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil {
		t.Fatal("Load() error = nil, want invalid timeout error")
	}
}

func Test_Load_when_NotifyDisabled_thenValidationSkipped(t *testing.T) {
	// 前提：已禁用的通知器上的无意义取值不得导致启动失败。
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

	// 操作
	_, err := Load(path)

	// 断言
	if err != nil {
		t.Fatalf("Load() error = %v, want nil while notify disabled", err)
	}
}

func Test_Load_when_TimeoutsOmitted_thenDefaults(t *testing.T) {
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
`)

	// 操作
	cfg, err := Load(path)

	// 断言
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Timeouts.CloneDuration(); got != 15*time.Minute {
		t.Errorf("CloneDuration() = %v, want 15m", got)
	}
	if got := cfg.Timeouts.BuildDuration(); got != 25*time.Minute {
		t.Errorf("BuildDuration() = %v, want 25m", got)
	}
	if got := cfg.Timeouts.JobDuration(); got != 30*time.Minute {
		t.Errorf("JobDuration() = %v, want 30m", got)
	}
	if got := cfg.Timeouts.DeployDuration(); got != 5*time.Minute {
		t.Errorf("DeployDuration() = %v, want 5m", got)
	}
}

func Test_Load_when_TimeoutsConfigured_thenApplied(t *testing.T) {
	// 前提
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
timeouts:
  clone: "90s"
  build: "40m"
  job: "45m"
  deploy: "2m"
`)

	// 操作
	cfg, err := Load(path)

	// 断言
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Timeouts.CloneDuration() != 90*time.Second || cfg.Timeouts.BuildDuration() != 40*time.Minute {
		t.Errorf("timeouts = %+v, want clone 90s build 40m", cfg.Timeouts)
	}
	if cfg.Timeouts.JobDuration() != 45*time.Minute || cfg.Timeouts.DeployDuration() != 2*time.Minute {
		t.Errorf("timeouts = %+v, want job 45m deploy 2m", cfg.Timeouts)
	}
}

func Test_Load_when_TimeoutInvalid_thenError(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not a duration", `timeouts: {clone: "soon"}`},
		{"zero", `timeouts: {build: "0s"}`},
		{"negative", `timeouts: {job: "-1m"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, "code:\n  webhook_secret: \"x\"\n  domain: \"example.com\"\n"+tt.body+"\n")
			if _, err := Load(path); err == nil {
				t.Fatal("Load() error = nil, want timeout error")
			}
		})
	}
}

const deployConfigHead = `
code:
  webhook_secret: "x"
  domain: "example.com"
deploy:
  enabled: true
  targets:
`

func Test_Load_when_DeployTargetsConfigured_thenLookupIgnoresCase(t *testing.T) {
	// 前提
	dir := filepath.ToSlash(filepath.Join(t.TempDir(), "atlas-market"))
	path := writeConfig(t, deployConfigHead+
		"    Atlas-Market:\n"+
		"      dir: \""+dir+"\"\n"+
		"      compose_file: \"docker-compose.yaml\"\n"+
		"      service: \"api\"\n"+
		"      tag: \"ignored\"\n")

	// 操作
	cfg, err := Load(path)

	// 断言
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Deploy.Enabled {
		t.Fatal("Deploy.Enabled = false")
	}
	for _, key := range []string{"atlas-market", "ATLAS-MARKET", " Atlas-Market "} {
		tgt, ok := cfg.Deploy.Target(key)
		if !ok {
			t.Errorf("Target(%q) not found", key)
			continue
		}
		if tgt.Dir != dir || tgt.ComposeFile != "docker-compose.yaml" || tgt.Service != "api" {
			t.Errorf("Target(%q) = %+v", key, tgt)
		}
	}
	if _, ok := cfg.Deploy.Target("other"); ok {
		t.Error("Target(other) found, want miss")
	}
}

func Test_Load_when_DeployDisabled_thenValidationSkipped(t *testing.T) {
	// 前提：在禁用状态下，残留或不完整的 targets 不得阻塞启动。
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
deploy:
  enabled: false
  targets:
    Bad Name:
      dir: "relative/dir"
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err != nil {
		t.Fatalf("Load() error = %v, want nil while deploy disabled", err)
	}
}

func Test_Load_when_DeployInvalid_thenError(t *testing.T) {
	abs := filepath.ToSlash(filepath.Join(t.TempDir(), "app"))
	tests := []struct {
		name   string
		target string
	}{
		{
			name: "no targets",
			target: `
`,
		},
		{
			name: "duplicate case-insensitive key",
			target: `    atlas:
      dir: "{{DIR}}"
      compose_file: "docker-compose.yaml"
    ATLAS:
      dir: "{{DIR}}"
      compose_file: "docker-compose.yaml"
`,
		},
		{
			name: "invalid name",
			target: `    "bad name":
      dir: "{{DIR}}"
      compose_file: "docker-compose.yaml"
`,
		},
		{
			name: "missing dir",
			target: `    app:
      compose_file: "docker-compose.yaml"
`,
		},
		{
			name: "relative dir",
			target: `    app:
      dir: "./services/app"
      compose_file: "docker-compose.yaml"
`,
		},
		{
			name: "compose file with path",
			target: `    app:
      dir: "{{DIR}}"
      compose_file: "sub/docker-compose.yaml"
`,
		},
		{
			name: "invalid service",
			target: `    app:
      dir: "{{DIR}}"
      compose_file: "docker-compose.yaml"
      service: "bad service"
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.ReplaceAll(tt.target, "{{DIR}}", abs)
			path := writeConfig(t, deployConfigHead+body)
			if _, err := Load(path); err == nil {
				t.Fatal("Load() error = nil, want deploy validation error")
			}
		})
	}
}

func Test_Load_when_DeployTargetMissingFields_thenErrorMentionsTarget(t *testing.T) {
	// 前提
	path := writeConfig(t, deployConfigHead+`    app:
      compose_file: "docker-compose.yaml"
`)

	// 操作
	_, err := Load(path)

	// 断言
	if err == nil || !strings.Contains(err.Error(), "app") || !strings.Contains(err.Error(), "dir") {
		t.Errorf("error = %v, want the app target's missing dir", err)
	}
}

func Test_ResolveProviders_legacyConfigEnablesGitLabOnly(t *testing.T) {
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "code.gitlab.com"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	providers, err := cfg.Code.ResolveProviders()
	if err != nil {
		t.Fatalf("ResolveProviders() error = %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers = %v, want GitLab only", providers)
	}
	p, ok := providers[ProviderGitLab]
	if !ok || len(p.Domains) != 1 || p.Domains[0] != "code.gitlab.com" {
		t.Errorf("gitlab provider = %+v", p)
	}
}

func Test_ResolveProviders_multiPlatformSharedSecret(t *testing.T) {
	t.Setenv("TEST_SHARED_SECRET", "shared")
	t.Setenv("TEST_GITHUB_TOKEN", "gh-token")
	path := writeConfig(t, `
code:
  webhook_secret: "${TEST_SHARED_SECRET}"
  branches: ["main", "release/*"]
  providers:
    gitlab:
      enabled: true
      domains: ["gitlab.example.com"]
    github:
      enabled: true
      domains: ["github.com"]
      token: "${TEST_GITHUB_TOKEN}"
    gitee:
      enabled: false
      domains: ["gitee.com"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	providers, err := cfg.Code.ResolveProviders()
	if err != nil {
		t.Fatalf("ResolveProviders() error = %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers = %v, want gitlab + github", providers)
	}
	if providers[ProviderGitHub].Token != "gh-token" {
		t.Errorf("github token = %q", providers[ProviderGitHub].Token)
	}
	if _, ok := providers[ProviderGitee]; ok {
		t.Error("disabled gitee provider must not be returned")
	}
	if !cfg.Code.BranchAllowed("release/1.2") || cfg.Code.BranchAllowed("feature/x") {
		t.Errorf("branch glob matching failed: %v", cfg.Code.Branches)
	}
}

func Test_Load_when_UnknownProvider_thenError(t *testing.T) {
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  providers:
    bitbucket:
      enabled: true
      domains: ["bitbucket.org"]
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want unknown provider error")
	}
}

func Test_ExpandEnv_withOptionalDefault(t *testing.T) {
	t.Setenv("TEST_SET_VAR", "value")
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
  providers:
    gitlab:
      domains: ["example.com"]
      token: "${TEST_SET_VAR:-fallback}"
    github:
      domains: ["github.com"]
      token: "${TEST_UNSET_VAR:-}"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Code.Providers["gitlab"].Token != "value" {
		t.Errorf("gitlab token = %q, want env value", cfg.Code.Providers["gitlab"].Token)
	}
	if cfg.Code.Providers["github"].Token != "" {
		t.Errorf("github token = %q, want empty default", cfg.Code.Providers["github"].Token)
	}
}

func Test_Load_when_DeployRootOnly_thenValid(t *testing.T) {
	root := filepath.ToSlash(filepath.Join(t.TempDir(), "services"))
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
deploy:
  enabled: true
  root: "`+root+`"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Deploy.Enabled || cfg.Deploy.Root != root {
		t.Errorf("deploy = %+v", cfg.Deploy)
	}
	if !cfg.Deploy.WaitEnabled() || !cfg.Deploy.RemoveOrphansEnabled() {
		t.Errorf("deploy defaults not applied: %+v", cfg.Deploy)
	}
}

func Test_Load_when_LarkEnvSet_thenEnvFillsMissingValues(t *testing.T) {
	// 前提：webhook 和 secret 仅存在于环境变量中。
	t.Setenv("LARK_WEBHOOK_URL", "https://open.feishu.cn/open-apis/bot/v2/hook/env")
	t.Setenv("LARK_SECRET", "env-secret")
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
notify:
  enabled: true
  lark:
    webhook_url: ""
    secret: ""
`)

	// 操作
	cfg, err := Load(path)

	// 断言
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Notify.Lark.WebhookURL != "https://open.feishu.cn/open-apis/bot/v2/hook/env" {
		t.Errorf("WebhookURL = %q, want the env value", cfg.Notify.Lark.WebhookURL)
	}
	if cfg.Notify.Lark.Secret != "env-secret" {
		t.Errorf("Secret = %q, want the env value", cfg.Notify.Lark.Secret)
	}
	if !cfg.Notify.LarkEnabled() {
		t.Error("LarkEnabled() = false, want true")
	}
}

func Test_Load_when_LarkEnvSetButYAMLConfigured_thenYAMLWins(t *testing.T) {
	// 前提
	t.Setenv("LARK_WEBHOOK_URL", "https://open.feishu.cn/open-apis/bot/v2/hook/env")
	t.Setenv("LARK_SECRET", "env-secret")
	path := writeConfig(t, `
code:
  webhook_secret: "x"
  domain: "example.com"
notify:
  enabled: true
  lark:
    webhook_url: "https://open.feishu.cn/open-apis/bot/v2/hook/yaml"
    secret: "yaml-secret"
`)

	// 操作
	cfg, err := Load(path)

	// 断言
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Notify.Lark.WebhookURL != "https://open.feishu.cn/open-apis/bot/v2/hook/yaml" {
		t.Errorf("WebhookURL = %q, want the YAML value", cfg.Notify.Lark.WebhookURL)
	}
	if cfg.Notify.Lark.Secret != "yaml-secret" {
		t.Errorf("Secret = %q, want the YAML value", cfg.Notify.Lark.Secret)
	}
}
