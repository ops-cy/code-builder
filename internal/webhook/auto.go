package webhook

import (
	"regexp"
	"strings"

	"gitlab-builder/internal/config"
)

// Reasons returned by deriveRepository when the event is rejected.
const (
	reasonMissingURL = "missing_repo_url"
	reasonHostDenied = "host_not_allowed"
)

// invalidNameChars matches every character Docker would refuse in the first
// path component of a repository name; also unsafe as a single directory name.
var invalidNameChars = regexp.MustCompile(`[^a-z0-9._-]+`)

// deriveRepository builds the runtime Repository for a push event entirely
// from the event payload plus the global build config: clone URL, project
// path, and branch come from GitLab. The configured GitLab host is the only
// gate — the event's clone URL is untrusted input, so a leaked webhook
// secret must not be able to point the service's deploy key at an arbitrary
// SSH server.
//
// Returns repo with reason == "", or a zero Repository with a
// machine-readable reason for the 202 response.
func deriveRepository(gitlabHost string, cfg config.BuildConfig, ev PushEvent) (config.Repository, string) {
	url := ev.Project.GitSSHURL
	if url == "" {
		url = ev.Project.GitHTTPURL
	}
	if url == "" {
		return config.Repository{}, reasonMissingURL
	}
	if !hostMatches(gitlabHost, url) {
		return config.Repository{}, reasonHostDenied
	}

	ns := ev.Project.PathWithNamespace
	if ns == "" {
		ns = ev.Project.Name
	}
	return config.Repository{
		Name:          repoNameFrom(ns),
		URL:           url,
		Branches:      cfg.Branches,
		BranchRegex:   cfg.BranchRegex,
		DeployKeyPath: cfg.DeployKeyPath,
		Build: config.BuildSpec{
			Context:    cfg.Context,
			Dockerfile: cfg.Dockerfile,
			Image:      cfg.ImageFor(ns),
			Args:       cfg.Args,
			TagLatest:  cfg.TagLatest,
			NoCache:    cfg.NoCache,
		},
	}, ""
}

// repoNameFrom reduces path_with_namespace to one filesystem-safe component:
// queue serialisation key, workspace directory suffix, and build display name.
func repoNameFrom(pathWithNamespace string) string {
	name := invalidNameChars.ReplaceAllString(strings.ToLower(pathWithNamespace), "-")
	name = strings.Trim(name, "-")
	if name == "" {
		return "unnamed-repo"
	}
	return name
}
