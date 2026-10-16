// Package config loads and validates the service configuration file.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration document: HTTP runtime settings plus the
// authorised code-hosting instance. Repositories are never listed — any
// project on code.domain that sends a webhook triggers a build, with its URL,
// branch and commit taken from the event payload.
type Config struct {
	Server ServerConfig `yaml:"server"`
	Code   CodeConfig   `yaml:"code"`
}

// ServerConfig holds HTTP listener and workspace settings.
type ServerConfig struct {
	Listen      string `yaml:"listen"`
	Workspace   string `yaml:"workspace"`
	LogsDir     string `yaml:"logs_dir"`
	QueueSize   int    `yaml:"queue_size"`
	Concurrency int    `yaml:"concurrency"`
}

// CodeConfig identifies the authorised GitLab instance and the trigger
// branch whitelist. It is the entire repository-access policy: no per-repo
// fields exist.
type CodeConfig struct {
	// WebhookSecret must equal the GitLab webhook Secret token; supplied via
	// ${GITLAB_WEBHOOK_SECRET} environment expansion (unset vars fail boot).
	WebhookSecret string `yaml:"webhook_secret"`
	// Domain is the GitLab instance repositories live on (e.g.
	// "code.gitlab.com"). Events whose clone URL belongs to any other host
	// are rejected as host_not_allowed. Plain domain or full URL both work;
	// ports are ignored when comparing.
	Domain string `yaml:"domain"`
	// Branches is the exact-match whitelist of push branches that trigger a
	// build; defaults to [main, master] when omitted.
	Branches []string `yaml:"branches"`
}

// Repository is the runtime view of one build job, derived from the webhook
// event. It is never parsed from YAML.
type Repository struct {
	// Key is the normalised clone URL — the identity for queue
	// serialisation and duplicate-delivery detection.
	Key string
	// Name is the sanitised repository name used for display and as the
	// workspace directory prefix.
	Name          string
	URL           string
	DeployKeyPath string
	Build         BuildSpec
}

// BuildSpec is the runtime build description handed to the builder.
// Uniform for every repository by design.
type BuildSpec struct {
	Image string
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
		c.Server.QueueSize = 10
	}
	if c.Server.Concurrency <= 0 {
		c.Server.Concurrency = 2
	}
	if len(c.Code.Branches) == 0 {
		c.Code.Branches = []string{"main", "master"}
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
	return nil
}
