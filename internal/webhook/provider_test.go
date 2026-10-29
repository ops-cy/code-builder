package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"code-builder/internal/config"
	"code-builder/internal/queue"

	"github.com/gin-gonic/gin"
)

func githubPushBody() map[string]any {
	return map[string]any{
		"ref":     "refs/heads/main",
		"before":  "1111111111111111111111111111111111111111",
		"after":   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"deleted": false,
		"pusher":  map[string]any{"name": "octocat"},
		"repository": map[string]any{
			"name":      "app",
			"full_name": "acme/app",
			"ssh_url":   "git@github.com:acme/app.git",
			"clone_url": "https://github.com/acme/app.git",
			"html_url":  "https://github.com/acme/app",
			"owner":     map[string]any{"login": "acme"},
		},
		"head_commit": map[string]any{
			"id":      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"message": "feat: github build\n\nbody",
			"author":  map[string]any{"name": "Octo Cat", "username": "octocat"},
		},
	}
}

func giteePushBody() map[string]any {
	return map[string]any{
		"ref":       "refs/heads/master",
		"before":    "1111111111111111111111111111111111111111",
		"after":     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"hook_name": "push_hooks",
		"password":  testSecret,
		"pusher":    map[string]any{"name": "gitee-user"},
		"repository": map[string]any{
			"name":      "svc",
			"path":      "acme/svc",
			"full_name": "acme/svc",
			"ssh_url":   "git@gitee.com:acme/svc.git",
			"url":       "https://gitee.com/acme/svc",
			"owner":     map[string]any{"login": "acme"},
		},
		"head_commit": map[string]any{
			"id":      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"message": "feat: gitee build\n\nbody",
			"author":  map[string]any{"name": "Gitee User"},
		},
	}
}

func githubSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func doWebhookWithHeaders(r *gin.Engine, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func multiProviderDeps(t *testing.T) Deps {
	t.Helper()
	deps := newTestDeps(t, queue.New(1, 10))
	deps.Branches = []string{"main", "master"}
	deps.Providers = map[string]config.Provider{
		config.ProviderGitLab: {Name: config.ProviderGitLab, Domains: []string{"example.com"}},
		config.ProviderGitHub: {Name: config.ProviderGitHub, Domains: []string{"github.com"}},
		config.ProviderGitee:  {Name: config.ProviderGitee, Domains: []string{"gitee.com"}},
	}
	return deps
}

func Test_GitHub_when_ValidSignature_thenQueued(t *testing.T) {
	deps := multiProviderDeps(t)
	r := newRouter(deps)
	body, _ := json.Marshal(githubPushBody())

	w := doWebhookWithHeaders(r, "/webhook/github", body, map[string]string{
		"X-GitHub-Event":      "push",
		"X-GitHub-Delivery":   "delivery-1",
		"X-Hub-Signature-256": githubSignature(testSecret, body),
	})

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", w.Code, w.Body.String())
	}
	var resp struct {
		Status   string `json:"status"`
		Provider string `json:"provider"`
		BuildID  string `json:"build_id"`
		Image    string `json:"image"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "queued" || resp.Provider != "github" || resp.BuildID == "" {
		t.Errorf("resp = %+v", resp)
	}
	b, ok := deps.Store.Get(resp.BuildID)
	if !ok {
		t.Fatal("build not stored")
	}
	if b.Provider != config.ProviderGitHub || b.FullName != "acme/app" {
		t.Errorf("build = %+v", b)
	}
	if b.Actor != "octocat" || b.CommitSubject != "feat: github build" {
		t.Errorf("actor/subject = %q/%q", b.Actor, b.CommitSubject)
	}
}

func Test_GitHub_when_BadSignature_then401(t *testing.T) {
	r := newRouter(multiProviderDeps(t))
	body, _ := json.Marshal(githubPushBody())
	w := doWebhookWithHeaders(r, "/webhook/github", body, map[string]string{
		"X-GitHub-Event":      "push",
		"X-Hub-Signature-256": "sha256=deadbeef",
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func Test_GitHub_when_Ping_thenPong(t *testing.T) {
	r := newRouter(multiProviderDeps(t))
	body := []byte(`{"zen":"Keep it logically awesome.","hook_id":1}`)
	w := doWebhookWithHeaders(r, "/webhook/github", body, map[string]string{
		"X-GitHub-Event":      "ping",
		"X-Hub-Signature-256": githubSignature(testSecret, body),
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "pong") {
		t.Errorf("status = %d body=%s, want pong", w.Code, w.Body.String())
	}
}

func Test_GitHub_when_DeletedBranch_thenIgnored(t *testing.T) {
	r := newRouter(multiProviderDeps(t))
	ev := githubPushBody()
	ev["deleted"] = true
	ev["after"] = "0000000000000000000000000000000000000000"
	body, _ := json.Marshal(ev)
	w := doWebhookWithHeaders(r, "/webhook/github", body, map[string]string{
		"X-GitHub-Event":      "push",
		"X-Hub-Signature-256": githubSignature(testSecret, body),
	})
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), "ignored") {
		t.Errorf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func Test_Gitee_when_ValidToken_thenQueued(t *testing.T) {
	deps := multiProviderDeps(t)
	r := newRouter(deps)
	body, _ := json.Marshal(giteePushBody())
	w := doWebhookWithHeaders(r, "/webhook/gitee", body, map[string]string{
		"X-Gitee-Event": "Push Hook",
		"X-Gitee-Token": testSecret,
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Status  string `json:"status"`
		BuildID string `json:"build_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "queued" {
		t.Errorf("resp = %+v", resp)
	}
	b, _ := deps.Store.Get(resp.BuildID)
	if b.Provider != config.ProviderGitee || b.FullName != "acme/svc" {
		t.Errorf("build = %+v", b)
	}
	if b.Actor != "gitee-user" || b.CommitSubject != "feat: gitee build" {
		t.Errorf("actor/subject = %q/%q", b.Actor, b.CommitSubject)
	}
}

func Test_Gitee_when_BodyPassword_thenQueued(t *testing.T) {
	r := newRouter(multiProviderDeps(t))
	body, _ := json.Marshal(giteePushBody())
	w := doWebhookWithHeaders(r, "/webhook/gitee", body, map[string]string{
		"X-Gitee-Event": "Push Hook",
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func Test_Gitee_when_Signature_thenQueued(t *testing.T) {
	r := newRouter(multiProviderDeps(t))
	body, _ := json.Marshal(giteePushBody())
	timestamp := "1576754827988"
	mac := hmac.New(sha256.New, []byte(testSecret))
	_, _ = mac.Write([]byte(timestamp + "\n" + testSecret))
	token := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	w := doWebhookWithHeaders(r, "/webhook/gitee", body, map[string]string{
		"X-Gitee-Event":     "Push Hook",
		"X-Gitee-Timestamp": timestamp,
		"X-Gitee-Token":     token,
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func Test_Gitee_when_WrongPassword_then401(t *testing.T) {
	r := newRouter(multiProviderDeps(t))
	ev := giteePushBody()
	ev["password"] = "wrong"
	body, _ := json.Marshal(ev)
	w := doWebhookWithHeaders(r, "/webhook/gitee", body, map[string]string{
		"X-Gitee-Event": "Push Hook",
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func Test_Webhook_when_AutoDetectsGitHub_thenQueued(t *testing.T) {
	r := newRouter(multiProviderDeps(t))
	body, _ := json.Marshal(githubPushBody())
	w := doWebhookWithHeaders(r, "/webhook", body, map[string]string{
		"X-GitHub-Event":      "push",
		"X-Hub-Signature-256": githubSignature(testSecret, body),
	})
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"provider":"github"`) {
		t.Errorf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func Test_Webhook_when_ProviderDisabled_then404(t *testing.T) {
	deps := newTestDeps(t, queue.New(1, 10))
	r := newRouter(deps)
	body, _ := json.Marshal(githubPushBody())
	w := doWebhookWithHeaders(r, "/webhook/github", body, map[string]string{
		"X-GitHub-Event":      "push",
		"X-Hub-Signature-256": githubSignature(testSecret, body),
	})
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func Test_parseGitHub_when_Push_thenNormalised(t *testing.T) {
	body, _ := json.Marshal(githubPushBody())
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "d-1")
	ev, err := parseEvent(config.ProviderGitHub, req, body)
	if err != nil {
		t.Fatal(err)
	}
	branch, _ := ev.Branch()
	if ev.Kind != "push" || branch != "main" || ev.SHA() != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("event = %+v", ev)
	}
	if ev.Namespace != "acme" || ev.DeliveryID != "d-1" {
		t.Errorf("event = %+v", ev)
	}
}

func Test_parseGitee_when_Push_thenNormalised(t *testing.T) {
	body, _ := json.Marshal(giteePushBody())
	req := httptest.NewRequest(http.MethodPost, "/webhook/gitee", bytes.NewReader(body))
	ev, err := parseEvent(config.ProviderGitee, req, body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != "push" || ev.SHA() != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Errorf("event = %+v", ev)
	}
	branch, _ := ev.Branch()
	if branch != "master" {
		t.Errorf("branch = %q", branch)
	}
}

func Test_verifyWebhook_when_MissingSecret_thenError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(strings.NewReader("{}")))
	if err := verifyWebhook(config.ProviderGitLab, "", req, []byte("{}")); err == nil {
		t.Fatal("verifyWebhook() error = nil, want secret required")
	}
}
