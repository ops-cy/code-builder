// Package config 负责加载并校验服务配置文件。
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是根配置文档：HTTP 运行时设置，以及被授权的代码托管实例。
// 这里从不列出仓库：code.domain 上任何发送 webhook 的项目都会触发构建，
// 其 URL、分支和提交信息都取自事件负载。
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Code     CodeConfig     `yaml:"code"`
	Timeouts TimeoutsConfig `yaml:"timeouts"`
	Build    BuildConfig    `yaml:"build"`
	Registry RegistryConfig `yaml:"registry"`
	Deploy   DeployConfig   `yaml:"deploy"`
	Notify   NotifyConfig   `yaml:"notify"`
}

// ServerConfig 保存 HTTP 监听与工作区设置。
type ServerConfig struct {
	Listen      string `yaml:"listen"`
	Workspace   string `yaml:"workspace"`
	LogsDir     string `yaml:"logs_dir"`
	QueueSize   int    `yaml:"queue_size"`
	Concurrency int    `yaml:"concurrency"`
}

// CodeConfig 指定被授权的 GitLab 实例以及触发构建的分支白名单。
// 它构成了全部的仓库访问策略：不存在按仓库配置的字段。
type CodeConfig struct {
	// WebhookSecret 必须与 GitLab webhook 的 Secret token 一致；通过
	// ${GITLAB_WEBHOOK_SECRET} 环境变量展开提供（变量未设置会导致启动失败）。
	WebhookSecret string `yaml:"webhook_secret"`
	// Domain 是仓库所在 GitLab 实例的域名（例如 "code.gitlab.com"）。
	// 若事件的克隆 URL 属于其他主机，会以 host_not_allowed 拒绝。
	// 纯域名或完整 URL 均可；比较时会忽略端口。
	Domain string `yaml:"domain"`
	// Branches 是触发构建的 push 分支精确匹配白名单；
	// 省略时默认为 [main, master]。
	Branches []string `yaml:"branches"`
}

// RegistryConfig 是可选的镜像推送目标。除非配置了 URL，否则它处于禁用状态，
// 因此省略该配置段（或 url 为空）时构建只在本地进行——这是文档中的默认行为。
type RegistryConfig struct {
	// URL 是 registry 主机（例如 "registry.example.com"）；它同时充当启用开关。
	// 允许结尾带斜杠。
	URL string `yaml:"url"`
	// Project 是可选的镜像名前缀 / 命名空间（例如 "tenants"）。
	Project string `yaml:"project"`
	// Username 和 Password 是可选的凭据；两者都设置时，推送前会执行
	// `docker login`。两者都为空时，则假定 daemon 已通过认证
	//（或该 registry 允许匿名访问）。
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Enabled 报告构建完成后是否会推送镜像。URL 就是开关：没有 URL 就不推送。
func (r RegistryConfig) Enabled() bool {
	return strings.TrimSpace(r.URL) != ""
}

// RemoteRef 返回 image:tag 的完整推送引用：
// "<url>[/<project>]/<image>:<tag>"，重复的斜杠会被归一化。
func (r RegistryConfig) RemoteRef(image, tag string) string {
	base := strings.TrimRight(strings.TrimSpace(r.URL), "/")
	if p := strings.Trim(strings.TrimSpace(r.Project), "/"); p != "" {
		base += "/" + p
	}
	return base + "/" + strings.TrimPrefix(image, "/") + ":" + tag
}

// BuildConfig 控制应用于成功构建的镜像 tag。
// 默认为 "latest"；分支级或全局覆盖会替换它。
type BuildConfig struct {
	// Tag 是所有分支的默认 tag（例如 "latest"）。
	Tag string `yaml:"tag"`
	// BranchTags 为特定分支覆盖 Tag（精确匹配），例如
	// {main: stable, pre: latest}。
	BranchTags map[string]string `yaml:"branch_tags"`
}

// ResolveTag 返回分支上构建所使用的镜像 tag：优先采用分支级覆盖，
// 其次是全局默认值，最后回退到 "latest"。commit SHA 仅为兼容调用方而保留，
// 已不再影响 tag。
func (b BuildConfig) ResolveTag(branch, _ string) string {
	if t, ok := b.BranchTags[branch]; ok && t != "" {
		return t
	}
	if b.Tag != "" {
		return b.Tag
	}
	return "latest"
}

// TimeoutsConfig 独立限制流水线各步骤的耗时。取值是 Go 的
// duration 格式（"15m"、"90s"）；省略的字段在加载时保留默认值。
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

// parseTimeout 解析 s；当 s 为空或非法时回退到 def。
func parseTimeout(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return def
}

// DeployConfig 控制可选的构建后 `docker compose up -d`。
// Targets 以仓库名（URL 最后一段路径，转为小写）为键：
// 若构建的仓库名匹配某个 target，则构建成功后执行部署。
type DeployConfig struct {
	// Enabled 开启部署。部署过程从不比较镜像 tag。
	Enabled bool `yaml:"enabled"`
	// Targets 将仓库名映射到其 compose 服务目录。
	Targets map[string]DeployTarget `yaml:"targets"`
}

// DeployTarget 是构建后启动的一个服务目录。
type DeployTarget struct {
	// Dir 是服务目录在容器内的绝对路径。
	Dir string `yaml:"dir"`
	// ComposeFile 是 Dir 内的 compose 文件名（仅文件名，不含路径）。
	ComposeFile string `yaml:"compose_file"`
	// Service 可选地将 `up -d` 限定为单个 compose 服务；
	// 为空则启动文件中的所有服务。
	Service string `yaml:"service"`
	// Tag 仅为向后兼容而保留，且被忽略：部署从不比较 tag。
	Tag string `yaml:"tag"`
}

// Target 按仓库名查找 target，忽略大小写。
func (d DeployConfig) Target(repoName string) (DeployTarget, bool) {
	name := strings.TrimSpace(repoName)
	for key, t := range d.Targets {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return t, true
		}
	}
	return DeployTarget{}, false
}

// dockerNamePattern 匹配 Docker 仓库名的组成部分（小写，
// 分隔符只能出现在字母数字之间，与 normalizeDockerName 的输出一致）。
var dockerNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// composeServicePattern 匹配 compose 服务键。
var composeServicePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// NotifyConfig 控制流水线通知（可选）。事件开关使用指针，
// 这样省略该键时会保留与 Jenkins 兼容的默认值（true），
// 而显式写成 false 则禁用该事件。
type NotifyConfig struct {
	// Enabled 开启通知器。为 false 时不发送任何通知。
	Enabled bool `yaml:"enabled"`
	// OnStarted / OnSuccess / OnFailure 选择哪些事件需要通知（默认 true）。
	OnStarted *bool `yaml:"on_started"`
	OnSuccess *bool `yaml:"on_success"`
	OnFailure *bool `yaml:"on_failure"`
	// Lark 是 Lark（飞书）自定义机器人渠道；webhook 为空则禁用。
	Lark LarkConfig `yaml:"lark"`
}

// LarkConfig 表示一个 Lark（飞书）自定义机器人 webhook。
type LarkConfig struct {
	// WebhookURL 是完整的机器人 hook URL，例如
	// https://open.feishu.cn/open-apis/bot/v2/hook/<token>。它属于密钥：
	// 永远不会写入日志。
	WebhookURL string `yaml:"webhook_url"`
	// Secret 非空时启用 Lark 的签名校验。
	Secret string `yaml:"secret"`
	// Timeout 限制单次投递尝试的耗时（默认 10s）。
	Timeout string `yaml:"timeout"`
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

// tagPattern 匹配合法的 Docker tag：以字母、数字或下划线开头，
// 最长 128 个字符，字符集为 [A-Za-z0-9_.-]。
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// ValidateTag 报告 tag 是否为合法的 Docker 镜像 tag。
func ValidateTag(tag string) error {
	if !tagPattern.MatchString(tag) {
		return fmt.Errorf("invalid docker tag %q", tag)
	}
	return nil
}

// Repository 是单个构建任务在运行时的视图，由 webhook 事件派生而来。
// 它从不从 YAML 中解析。
type Repository struct {
	// Key 是归一化后的克隆 URL——用于队列串行化和重复投递检测的身份标识。
	Key string
	// Name 是清洗后的仓库名，用于展示并作为工作区目录前缀。
	Name          string
	URL           string
	DeployKeyPath string
	Build         BuildSpec
}

// BuildSpec 是传给构建器的运行时构建描述。
// 按设计对所有仓库保持一致。
type BuildSpec struct {
	Image string
}

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load 读取 path 处的 YAML 文件，展开 ${ENV_VAR} 占位符，
// 应用默认值，并校验结果。
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

// expandEnv 用环境变量值替换 ${VAR}；变量未设置会报错。
// 首个非空白字符为 '#' 的行是注释，会原样保留，
// 因而文档中可以自由提及 ${VAR}。
func expandEnv(raw []byte) ([]byte, error) {
	var missing []string
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if isCommentLine(line) {
			continue
		}
		lines[i] = envPattern.ReplaceAllStringFunc(line, func(m string) string {
			name := envPattern.FindStringSubmatch(m)[1]
			v, ok := os.LookupEnv(name)
			if !ok {
				missing = append(missing, name)
				return m
			}
			return v
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
	if c.Server.QueueSize <= 0 {
		c.Server.QueueSize = 10
	}
	if c.Server.Concurrency <= 0 {
		c.Server.Concurrency = 2
	}
	if len(c.Code.Branches) == 0 {
		c.Code.Branches = []string{"main", "master"}
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
	if c.Notify.Lark.Timeout == "" {
		c.Notify.Lark.Timeout = "10s"
	}
	// 环境变量优先于 YAML 文件，这样 webhook secret 就不必出现在
	// 仓库中的 config.yaml 里。
	if strings.TrimSpace(c.Notify.Lark.WebhookURL) == "" {
		c.Notify.Lark.WebhookURL = strings.TrimSpace(os.Getenv("LARK_WEBHOOK_URL"))
	}
	if c.Notify.Lark.Secret == "" {
		c.Notify.Lark.Secret = os.Getenv("LARK_SECRET")
	}
}

func (c Config) validate() error {
	if c.Code.WebhookSecret == "" {
		return fmt.Errorf("config: code.webhook_secret is required (use ${GITLAB_WEBHOOK_SECRET})")
	}
	if strings.TrimSpace(c.Code.Domain) == "" {
		return fmt.Errorf("config: code.domain is required (the GitLab instance domain, e.g. code.gitlab.com)")
	}
	if c.Server.Concurrency < 1 {
		return fmt.Errorf("config: server.concurrency must be >= 1")
	}
	if c.Server.QueueSize < 1 {
		return fmt.Errorf("config: server.queue_size must be >= 1")
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

// validate 检查每个已配置步骤的超时都是正数时长。
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

// validate 校验 deploy 配置段；被禁用的 deploy 不会校验，
// 因此文件中残留的配置不会阻塞启动。
func (d DeployConfig) validate() error {
	if !d.Enabled {
		return nil
	}
	if len(d.Targets) == 0 {
		return fmt.Errorf("config: deploy.enabled requires at least one deploy.targets entry")
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
		if !dockerNamePattern.MatchString(lower) {
			return fmt.Errorf("config: deploy.targets[%q] must be a valid docker repository name", name)
		}
		if strings.TrimSpace(t.Dir) == "" {
			return fmt.Errorf("config: deploy.targets[%q].dir is required", name)
		}
		if !filepath.IsAbs(t.Dir) {
			return fmt.Errorf("config: deploy.targets[%q].dir %q must be an absolute path", name, t.Dir)
		}
		if strings.TrimSpace(t.ComposeFile) == "" {
			return fmt.Errorf("config: deploy.targets[%q].compose_file is required", name)
		}
		if filepath.Base(t.ComposeFile) != t.ComposeFile || t.ComposeFile == "." || t.ComposeFile == ".." {
			return fmt.Errorf("config: deploy.targets[%q].compose_file %q must be a single file name", name, t.ComposeFile)
		}
		if t.Service != "" && !composeServicePattern.MatchString(t.Service) {
			return fmt.Errorf("config: deploy.targets[%q].service %q is not a valid compose service name", name, t.Service)
		}
	}
	return nil
}

// validate 校验 notify 配置段；被禁用的通知器不会校验，
// 因此配置不完整的渠道不会阻塞启动。
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
