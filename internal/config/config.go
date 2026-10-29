// Package config 负责加载并校验服务配置文件。
package config

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是根配置文档：HTTP 运行时设置、被授权的代码托管平台，
// 以及可选的构建 / 推送 / 部署 / 通知策略。
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Code     CodeConfig     `yaml:"code"`
	Network  NetworkConfig  `yaml:"network"`
	Timeouts TimeoutsConfig `yaml:"timeouts"`
	Build    BuildConfig    `yaml:"build"`
	Registry RegistryConfig `yaml:"registry"`
	Deploy   DeployConfig   `yaml:"deploy"`
	Notify   NotifyConfig   `yaml:"notify"`
}

// NetworkConfig 控制构建时使用的软件包下载源。region=auto 时会探测
// 国内 / 国外镜像的可达性与延迟，并缓存探测结果。
type NetworkConfig struct {
	Region       string `yaml:"region"`
	ProbeTimeout string `yaml:"probe_timeout"`
	CacheTTL     string `yaml:"cache_ttl"`

	GoProxy       string `yaml:"goproxy"`
	GoSumDB       string `yaml:"gosumdb"`
	NPMRegistry   string `yaml:"npm_registry"`
	MavenMirror   string `yaml:"maven_mirror"`
	GradleMirror  string `yaml:"gradle_mirror"`
	CargoRegistry string `yaml:"cargo_registry"`
	APKMirror     string `yaml:"apk_mirror"`
	APTMirror     string `yaml:"apt_mirror"`
}

// ServerConfig 保存 HTTP 监听、工作区与构建记录设置。
type ServerConfig struct {
	Listen      string `yaml:"listen"`
	Workspace   string `yaml:"workspace"`
	LogsDir     string `yaml:"logs_dir"`
	StateFile   string `yaml:"state_file"`
	QueueSize   int    `yaml:"queue_size"`
	Concurrency int    `yaml:"concurrency"`
	LogLevel    string `yaml:"log_level"`
}

// 支持的代码托管平台。
const (
	ProviderGitLab = "gitlab"
	ProviderGitHub = "github"
	ProviderGitee  = "gitee"
)

var supportedProviders = []string{ProviderGitLab, ProviderGitHub, ProviderGitee}

// CodeConfig 指定被授权的代码托管平台以及触发构建的分支白名单。
// 所有平台共用同一个 Webhook Secret。
type CodeConfig struct {
	// WebhookSecret 必须与各平台 Webhook 配置中的 Secret / Token / 密码一致。
	// 未配置时回退到 WEBHOOK_SECRET，再回退到旧的 GITLAB_WEBHOOK_SECRET。
	WebhookSecret string `yaml:"webhook_secret"`
	// Domain 是旧版单域名配置，等价于只启用 GitLab。
	Domain string `yaml:"domain"`
	// Domains 是所有平台共用的域名白名单；平台内 domains 可覆盖它。
	Domains []string `yaml:"domains"`
	// DeployKeyPath 是全局 SSH Deploy Key 在容器内的路径；留空表示不使用 SSH key。
	DeployKeyPath string `yaml:"deploy_key_path"`
	// Branches 是触发构建的分支白名单；支持精确名称和 path.Match 通配符。
	Branches []string `yaml:"branches"`
	// Providers 按平台配置启用状态、域名和可选的 HTTPS clone token。
	Providers map[string]ProviderConfig `yaml:"providers"`
}

// ProviderConfig 是单个平台的配置。
type ProviderConfig struct {
	// Enabled 省略时为 true；显式 false 可关闭该平台。
	Enabled *bool `yaml:"enabled"`
	// Domains 是该平台允许的仓库域名；省略时回退到 code.domains。
	Domains []string `yaml:"domains"`
	// Token 是可选 HTTPS clone 凭据（GitHub / Gitee / GitLab 私有仓库）。
	Token string `yaml:"token"`
}

// Provider 是解析后的平台配置。
type Provider struct {
	Name    string
	Domains []string
	Token   string
}

// ResolveProviders 返回已启用的平台配置。没有 code.providers 时，
// 旧配置 code.domain + code.webhook_secret 视为只启用 GitLab。
func (c CodeConfig) ResolveProviders() (map[string]Provider, error) {
	globalDomains := c.allDomains()

	if len(c.Providers) == 0 {
		if len(globalDomains) == 0 {
			return nil, fmt.Errorf("config: code.domain or code.domains is required")
		}
		return map[string]Provider{
			ProviderGitLab: {Name: ProviderGitLab, Domains: globalDomains},
		}, nil
	}

	out := make(map[string]Provider, len(c.Providers))
	for rawName, pc := range c.Providers {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if !isSupportedProvider(name) {
			return nil, fmt.Errorf("config: unknown code.providers[%q] (supported: %s)",
				rawName, strings.Join(supportedProviders, ", "))
		}
		if pc.Enabled != nil && !*pc.Enabled {
			continue
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("config: code.providers contains duplicate platform %q", name)
		}
		domains := normalizeList(pc.Domains)
		if len(domains) == 0 {
			domains = globalDomains
		}
		if len(domains) == 0 {
			return nil, fmt.Errorf("config: code.providers[%s].domains is required", name)
		}
		out[name] = Provider{Name: name, Domains: domains, Token: strings.TrimSpace(pc.Token)}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("config: at least one code.providers entry must be enabled")
	}
	return out, nil
}

// BranchAllowed 报告分支是否命中白名单。包含 * ? [ 的条目按 path.Match
// 通配符匹配（例如 release/*）；其余条目精确匹配。
func (c CodeConfig) BranchAllowed(branch string) bool {
	for _, pattern := range c.Branches {
		if pattern == branch {
			return true
		}
		if strings.ContainsAny(pattern, "*?[") {
			if ok, err := path.Match(pattern, branch); err == nil && ok {
				return true
			}
		}
	}
	return false
}

func (c CodeConfig) allDomains() []string {
	all := make([]string, 0, len(c.Domains)+1)
	if strings.TrimSpace(c.Domain) != "" {
		all = append(all, strings.TrimSpace(c.Domain))
	}
	all = append(all, normalizeList(c.Domains)...)
	return dedupeStrings(all)
}

func isSupportedProvider(name string) bool {
	for _, p := range supportedProviders {
		if p == name {
			return true
		}
	}
	return false
}

func normalizeList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// RegistryConfig 是可选的镜像推送目标。除非配置了 URL，否则它处于禁用状态。
type RegistryConfig struct {
	URL      string `yaml:"url"`
	Project  string `yaml:"project"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Enabled 报告构建完成后是否会推送镜像。URL 就是开关。
func (r RegistryConfig) Enabled() bool {
	return strings.TrimSpace(r.URL) != ""
}

// RemoteRef 返回 image:tag 的完整推送引用。
func (r RegistryConfig) RemoteRef(image, tag string) string {
	base := strings.TrimRight(strings.TrimSpace(r.URL), "/")
	if p := strings.Trim(strings.TrimSpace(r.Project), "/"); p != "" {
		base += "/" + p
	}
	return base + "/" + strings.TrimPrefix(image, "/") + ":" + tag
}

// BuildConfig 控制镜像 tag、额外构建参数与 docker build 选项。
type BuildConfig struct {
	// Tag 是所有分支的默认 tag（例如 "latest"）。
	Tag string `yaml:"tag"`
	// BranchTags 为特定分支覆盖 Tag（精确匹配）。
	BranchTags map[string]string `yaml:"branch_tags"`
	// ShaTag 为 true 时额外产出一个 sha-<短SHA> 标签。
	ShaTag bool `yaml:"sha_tag"`
	// Platform 传给 docker build --platform，例如 linux/amd64。
	Platform string `yaml:"platform"`
	// Pull 为 true 时传给 docker build --pull。
	Pull bool `yaml:"pull"`
	// Args 是额外 build-arg。
	Args map[string]string `yaml:"args"`
	// Labels 是额外镜像 label。
	Labels map[string]string `yaml:"labels"`
	// CacheFrom 是 docker build --cache-from 列表。
	CacheFrom []string `yaml:"cache_from"`
	// Stack 是技术栈版本与构建参数的默认/覆盖值。
	Stack BuildStackConfig `yaml:"stack"`
}

// BuildStackConfig 控制模板使用的语言版本与构建参数。留空表示自动检测。
type BuildStackConfig struct {
	GoVersion      string `yaml:"go_version"`
	RustVersion    string `yaml:"rust_version"`
	JavaVersion    string `yaml:"java_version"`
	JavaMinVersion string `yaml:"java_min_version"`
	NodeVersion    string `yaml:"node_version"`
	PackageManager string `yaml:"package_manager"`
	CGOEnabled     bool   `yaml:"cgo_enabled"`
}

// ResolveTag 返回分支上构建所使用的镜像 tag。
func (b BuildConfig) ResolveTag(branch, _ string) string {
	if t, ok := b.BranchTags[branch]; ok && t != "" {
		return t
	}
	if b.Tag != "" {
		return b.Tag
	}
	return "latest"
}

// TimeoutsConfig 独立限制流水线各步骤的耗时。
type TimeoutsConfig struct {
	Clone  string `yaml:"clone"`
	Build  string `yaml:"build"`
	Job    string `yaml:"job"`
	Deploy string `yaml:"deploy"`
}

// CloneDuration 返回 clone+checkout 超时时间（默认 15m）。
func (t TimeoutsConfig) CloneDuration() time.Duration { return parseTimeout(t.Clone, 15*time.Minute) }

// BuildDuration 返回 docker build 超时时间（默认 25m）。
func (t TimeoutsConfig) BuildDuration() time.Duration { return parseTimeout(t.Build, 25*time.Minute) }

// JobDuration 返回整条流水线的超时时间（默认 30m）。
func (t TimeoutsConfig) JobDuration() time.Duration { return parseTimeout(t.Job, 30*time.Minute) }

// DeployDuration 返回 docker compose 超时时间（默认 5m）。
func (t TimeoutsConfig) DeployDuration() time.Duration { return parseTimeout(t.Deploy, 5*time.Minute) }

func parseTimeout(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return def
}

// DeployConfig 控制构建后的本机 docker compose 部署。
type DeployConfig struct {
	Enabled bool `yaml:"enabled"`
	// Root 是自动发现服务目录的根路径；标准布局为 <root>/<repo_name>。
	Root string `yaml:"root"`
	// ComposeFiles 是自动探测 compose 文件的顺序。
	ComposeFiles []string `yaml:"compose_files"`
	// Wait 为 true 时使用 compose --wait 等待容器健康；默认关闭。
	Wait *bool `yaml:"wait"`
	// WaitTimeout 是 --wait-timeout 的值（默认 120s）。
	WaitTimeout string `yaml:"wait_timeout"`
	// Pull 传给 compose --pull：never（默认）/ missing / always。
	Pull string `yaml:"pull"`
	// RemoveOrphans 为 true 时传 --remove-orphans；默认关闭。
	RemoveOrphans *bool `yaml:"remove_orphans"`
	// Targets 是显式登记；匹配 owner/repo 或 repo_name，优先于自动发现。
	Targets map[string]DeployTarget `yaml:"targets"`
}

// DeployTarget 是构建后启动的一个服务目录。
type DeployTarget struct {
	Dir         string            `yaml:"dir"`
	ComposeFile string            `yaml:"compose_file"`
	Service     string            `yaml:"service"`
	ProjectName string            `yaml:"project_name"`
	Env         map[string]string `yaml:"env"`
	// Tag 仅为向后兼容而保留，且被忽略。
	Tag string `yaml:"tag"`
}

// DefaultComposeFiles 是自动探测时依次尝试的文件名。
var DefaultComposeFiles = []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}

// WaitEnabled 报告是否使用 compose --wait（默认关闭）。
func (d DeployConfig) WaitEnabled() bool { return d.Wait != nil && *d.Wait }

// RemoveOrphansEnabled 报告是否传 --remove-orphans（默认关闭）。
func (d DeployConfig) RemoveOrphansEnabled() bool {
	return d.RemoveOrphans != nil && *d.RemoveOrphans
}

// ComposeFileCandidates 返回要探测的 compose 文件名。
func (d DeployConfig) ComposeFileCandidates() []string {
	if len(d.ComposeFiles) > 0 {
		return d.ComposeFiles
	}
	return DefaultComposeFiles
}

// Target 按仓库名查找显式登记的目标，忽略大小写。
func (d DeployConfig) Target(repoName string) (DeployTarget, bool) {
	name := strings.TrimSpace(repoName)
	for key, t := range d.Targets {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return t, true
		}
	}
	return DeployTarget{}, false
}

// Match 先按完整仓库名（owner/repo），再按 repo_name 查找显式目标。
func (d DeployConfig) Match(repoName, fullName string) (DeployTarget, bool) {
	if fullName = strings.TrimSpace(fullName); fullName != "" {
		if t, ok := d.Target(fullName); ok {
			return t, true
		}
	}
	return d.Target(repoName)
}

var (
	deployKeyPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*(?:/[A-Za-z0-9][A-Za-z0-9_.-]*)*$`)
	composeServicePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	envNamePattern        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// NotifyConfig 控制流水线通知（可选）。
type NotifyConfig struct {
	Enabled   bool       `yaml:"enabled"`
	OnStarted *bool      `yaml:"on_started"`
	OnSuccess *bool      `yaml:"on_success"`
	OnFailure *bool      `yaml:"on_failure"`
	Lark      LarkConfig `yaml:"lark"`
}

// LarkConfig 表示一个 Lark（飞书）自定义机器人 webhook。
type LarkConfig struct {
	WebhookURL string `yaml:"webhook_url"`
	Secret     string `yaml:"secret"`
	Timeout    string `yaml:"timeout"`
}

// Started 报告流水线开始事件是否发送通知（默认 true）。
func (n NotifyConfig) Started() bool { return n.OnStarted == nil || *n.OnStarted }

// Success 报告成功事件是否发送通知（默认 true）。
func (n NotifyConfig) Success() bool { return n.OnSuccess == nil || *n.OnSuccess }

// Failure 报告失败事件是否发送通知（默认 true）。
func (n NotifyConfig) Failure() bool { return n.OnFailure == nil || *n.OnFailure }

// LarkEnabled 报告通知是否已启用且存在 webhook。
func (n NotifyConfig) LarkEnabled() bool {
	return n.Enabled && strings.TrimSpace(n.Lark.WebhookURL) != ""
}

var (
	tagPattern     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
)

// ValidateTag 报告 tag 是否为合法的 Docker 镜像 tag。
func ValidateTag(tag string) error {
	if !tagPattern.MatchString(tag) {
		return fmt.Errorf("invalid docker tag %q", tag)
	}
	return nil
}

// Repository 是单个构建任务在运行时的视图，由 webhook 事件派生而来。
type Repository struct {
	// Key 是归一化后的克隆 URL（不含凭据），用于队列串行化和去重。
	Key string
	// Name 是清洗后的仓库名，用于镜像名、工作区目录与部署匹配。
	Name string
	// FullName 是平台上的完整仓库名，例如 owner/repo。
	FullName string
	// Namespace 是仓库所属命名空间 / 组织。
	Namespace string
	// Provider 是代码托管平台名称（gitlab / github / gitee）。
	Provider      string
	URL           string
	DeployKeyPath string
	Build         BuildSpec
}

// BuildSpec 是传给构建器的运行时构建描述。
type BuildSpec struct {
	Image string
}

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// Load 读取 path 处的 YAML 文件，展开 ${ENV_VAR} / ${ENV_VAR:-default}
// 占位符，应用默认值，并校验结果。
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	expanded, err := expandEnv(raw)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// expandEnv 用环境变量值替换 ${VAR}；${VAR:-default} 在变量未设置时
// 使用默认值（允许为空）。变量未设置且没有默认值时报错。
func expandEnv(raw []byte) ([]byte, error) {
	var missing []string
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if isCommentLine(line) {
			continue
		}
		lines[i] = envPattern.ReplaceAllStringFunc(line, func(m string) string {
			sub := envPattern.FindStringSubmatch(m)
			name := sub[1]
			def := sub[2]
			hasDefault := strings.Contains(m, ":-")
			if v, ok := os.LookupEnv(name); ok {
				return v
			}
			if hasDefault {
				return def
			}
			missing = append(missing, name)
			return m
		})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("config: unset environment variables: %s", strings.Join(missing, ", "))
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func isCommentLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "#")
}

func (c *Config) applyDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8080"
	}
	if c.Server.Workspace == "" {
		c.Server.Workspace = "/app/workspace"
	}
	if c.Server.LogsDir == "" {
		c.Server.LogsDir = "/app/logs"
	}
	if c.Server.StateFile == "" {
		c.Server.StateFile = filepath.Join(c.Server.LogsDir, "builds.json")
	}
	if c.Server.QueueSize <= 0 {
		c.Server.QueueSize = 10
	}
	if c.Server.Concurrency <= 0 {
		c.Server.Concurrency = 2
	}
	if c.Server.LogLevel == "" {
		c.Server.LogLevel = "info"
	}
	if len(c.Code.Branches) == 0 {
		c.Code.Branches = []string{"main", "master"}
	}
	if c.Network.Region == "" {
		c.Network.Region = "auto"
	}
	if c.Network.ProbeTimeout == "" {
		c.Network.ProbeTimeout = "3s"
	}
	if c.Network.CacheTTL == "" {
		c.Network.CacheTTL = "10m"
	}
	if c.Build.Stack.JavaMinVersion == "" {
		c.Build.Stack.JavaMinVersion = "8"
	}
	if c.Build.Stack.PackageManager == "" {
		c.Build.Stack.PackageManager = "auto"
	}
	if c.Timeouts.Clone == "" {
		c.Timeouts.Clone = "15m"
	}
	if c.Timeouts.Build == "" {
		c.Timeouts.Build = "25m"
	}
	if c.Timeouts.Job == "" {
		c.Timeouts.Job = "30m"
	}
	if c.Timeouts.Deploy == "" {
		c.Timeouts.Deploy = "5m"
	}
	if c.Deploy.Pull == "" {
		c.Deploy.Pull = "never"
	}
	if c.Deploy.WaitTimeout == "" {
		c.Deploy.WaitTimeout = "120s"
	}
	if len(c.Deploy.ComposeFiles) == 0 {
		c.Deploy.ComposeFiles = append([]string(nil), DefaultComposeFiles...)
	}
	if c.Notify.Lark.Timeout == "" {
		c.Notify.Lark.Timeout = "10s"
	}
	// 环境变量可补齐配置文件中留空的 secret；旧变量名继续兼容。
	if strings.TrimSpace(c.Code.WebhookSecret) == "" {
		if v := strings.TrimSpace(os.Getenv("WEBHOOK_SECRET")); v != "" {
			c.Code.WebhookSecret = v
		} else {
			c.Code.WebhookSecret = strings.TrimSpace(os.Getenv("GITLAB_WEBHOOK_SECRET"))
		}
	}
	if strings.TrimSpace(c.Notify.Lark.WebhookURL) == "" {
		c.Notify.Lark.WebhookURL = strings.TrimSpace(os.Getenv("LARK_WEBHOOK_URL"))
	}
	if c.Notify.Lark.Secret == "" {
		c.Notify.Lark.Secret = os.Getenv("LARK_SECRET")
	}
}

func (c Config) validate() error {
	if c.Code.WebhookSecret == "" {
		return fmt.Errorf("config: code.webhook_secret is required (use ${WEBHOOK_SECRET})")
	}
	if _, err := c.Code.ResolveProviders(); err != nil {
		return err
	}
	if err := c.Network.validate(); err != nil {
		return err
	}
	if err := c.Build.Stack.validate(); err != nil {
		return err
	}
	if c.Server.Concurrency < 1 {
		return fmt.Errorf("config: server.concurrency must be >= 1")
	}
	if c.Server.QueueSize < 1 {
		return fmt.Errorf("config: server.queue_size must be >= 1")
	}
	switch strings.ToLower(c.Server.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: server.log_level %q must be debug, info, warn or error", c.Server.LogLevel)
	}
	for _, b := range c.Code.Branches {
		if strings.TrimSpace(b) == "" {
			return fmt.Errorf("config: code.branches must not contain empty entries")
		}
	}
	if (c.Registry.Username == "") != (c.Registry.Password == "") {
		return fmt.Errorf("config: registry.username and registry.password must be set together")
	}
	if !c.Registry.Enabled() && (c.Registry.Username != "" || c.Registry.Password != "") {
		return fmt.Errorf("config: registry.username/password set but registry.url is empty (set registry.url to enable pushing)")
	}
	if c.Build.Tag != "" && !tagPattern.MatchString(c.Build.Tag) {
		return fmt.Errorf("config: build.tag %q is not a valid docker tag", c.Build.Tag)
	}
	for branch, tag := range c.Build.BranchTags {
		if strings.TrimSpace(branch) == "" {
			return fmt.Errorf("config: build.branch_tags must not contain an empty branch name")
		}
		if !tagPattern.MatchString(tag) {
			return fmt.Errorf("config: build.branch_tags[%s] = %q is not a valid docker tag", branch, tag)
		}
	}
	for k := range c.Build.Args {
		if !envNamePattern.MatchString(k) {
			return fmt.Errorf("config: build.args key %q is not a valid build-arg name", k)
		}
	}
	if err := c.Timeouts.validate(); err != nil {
		return err
	}
	if err := c.Deploy.validate(); err != nil {
		return err
	}
	if err := c.Notify.validate(); err != nil {
		return err
	}
	return nil
}

func (t TimeoutsConfig) validate() error {
	for _, f := range []struct {
		name  string
		value string
	}{
		{"clone", t.Clone},
		{"build", t.Build},
		{"job", t.Job},
		{"deploy", t.Deploy},
	} {
		d, err := time.ParseDuration(f.value)
		if err != nil || d <= 0 {
			return fmt.Errorf("config: timeouts.%s %q is not a positive duration", f.name, f.value)
		}
	}
	return nil
}

func (n NetworkConfig) validate() error {
	switch n.Region {
	case "auto", "cn", "global":
	default:
		return fmt.Errorf("config: network.region %q must be auto, cn or global", n.Region)
	}
	for _, f := range []struct {
		name  string
		value string
	}{{"probe_timeout", n.ProbeTimeout}, {"cache_ttl", n.CacheTTL}} {
		d, err := time.ParseDuration(f.value)
		if err != nil || d <= 0 {
			return fmt.Errorf("config: network.%s %q is not a positive duration", f.name, f.value)
		}
	}
	return nil
}

func (s BuildStackConfig) validate() error {
	for _, f := range []struct {
		name  string
		value string
	}{
		{"go_version", s.GoVersion},
		{"rust_version", s.RustVersion},
		{"java_version", s.JavaVersion},
		{"java_min_version", s.JavaMinVersion},
		{"node_version", s.NodeVersion},
	} {
		if f.value != "" && !versionPattern.MatchString(f.value) {
			return fmt.Errorf("config: build.stack.%s %q is not a valid version", f.name, f.value)
		}
	}
	switch s.PackageManager {
	case "", "auto", "npm", "pnpm", "yarn", "bun":
	default:
		return fmt.Errorf("config: build.stack.package_manager %q must be auto, npm, pnpm, yarn or bun", s.PackageManager)
	}
	return nil
}

func (d DeployConfig) validate() error {
	if !d.Enabled {
		return nil
	}
	if strings.TrimSpace(d.Root) == "" && len(d.Targets) == 0 {
		return fmt.Errorf("config: deploy.enabled requires deploy.root or at least one deploy.targets entry")
	}
	if d.Root != "" && !filepath.IsAbs(d.Root) {
		return fmt.Errorf("config: deploy.root %q must be an absolute path", d.Root)
	}
	switch d.Pull {
	case "never", "missing", "always":
	default:
		return fmt.Errorf("config: deploy.pull %q must be never, missing or always", d.Pull)
	}
	if dur, err := time.ParseDuration(d.WaitTimeout); err != nil || dur <= 0 {
		return fmt.Errorf("config: deploy.wait_timeout %q is not a positive duration", d.WaitTimeout)
	}
	for _, f := range d.ComposeFiles {
		if f == "" || filepath.Base(f) != f || f == "." || f == ".." {
			return fmt.Errorf("config: deploy.compose_files entry %q must be a single file name", f)
		}
	}
	seen := make(map[string]string, len(d.Targets))
	for key, t := range d.Targets {
		name := strings.TrimSpace(key)
		if name == "" {
			return fmt.Errorf("config: deploy.targets must not contain an empty name")
		}
		lower := strings.ToLower(name)
		if prev, dup := seen[lower]; dup {
			return fmt.Errorf("config: deploy.targets[%q] duplicates %q (names are case-insensitive)", name, prev)
		}
		seen[lower] = name
		if !deployKeyPattern.MatchString(name) {
			return fmt.Errorf("config: deploy.targets[%q] must be a repository name or owner/repo", name)
		}
		if strings.TrimSpace(t.Dir) == "" {
			return fmt.Errorf("config: deploy.targets[%q].dir is required", name)
		}
		if !filepath.IsAbs(t.Dir) {
			return fmt.Errorf("config: deploy.targets[%q].dir %q must be an absolute path", name, t.Dir)
		}
		if t.ComposeFile != "" && (filepath.Base(t.ComposeFile) != t.ComposeFile || t.ComposeFile == "." || t.ComposeFile == "..") {
			return fmt.Errorf("config: deploy.targets[%q].compose_file %q must be a single file name", name, t.ComposeFile)
		}
		if t.Service != "" && !composeServicePattern.MatchString(t.Service) {
			return fmt.Errorf("config: deploy.targets[%q].service %q is not a valid compose service name", name, t.Service)
		}
		if t.ProjectName != "" && !composeServicePattern.MatchString(t.ProjectName) {
			return fmt.Errorf("config: deploy.targets[%q].project_name %q is not a valid compose project name", name, t.ProjectName)
		}
		for envKey := range t.Env {
			if !envNamePattern.MatchString(envKey) {
				return fmt.Errorf("config: deploy.targets[%q].env key %q is not a valid environment variable name", name, envKey)
			}
		}
	}
	return nil
}

func (n NotifyConfig) validate() error {
	if !n.Enabled {
		return nil
	}
	raw := strings.TrimSpace(n.Lark.WebhookURL)
	if raw == "" {
		return fmt.Errorf("config: notify.enabled requires notify.lark.webhook_url")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("config: notify.lark.webhook_url must be an https URL")
	}
	if !n.Started() && !n.Success() && !n.Failure() {
		return fmt.Errorf("config: notify.enabled but on_started/on_success/on_failure are all false")
	}
	d, err := time.ParseDuration(n.Lark.Timeout)
	if err != nil || d <= 0 {
		return fmt.Errorf("config: notify.lark.timeout %q is not a valid duration", n.Lark.Timeout)
	}
	return nil
}
