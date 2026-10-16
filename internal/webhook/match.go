package webhook

import (
	"strings"

	"gitlab-builder/internal/config"
)

// MatchRepo finds the configured repository matching the event's project
// URLs (SSH or HTTP). Both sides are normalised to host[:port]/path form and
// compared with a case-insensitive host part.
func MatchRepo(repos []config.Repository, sshURL, httpURL string) (config.Repository, bool) {
	for _, r := range repos {
		norm := normalizeGitURL(r.URL)
		if norm != "" && (norm == normalizeGitURL(sshURL) || norm == normalizeGitURL(httpURL)) {
			return r, true
		}
	}
	return config.Repository{}, false
}

// normalizeGitURL reduces a git remote URL to "host[:port]/path" form:
// strips known schemes and the "git@" user, drops a ".git" suffix, and
// converts scp-style "host:path" (non-numeric path segment) to host/path
// while preserving real "host:port/path" addressing.
func normalizeGitURL(u string) string {
	s := strings.TrimSpace(u)
	if s == "" {
		return ""
	}
	for _, scheme := range []string{"ssh://", "git://", "https://", "http://"} {
		s = strings.TrimPrefix(s, scheme)
	}
	s = strings.TrimPrefix(s, "git@")
	s = strings.TrimSuffix(s, ".git")

	if i := strings.IndexByte(s, ':'); i >= 0 {
		rest := s[i+1:]
		if j := strings.IndexByte(rest, '/'); j < 0 {
			// host:path with no slash — scp syntax.
			s = s[:i] + "/" + rest
		} else if !isAllDigits(rest[:j]) {
			// host:word/path — the colon belongs to scp syntax, not a port.
			s = s[:i] + "/" + rest
		}
		// host:port/path — keep as is.
	}

	if i := strings.IndexByte(s, '/'); i >= 0 {
		return strings.ToLower(s[:i]) + s[i:]
	}
	return strings.ToLower(s)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
