package webhook

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"

	"gitlab-builder/internal/config"
)

// deriveRepository 在事件被拒绝、无法据此推导构建时返回的原因。
const (
	reasonMissingURL  = "missing_repo_url"
	reasonHostDenied  = "host_not_allowed"
	reasonInvalidName = "invalid_repo_name"
)

// deriveRepository 完全依据经过校验的事件负载加上全局策略，为 push 事件构造
// 运行时 Repository：clone URL 和项目名都来自 GitLab。配置的域名是唯一的
// 访问关卡 —— clone URL 属于不可信输入，因此即使 webhook 密钥泄露，也不能
// 让服务的部署密钥指向任意的 SSH 服务器。
//
// 成功时返回 reason == "" 的 repo；否则返回零值 Repository 以及供 202
// 响应的机器可读 reason。
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
	// 仓库名取自 URL 的最后一段路径（转为小写并做净化处理），
	// 同时用作镜像名和部署目标 key。
	name := normalizeDockerName(repoNameFromURL(url))
	if name == "" {
		return config.Repository{}, reasonInvalidName
	}
	return config.Repository{
		Key:   key,
		Name:  name,
		URL:   url,
		Build: config.BuildSpec{Image: imgs.resolve(name, key)},
	}, ""
}

// normalizeDockerName 将 s 转为小写，并只保留 Docker 单个仓库名组件允许的
// 字符（[a-z0-9._-]，不出现重复或首尾分隔符）。有损转换可能让不同仓库发生
// 冲突；调用方通过 imageRegistry 解决这些冲突。
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
				continue // 连续的分隔符折叠为一个字符
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

// hash8 返回 s 的 SHA-256 前 8 个十六进制字符：用于名称回退和冲突后缀的
// 短小且稳定的标识符。
func hash8(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// imageRegistry 负责分配镜像名，避免两个不同的仓库在无提示的情况下共用同一个。
// 先占用某个名字的仓库拥有它；后占用同一名字的仓库会得到一个由 URL 推导的
// 稳定后缀（而不是报错），因此重复投递的结果保持确定性。
type imageRegistry struct {
	mu      sync.Mutex
	byImage map[string]string // 镜像名 -> 仓库 key
}

func newImageRegistry() *imageRegistry {
	return &imageRegistry{byImage: make(map[string]string)}
}

// resolve 返回 repoKey 对应的镜像名，并消除冲突。
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

// dedup 用于抑制重复的 webhook 投递：某个仓库的同一 commit 已在队列中或
// 正在运行时，不再重复处理。
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

// release 仅在 buildID 仍持有该 key 时才删除它，这样同一 commit 的较新构建
// 不会被迟到的旧运行注销。
func (d *dedup) release(k, buildID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m[k] == buildID {
		delete(d.m, k)
	}
}
