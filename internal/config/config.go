// Package config loads and validates the service configuration file.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration document. Repositories are not listed
// here: every project on the GitLab host that sends a webhook to this service
// triggers a build, with its URL and branch taken from the event payload.
type Config struct {
	Server ServerConfig `yaml:"server"`
	GitLab GitLabConfig `yaml:"gitlab"`
	Build  BuildConfig  `yaml:"build"`
}

// ServerConfig holds HTTP listener and workspace settings.
type ServerConfig struct {
	Listen      string `yaml:"listen"`
	Workspace   string `yaml:"workspace"`
	LogsDir     string `yaml:"logs_dir"`
	QueueSize   int    `yaml:"queue_size"`
	Concurrency int    `yaml:"concurrency"`
}

// GitLabConfig identifies the GitLab instance and authenticates webhooks.
type GitLabConfig struct {
	// Host is the GitLab domain repositories live on (e.g.
	// "code.zmatrix.cn"). Plain domain or full URL both work; only events
	// whose clone URL belongs to this host are accepted — everything else
	// from a valid secret is rejected as host_not_allowed.
	Host          string `yaml:"host"`
	WebhookSecret string `yaml:"webhook_secret"`
}

// BuildConfig holds the global build defaults applied to every triggered
// repository; per-repo overrides do not exist.
type BuildConfig struct {
	Branches      []string          `yaml:"branches"`
	BranchRegex   string            `yaml:"branch_regex"`
	ImagePrefix   string            `yaml:"image_prefix"`
	Dockerfile    string            `yaml:"dockerfile"` // "auto" (default) | template name | repo path
	Context       string            `yaml:"context"`
	DeployKeyPath string            `yaml:"deploy_key_path"`
	Args          map[string]string `yaml:"args"`
	TagLatest     bool              `yaml:"tag_latest"`
	NoCache       bool              `yaml:"no_cache"`
}

// ImageFor derives the local image name from a project's path_with_namespace,
// sanitised to characters Docker accepts as a repository name.
func (b BuildConfig) ImageFor(pathWithNamespace string) string {
	sanitised := invalidImageChars.ReplaceAllString(strings.ToLower(pathWithNamespace), "-")
	sanitised = strings.Trim(sanitised, "/")
	if sanitised == "" {
		sanitised = "unnamed-repo"
	}
	prefix := strings.TrimSuffix(b.ImagePrefix, "/")
	if prefix == "" {
		return sanitised
	}
	return strings.ToLower(prefix) + "/" + sanitised
}

var invalidImageChars = regexp.MustCompile(`[^a-z0-9._/-]+`)

// Repository is the runtime view of one build job, derived from the webhook
// event plus the global build config. It is never parsed from YAML.
type Repository struct {
	Name          string
	URL           string
	Branches      []string
	BranchRegex   string
	DeployKeyPath string
	Build         BuildSpec
}

// BuildSpec is the runtime build description handed to the builder.
type BuildSpec struct {
	Context    string
	Dockerfile string
	Image      string
	Args       map[string]string
	TagLatest  bool
	NoCache    bool
}

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load reads the YAML file at path, expands ${ENV_VAR} placeholders, applies
// defaults, and validates the result.
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

// expandEnv substitutes ${VAR} with the environment value; unset vars are an
// error. Lines whose first non-space character is '#' are comments and are
// copied verbatim so documentation may mention ${VAR} freely.
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
		c.Server.QueueSize = 100
	}
	if c.Server.Concurrency <= 0 {
		c.Server.Concurrency = 2
	}
	if len(c.Build.Branches) == 0 {
		c.Build.Branches = []string{"main", "master"}
	}
	if c.Build.BranchRegex == "" {
		c.Build.BranchRegex = ".*"
	}
	if c.Build.Dockerfile == "" {
		c.Build.Dockerfile = "auto"
	}
	if c.Build.Context == "" {
		c.Build.Context = "."
	}
}

func (c Config) validate() error {
	if c.GitLab.WebhookSecret == "" {
		return fmt.Errorf("config: gitlab.webhook_secret is required")
	}
	if c.Server.Concurrency < 1 {
		return fmt.Errorf("config: server.concurrency must be >= 1")
	}
	if c.Server.QueueSize < 1 {
		return fmt.Errorf("config: server.queue_size must be >= 1")
	}
	if strings.TrimSpace(c.GitLab.Host) == "" {
		return fmt.Errorf("config: gitlab.host is required (the GitLab domain, e.g. code.zmatrix.cn)")
	}
	if _, err := regexp.Compile(c.Build.BranchRegex); err != nil {
		return fmt.Errorf("config: build.branch_regex %q: %w", c.Build.BranchRegex, err)
	}
	return nil
}
