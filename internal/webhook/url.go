package webhook

import "strings"

// normalizeGitURL 将 git remote URL 化简为 "host[:port]/path" 形式：去掉
// 已知的 scheme 和 "git@" 用户前缀，删除 ".git" 后缀，并把 scp 风格的
// "host:path"（路径段不是数字）转换为 host/path，同时保留真正的
// "host:port/path" 写法。
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
			// host:path 且没有斜杠 —— scp 语法。
			s = s[:i] + "/" + rest
		} else if !isAllDigits(rest[:j]) {
			// host:word/path —— 冒号属于 scp 语法，而不是端口。
			s = s[:i] + "/" + rest
		}
		// host:port/path —— 保持原样。
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

// repoNameFromURL 从 clone URL 中提取仓库名：取最后一个 "/" 与可选的 ".git"
// 后缀之间的值。它兼容 https 和 ssh URL、scp 风格的 "host:path" remote、
// 内嵌凭据（user:token@）、query string/fragment 以及结尾斜杠：
//
//	https://gitlab.com/group/Atlas-Market.git        -> Atlas-Market
//	git@gitlab.com:group/atlas-market.git            -> atlas-market
//	https://user:token@gitlab.com/group/repo/        -> repo
//
// 调用方会对返回值做小写化和净化处理。
func repoNameFromURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// 去掉位于路径之前的 userinfo（user[:password]@ 或 token@）。
	if at := strings.IndexByte(s, '@'); at >= 0 {
		if slash := strings.IndexByte(s, '/'); slash < 0 || at < slash {
			s = s[at+1:]
		}
	}
	s = strings.TrimRight(s, "/")
	// 只有主机名（没有路径、也没有 scp 冒号）不包含仓库名。
	if !strings.ContainsAny(s, "/:") {
		return ""
	}
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	// 没有斜杠的 scp 风格 host:repo。
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(s, ".git")
}

// hostOf 借助 normalizeGitURL 的 "host[:port]/path" 形式，从 git remote URL
// 或裸主机名/URL 形式的配置主机中提取小写、不含端口的主机名。不考虑 IPv6
// 字面量：配置项都是普通主机名或 http(s) URL。
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

// hostMatches 判断事件 URL 的主机是否等于配置的 GitLab 主机。两侧都忽略
// 端口：使用 :20022 的 git SSH 仍然匹配纯域名，配置也可以是
// "code.zmatrix.cn" 或 "https://code.zmatrix.cn"。
func hostMatches(configuredHost, rawURL string) bool {
	want := hostOf(configuredHost)
	got := hostOf(rawURL)
	return want != "" && want == got
}
