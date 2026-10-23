// Package config loads and validates the service configuration file.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration document: HTTP runtime settings plus the
// authorised code-hosting instance. Repositories are never listed — any
// project on code.domain that sends a webhook triggers a build, with its URL,
// branch and commit taken from the event payload.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Code     CodeConfig     `yaml:"code"`
	Build    BuildConfig    `yaml:"build"`
	Registry RegistryConfig `yaml:"registry"`
	Deploy   DeployConfig   `yaml:"deploy"`
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

// RegistryConfig is the optional image-push target. It is disabled unless a
// URL is configured, so an omitted section (or an empty url) keeps builds
// local-only — the documented default.
type RegistryConfig struct {
	// URL is the registry host (e.g. "registry.example.com"); it doubles as
	// the enable switch. A trailing slash is tolerated.
	URL string `yaml:"url"`
	// Project is an optional image-name prefix / namespace (e.g. "tenants").
	Project string `yaml:"project"`
	// Username and Password are optional credentials; when both are set a
	// `docker login` runs before the push. When both are empty an already
	// authenticated daemon (or an anonymous registry) is assumed.
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Enabled reports whether built images are pushed after build. The URL is the
// switch: no URL, no push.
func (r RegistryConfig) Enabled() bool {
	return strings.TrimSpace(r.URL) != ""
}

// RemoteRef is the fully-qualified push reference for image:tag:
// "<url>[/<project>]/<image>:<tag>", with redundant slashes normalised.
func (r RegistryConfig) RemoteRef(image, tag string) string {
	base := strings.TrimRight(strings.TrimSpace(r.URL), "/")
	if p := strings.Trim(strings.TrimSpace(r.Project), "/"); p != "" {
		base += "/" + p
	}
	return base + "/" + strings.TrimPrefix(image, "/") + ":" + tag
}

// BuildConfig controls the image tag applied to a successful build. Tags are
// explicit: when neither field is set the commit-sha tag is kept for backward
// compatibility, but configuring either replaces it.
type BuildConfig struct {
	// Tag is the default tag for every branch (e.g. "latest").
	Tag string `yaml:"tag"`
	// BranchTags overrides Tag for specific branches (exact match), e.g.
	// {main: stable, pre: latest}.
	BranchTags map[string]string `yaml:"branch_tags"`
}

// ResolveTag returns the image tag for a build on branch: a branch-specific
// override wins, then the global default, and finally the 8-char commit sha
// (the pre-existing behaviour when nothing is configured).
func (b BuildConfig) ResolveTag(branch, sha string) string {
	if t, ok := b.BranchTags[branch]; ok && t != "" {
		return t
	}
	if b.Tag != "" {
		return b.Tag
	}
	if len(sha) >= 8 {
		return sha[:8]
	}
	return sha
}

// DeployConfig controls the optional post-build `docker compose up -d`. Each
// service lives in its own directory under ServicesDir and is brought up with
// the locally built image, so the compose file must reference
// `image: <service>:<tag>` rather than `build:`.
type DeployConfig struct {
	// Enabled turns deployment on; it only runs when the resolved build tag
	// equals Tag.
	Enabled bool `yaml:"enabled"`
	// ServicesDir is the container path holding one sub-directory per service
	// (e.g. /data/services). It must be mounted at the same path from the host
	// so compose can resolve relative paths and .env files.
	ServicesDir string `yaml:"services_dir"`
	// ComposeFile is the file name inside each service directory. When the
	// configured name is absent, standard names (compose.yaml, ...) are tried.
	ComposeFile string `yaml:"compose_file"`
	// ProjectName overrides the compose project; empty means the service's
	// directory name (compose's own default).
	ProjectName string `yaml:"project_name"`
	// Tag is the build tag that triggers deployment (e.g. "latest").
	Tag string `yaml:"tag"`
	// Wait adds `--wait` and waits up to WaitTimeout for healthy containers.
	Wait        bool   `yaml:"wait"`
	WaitTimeout string `yaml:"wait_timeout"`
	// Services holds optional per-service path/name overrides, keyed by the
	// image (service) name.
	Services map[string]DeployService `yaml:"services"`
}

// DeployService overrides where/how one service is deployed.
type DeployService struct {
	// Dir is the sub-directory under ServicesDir (single path element).
	Dir string `yaml:"dir"`
	// Service is the compose service key to bring up.
	Service string `yaml:"service"`
	// ProjectName overrides the compose project for this service.
	ProjectName string `yaml:"project_name"`
	// ComposeFile overrides the compose file name for this service.
	ComposeFile string `yaml:"compose_file"`
}

// tagPattern matches a valid Docker tag: starts alphanumeric/underscore and is
// at most 128 chars of [A-Za-z0-9_.-].
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// ValidateTag reports whether tag is a legal Docker image tag.
func ValidateTag(tag string) error {
	if !tagPattern.MatchString(tag) {
		return fmt.Errorf("invalid docker tag %q", tag)
	}
	return nil
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
	if c.Deploy.ServicesDir == "" {
		c.Deploy.ServicesDir = "/data/services"
	}
	if c.Deploy.ComposeFile == "" {
		c.Deploy.ComposeFile = "docker-compose.yaml"
	}
	if c.Deploy.Tag == "" {
		c.Deploy.Tag = "latest"
	}
	if c.Deploy.WaitTimeout == "" {
		c.Deploy.WaitTimeout = "120s"
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
	if err := c.Deploy.validate(); err != nil {
		return err
	}
	return nil
}

func (d DeployConfig) validate() error {
	if !d.Enabled {
		return nil
	}
	if strings.TrimSpace(d.ServicesDir) == "" {
		return fmt.Errorf("config: deploy.services_dir is required when deploy.enabled")
	}
	if strings.TrimSpace(d.ComposeFile) == "" {
		return fmt.Errorf("config: deploy.compose_file is required when deploy.enabled")
	}
	if !tagPattern.MatchString(d.Tag) {
		return fmt.Errorf("config: deploy.tag %q is not a valid docker tag", d.Tag)
	}
	if _, err := time.ParseDuration(d.WaitTimeout); err != nil {
		return fmt.Errorf("config: deploy.wait_timeout %q is not a valid duration: %w", d.WaitTimeout, err)
	}
	for image, svc := range d.Services {
		if strings.TrimSpace(image) == "" {
			return fmt.Errorf("config: deploy.services must not contain an empty service name")
		}
		if svc.Dir != "" && !validPathElement(svc.Dir) {
			return fmt.Errorf("config: deploy.services[%s].dir %q must be a single path element", image, svc.Dir)
		}
		if svc.Service != "" && strings.TrimSpace(svc.Service) == "" {
			return fmt.Errorf("config: deploy.services[%s].service must not be blank", image)
		}
		if svc.ComposeFile != "" && !validPathElement(svc.ComposeFile) {
			return fmt.Errorf("config: deploy.services[%s].compose_file %q must be a single path element", image, svc.ComposeFile)
		}
	}
	return nil
}

// validPathElement reports whether s is safe to use as one path segment: it
// must be non-empty, contain no separators, and not be "." or "..".
func validPathElement(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`)
}
