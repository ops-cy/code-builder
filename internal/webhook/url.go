package webhook

import "strings"

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

// hostOf extracts the lowercased, port-less host from a git remote URL or a
// bare/URL-shaped configured host, via normalizeGitURL's "host[:port]/path"
// form. IPv6 literals are out of scope: configuration entries are plain
// hostnames or http(s) URLs.
func hostOf(rawURL string) string {
	n := normalizeGitURL(rawURL)
	if n == "" {
		return ""
	}
	hostport := n
	if i := strings.IndexByte(n, '/'); i >= 0 {
		hostport = n[:i]
	}
	if i := strings.IndexByte(hostport, ':'); i >= 0 {
		hostport = hostport[:i]
	}
	return strings.ToLower(hostport)
}

// hostMatches reports whether the event URL's host equals the configured
// GitLab host. Ports are ignored on both sides: git SSH on :20022 still
// matches the plain domain, and the config may be "code.zmatrix.cn" or
// "https://code.zmatrix.cn".
func hostMatches(configuredHost, rawURL string) bool {
	want := hostOf(configuredHost)
	got := hostOf(rawURL)
	return want != "" && want == got
}
