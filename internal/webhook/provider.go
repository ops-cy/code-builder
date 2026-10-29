package webhook

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"code-builder/internal/config"
)

// Event 是三个代码托管平台 Push 事件的统一视图。
type Event struct {
	Provider      string
	Kind          string // push / ping / other
	Ref           string
	Before        string
	After         string
	CheckoutSHA   string
	Deleted       bool
	RepoSSHURL    string
	RepoHTTPURL   string
	FullName      string
	Namespace     string
	Actor         string
	CommitSubject string
	DeliveryID    string
}

// IsPush 报告事件是否为可构建的分支 Push。
func (e Event) IsPush() bool { return e.Kind == "push" }

// IsDelete 报告事件是否为删除 ref（没有新的 commit）。
func (e Event) IsDelete() bool {
	if e.Deleted {
		return true
	}
	after := strings.TrimSpace(e.After)
	if after == "" {
		return strings.TrimSpace(e.CheckoutSHA) == ""
	}
	return allZero(after)
}

// Branch 去掉 refs/heads/ 前缀；非分支 ref 返回 ok=false。
func (e Event) Branch() (string, bool) {
	const prefix = "refs/heads/"
	if len(e.Ref) <= len(prefix) || e.Ref[:len(prefix)] != prefix {
		return "", false
	}
	return e.Ref[len(prefix):], true
}

// SHA 返回本次构建应检出的 commit：GitLab 优先 checkout_sha，
// GitHub / Gitee 使用 after。
func (e Event) SHA() string {
	if s := strings.TrimSpace(e.CheckoutSHA); s != "" {
		return s
	}
	return strings.TrimSpace(e.After)
}

func allZero(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != '0' {
			return false
		}
	}
	return true
}

var errUnknownProvider = errors.New("unknown webhook provider")

// detectProvider 根据请求头与 JSON 顶层字段识别平台。显式 /webhook/:provider
// 入口不会调用它。
func detectProvider(r *http.Request, body []byte) (string, error) {
	switch {
	case strings.TrimSpace(r.Header.Get("X-GitHub-Event")) != "":
		return config.ProviderGitHub, nil
	case strings.TrimSpace(r.Header.Get("X-Gitlab-Event")) != "" ||
		strings.TrimSpace(r.Header.Get("X-Gitlab-Token")) != "":
		return config.ProviderGitLab, nil
	case strings.TrimSpace(r.Header.Get("X-Gitee-Event")) != "" ||
		strings.TrimSpace(r.Header.Get("X-Gitee-Token")) != "" ||
		strings.TrimSpace(r.Header.Get("X-Gitee-Signature")) != "":
		return config.ProviderGitee, nil
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", errUnknownProvider
	}
	if _, ok := probe["object_kind"]; ok {
		return config.ProviderGitLab, nil
	}
	if _, ok := probe["zen"]; ok {
		return config.ProviderGitHub, nil
	}
	if _, ok := probe["hook_id"]; ok {
		return config.ProviderGitHub, nil
	}
	if _, ok := probe["hook_name"]; ok {
		return config.ProviderGitee, nil
	}
	return "", errUnknownProvider
}

// verifyWebhook 使用全局 Secret 校验请求来源。GitLab 使用静态 Token，
// GitHub 使用 HMAC-SHA256（兼容旧 SHA1），Gitee 使用签名或 Token/密码。
func verifyWebhook(provider, secret string, r *http.Request, body []byte) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("webhook secret is not configured")
	}
	switch provider {
	case config.ProviderGitLab:
		if !constantTimeEqual(r.Header.Get("X-Gitlab-Token"), secret) {
			return errors.New("invalid gitlab token")
		}
		return nil
	case config.ProviderGitHub:
		if sig := r.Header.Get("X-Hub-Signature-256"); sig != "" {
			sum := hmacSHA256(secret, body)
			return verifyHexSignature(sig, "sha256=", sum)
		}
		if sig := r.Header.Get("X-Hub-Signature"); sig != "" {
			mac := hmac.New(sha1.New, []byte(secret))
			_, _ = mac.Write(body)
			return verifyHexSignature(sig, "sha1=", mac.Sum(nil))
		}
		return errors.New("missing github signature")
	case config.ProviderGitee:
		if sig := strings.TrimSpace(r.Header.Get("X-Gitee-Signature")); sig != "" {
			if strings.HasPrefix(strings.ToLower(sig), "sha1=") {
				mac := hmac.New(sha1.New, []byte(secret))
				_, _ = mac.Write(body)
				return verifyHexSignature(sig, "sha1=", mac.Sum(nil))
			}
			return verifyHexSignature(sig, "sha256=", hmacSHA256(secret, body))
		}
		if token := r.Header.Get("X-Gitee-Token"); token != "" {
			if !verifyGiteeToken(secret, token, r.Header.Get("X-Gitee-Timestamp")) {
				return errors.New("invalid gitee token")
			}
			return nil
		}
		var payload giteeEvent
		if err := json.Unmarshal(body, &payload); err != nil {
			return fmt.Errorf("parse gitee password: %w", err)
		}
		if !constantTimeEqual(payload.Password, secret) {
			return errors.New("invalid gitee password")
		}
		return nil
	default:
		return fmt.Errorf("unknown provider %q", provider)
	}
}

// verifyGiteeToken 支持 Gitee 的两种模式：
//  1. 密码模式：X-Gitee-Token 直接等于 Webhook 密码；
//  2. 签名模式：X-Gitee-Token = urlEncode(base64(HMAC-SHA256(timestamp+"\n"+secret))).
func verifyGiteeToken(secret, token, timestamp string) bool {
	if constantTimeEqual(token, secret) {
		return true
	}
	if timestamp == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + secret))
	encoded := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if constantTimeEqual(token, encoded) || constantTimeEqual(token, url.QueryEscape(encoded)) {
		return true
	}
	if decoded, err := url.QueryUnescape(token); err == nil {
		return constantTimeEqual(decoded, encoded)
	}
	return false
}

func hmacSHA256(secret string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return mac.Sum(nil)
}

func verifyHexSignature(header, prefix string, want []byte) error {
	sig := strings.TrimSpace(header)
	if prefix != "" && strings.HasPrefix(strings.ToLower(sig), prefix) {
		sig = sig[len(prefix):]
	}
	// 兼容部分平台附加的 "sha256=...,timestamp=..." 形式。
	if i := strings.IndexByte(sig, ','); i >= 0 {
		sig = sig[:i]
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}
	if !hmac.Equal(got, want) {
		return errors.New("invalid signature")
	}
	return nil
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// parseEvent 将平台原始负载转换为统一 Event。
func parseEvent(provider string, r *http.Request, body []byte) (Event, error) {
	switch provider {
	case config.ProviderGitLab:
		return parseGitLab(body)
	case config.ProviderGitHub:
		return parseGitHub(r, body)
	case config.ProviderGitee:
		return parseGitee(body)
	default:
		return Event{}, fmt.Errorf("unknown provider %q", provider)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\r'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func deriveNamespace(fullName string) string {
	fullName = strings.Trim(fullName, "/")
	if i := strings.LastIndexByte(fullName, '/'); i >= 0 {
		return fullName[:i]
	}
	return ""
}
