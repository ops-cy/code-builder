// Package config loads and validates the service configuration file.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration document.
type Config struct {
	Server       ServerConfig `yaml:"server"`
	GitLab       GitLabConfig `yaml:"gitlab"`
	Repositories []Repository `yaml:"repositories"`
}

// ServerConfig holds HTTP listener and workspace settings.
type ServerConfig struct {
	Listen      string `yaml:"listen"`
	Workspace   string `yaml:"workspace"`
	LogsDir     string `yaml:"logs_dir"`
	QueueSize   int    `yaml:"queue_size"`
	Concurrency int    `yaml:"concurrency"`
}

// GitLabConfig holds webhook authentication settings.
type GitLabConfig struct {
	WebhookSecret string `yaml:"webhook_secret"`
}

// Repository describes one buildable Git repository.
type Repository struct {
	Name          string    `yaml:"name"`
	URL           string    `yaml:"url"`
	Branches      []string  `yaml:"branches"`
	BranchRegex   string    `yaml:"branch_regex"`
	DeployKeyPath string    `yaml:"deploy_key_path"`
	Build         BuildSpec `yaml:"build"`
}

// BuildSpec describes how to build one repository.
type BuildSpec struct {
	Type       string            `yaml:"type"`
	Context    string            `yaml:"context"`
	Dockerfile string            `yaml:"dockerfile"`
	Image      string            `yaml:"image"`
	Args       map[string]string `yaml:"args"`
	TagLatest  bool              `yaml:"tag_latest"`
	NoCache    bool              `yaml:"no_cache"`
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
	for i := range c.Repositories {
		r := &c.Repositories[i]
		if r.Build.Context == "" {
			r.Build.Context = "."
		}
		if r.Build.Dockerfile == "" {
			r.Build.Dockerfile = "Dockerfile"
		}
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
	if len(c.Repositories) == 0 {
		return fmt.Errorf("config: at least one repository is required")
	}
	seen := make(map[string]struct{}, len(c.Repositories))
	for _, r := range c.Repositories {
		if r.Name == "" {
			return fmt.Errorf("config: repository name must not be empty")
		}
		if _, dup := seen[r.Name]; dup {
			return fmt.Errorf("config: duplicate repository name %q", r.Name)
		}
		seen[r.Name] = struct{}{}
		if r.URL == "" {
			return fmt.Errorf("config: repository %q: url is required", r.Name)
		}
		if r.Build.Image == "" {
			return fmt.Errorf("config: repository %q: build.image is required", r.Name)
		}
		if len(r.Branches) == 0 {
			return fmt.Errorf("config: repository %q: at least one trigger branch is required", r.Name)
		}
		pattern := r.BranchRegex
		if pattern == "" {
			pattern = ".*"
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("config: repository %q: invalid branch_regex %q: %w", r.Name, r.BranchRegex, err)
		}
	}
	return nil
}
