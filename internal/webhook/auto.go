package webhook

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"sync"

	"code-builder/internal/config"
)

// deriveRepository 在事件被拒绝、无法据此推导构建时返回的原因。
const (
	reasonMissingURL  = "missing_repo_url"
	reasonHostDenied  = "host_not_allowed"
	reasonInvalidName = "invalid_repo_name"
)

// deriveRepository 依据经过校验的事件负载为 Push 事件构造运行时 Repository。
// 平台域名白名单是唯一的访问关卡：clone URL 属于不可信输入，即使 webhook
// Secret 泄露，也不能让服务的部署密钥或 token 指向任意服务器。
func deriveRepository(provider config.Provider, deployKeySet bool, imgs *imageRegistry, ev Event) (config.Repository, string) {
	sshURL := strings.TrimSpace(ev.RepoSSHURL)
	httpURL := strings.TrimSpace(ev.RepoHTTPURL)
	if sshURL == "" && httpURL == "" {
		return config.Repository{}, reasonMissingURL
	}

	// 选择 clone 地址：有平台 token 时优先带凭据的 HTTPS；否则有 Deploy Key
	// 时用 SSH，最后匿名 HTTPS。这样私有仓库与公开仓库都能工作。
	cloneURL := ""
	switch {
	case provider.Token != "" && httpURL != "":
		cloneURL = withHTTPToken(httpURL, provider.Name, provider.Token)
	case deployKeySet && sshURL != "":
		cloneURL = sshURL
	case provider.Name == config.ProviderGitLab && sshURL != "":
		// 保持旧行为：GitLab 默认使用 SSH，兼容容器内默认 ssh 身份。
		cloneURL = sshURL
	case httpURL != "":
		cloneURL = httpURL
	default:
		cloneURL = sshURL
	}

	// 只校验实际使用的 clone 地址（注入 token 前后主机一致）。
	if !hostAllowed(provider.Domains, cloneURL) {
		return config.Repository{}, reasonHostDenied
	}

	keyURL := stripCredentials(cloneURL)
	name := normalizeDockerName(repoNameFromURL(keyURL))
	if name == "" {
		name = normalizeDockerName(repoNameFromURL(ev.FullName))
	}
	if name == "" {
		return config.Repository{}, reasonInvalidName
	}
	fullName := strings.Trim(strings.TrimSpace(ev.FullName), "/")
	if fullName == "" {
		fullName = strings.Trim(strings.TrimPrefix(normalizeGitURL(keyURL), hostOf(keyURL)+"/"), "/")
	}
	namespace := strings.Trim(ev.Namespace, "/")
	if namespace == "" {
		namespace = deriveNamespace(fullName)
	}
	// normalizeGitURL 已去掉 scheme / userinfo / .git，SSH 与 HTTPS 会得到
	// 同一个 key，因此仓库身份在两种 clone 方式下保持一致。
	key := provider.Name + ":" + normalizeGitURL(keyURL)

	return config.Repository{
		Key:       key,
		Name:      name,
		FullName:  fullName,
		Namespace: namespace,
		Provider:  provider.Name,
		URL:       cloneURL,
		Build:     config.BuildSpec{Image: imgs.resolve(name, key)},
	}, ""
}

// withHTTPToken 把平台 token 注入 HTTPS clone URL。非 HTTPS/scp 地址原样返回。
func withHTTPToken(raw, provider, token string) string {
	s := strings.TrimSpace(raw)
	if s == "" || !strings.Contains(s, "://") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return s
	}
	user := "oauth2"
	if provider == config.ProviderGitHub {
		user = "x-access-token"
	}
	u.User = url.UserPassword(user, token)
	return u.String()
}

// normalizeDockerName 将 s 转为小写，并只保留 Docker 单个仓库名组件允许的字符。
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
				continue
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

// hash8 返回 s 的 SHA-256 前 8 个十六进制字符。
func hash8(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// imageRegistry 负责分配镜像名，避免两个不同仓库无提示地共用同一个。
type imageRegistry struct {
	mu      sync.Mutex
	byImage map[string]string // 镜像名 -> 仓库 key
}

func newImageRegistry() *imageRegistry {
	return &imageRegistry{byImage: make(map[string]string)}
}

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

// dedup 用于抑制重复的 webhook 投递。
type dedup struct {
	mu sync.Mutex
	m  map[string]string // "<repoKey>@<sha>" 映射到 buildID
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

func (d *dedup) release(k, buildID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m[k] == buildID {
		delete(d.m, k)
	}
}
