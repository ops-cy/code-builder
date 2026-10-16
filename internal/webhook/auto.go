package webhook

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"

	"gitlab-builder/internal/config"
)

// Reasons returned by deriveRepository when an event is rejected before a
// build can be derived from it.
const (
	reasonMissingURL = "missing_repo_url"
	reasonHostDenied = "host_not_allowed"
)

// deriveRepository builds the runtime Repository for a push event entirely
// from the validated event payload plus global policy: clone URL and project
// name come from GitLab. The configured domain is the only access gate — the
// clone URL is untrusted input, so a leaked webhook secret must not be able
// to point the service's deploy key at an arbitrary SSH server.
//
// Returns repo with reason == "", or a zero Repository with a
// machine-readable reason for the 202 response.
func deriveRepository(domain string, imgs *imageRegistry, ev PushEvent) (config.Repository, string) {
	url := ev.Project.GitSSHURL
	if url == "" {
		url = ev.Project.GitHTTPURL
	}
	if url == "" {
		return config.Repository{}, reasonMissingURL
	}
	if !hostMatches(domain, url) {
		return config.Repository{}, reasonHostDenied
	}

	key := normalizeGitURL(url)
	rawName := ev.Project.Name
	if rawName == "" {
		rawName = repoNameFromURL(url)
	}
	name := normalizeDockerName(rawName)
	if name == "" {
		// Unconvertible name (e.g. all CJK/punctuation): stable unique id.
		name = "repo-" + hash8(key)
	}
	return config.Repository{
		Key:   key,
		Name:  name,
		URL:   url,
		Build: config.BuildSpec{Image: imgs.resolve(name, key)},
	}, ""
}

// repoNameFromURL extracts the final path segment of the clone URL without
// the ".git" suffix, as a fallback when the event carries no project name.
func repoNameFromURL(url string) string {
	n := normalizeGitURL(url) // host[:port]/path
	if i := strings.IndexByte(n, '/'); i >= 0 {
		n = n[i+1:]
	}
	if i := strings.LastIndexByte(n, '/'); i >= 0 {
		n = n[i+1:]
	}
	return strings.TrimSuffix(n, ".git")
}

// normalizeDockerName lowercases and reduces s to characters Docker accepts
// in a single repository-name component ([a-z0-9._-], no repeated or edge
// separators). Lossy conversions may collide across repositories; callers
// resolve those collisions via imageRegistry.
func normalizeDockerName(s string) string {
	var b strings.Builder
	lastSep := false
	for _, r := range strings.ToLower(s) {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		isSep := r == '.' || r == '_' || r == '-'
		switch {
		case isAlnum:
			b.WriteRune(r)
			lastSep = false
		case isSep:
			if lastSep {
				continue // collapse separator runs to one char
			}
			b.WriteRune(r)
			lastSep = true
		default:
			if lastSep {
				continue
			}
			b.WriteByte('-')
			lastSep = true
		}
	}
	return strings.Trim(b.String(), "._-")
}

// hash8 returns the first 8 hex chars of the SHA-256 of s: a short stable
// identifier used for name fallbacks and collision suffixes.
func hash8(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// imageRegistry hands out image names without letting two different
// repositories silently share one. The first repository to claim a name owns
// it; a later repository claiming the same name receives a stable
// URL-derived suffix instead of an error, so redeliveries stay deterministic.
type imageRegistry struct {
	mu      sync.Mutex
	byImage map[string]string // image name -> repository key
}

func newImageRegistry() *imageRegistry {
	return &imageRegistry{byImage: make(map[string]string)}
}

// resolve returns the image name for repoKey, disambiguating conflicts.
func (r *imageRegistry) resolve(image, repoKey string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if taken, ok := r.byImage[image]; ok && taken != repoKey {
		alt := image + "-" + hash8(repoKey)
		r.byImage[alt] = repoKey
		return alt
	}
	r.byImage[image] = repoKey
	return image
}

// dedup suppresses duplicate webhook deliveries for a repository commit that
// is already queued or running.
type dedup struct {
	mu sync.Mutex
	m  map[string]string // "<repoKey>@<sha>" -> buildID
}

func newDedup() *dedup {
	return &dedup{m: make(map[string]string)}
}

func (d *dedup) get(k string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id, ok := d.m[k]
	return id, ok
}

func (d *dedup) put(k, buildID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.m[k] = buildID
}

// release drops the key only if buildID still owns it, so a newer build for
// the same commit is never unregistered by a stale run finishing late.
func (d *dedup) release(k, buildID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m[k] == buildID {
		delete(d.m, k)
	}
}
