package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab-builder/internal/config"
	"gitlab-builder/internal/queue"
	"gitlab-builder/internal/store"

	"github.com/gin-gonic/gin"
)

const testSecret = "test-secret"

func testRepo() config.Repository {
	return config.Repository{
		Name:     "app",
		URL:      "ssh://git@example.com:20022/group/app.git",
		Branches: []string{"main"},
		Build:    config.BuildSpec{Image: "local/app"},
	}
}

func newTestDeps(t *testing.T, q *queue.Queue) Deps {
	t.Helper()
	return Deps{
		Host: "example.com",
		Build: config.BuildConfig{
			Branches:    []string{"main"},
			BranchRegex: ".*",
			Dockerfile:  "auto",
			Context:     ".",
		},
		Secret:    testSecret,
		Store:     store.New(t.TempDir()),
		Queue:     q,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Workspace: t.TempDir(),
	}
}

func newRouter(deps Deps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/webhook", Handler(deps))
	return r
}

func pushBody(t *testing.T, ev map[string]any) io.Reader {
	t.Helper()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

func basePushEvent() map[string]any {
	return map[string]any{
		"object_kind":  "push",
		"event_name":   "push",
		"ref":          "refs/heads/main",
		"before":       "1111111111111111111111111111111111111111",
		"after":        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"checkout_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"project": map[string]any{
			"name":                "app",
			"path_with_namespace": "group/app",
			"git_http_url":        "https://example.com/group/app.git",
			"git_ssh_url":         "ssh://git@example.com:20022/group/app.git",
		},
	}
}

func doWebhook(r *gin.Engine, token string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook", body)
	if token != "" {
		req.Header.Set("X-Gitlab-Token", token)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func Test_Webhook_when_WrongSecret_thenReturn401(t *testing.T) {
	// Given
	r := newRouter(newTestDeps(t, queue.New(1, 10)))

	// When
	w := doWebhook(r, "wrong", pushBody(t, basePushEvent()))

	// Then
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func Test_Webhook_when_MissingSecret_thenReturn401(t *testing.T) {
	// Given
	r := newRouter(newTestDeps(t, queue.New(1, 10)))

	// When
	w := doWebhook(r, "", pushBody(t, basePushEvent()))

	// Then
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func Test_Webhook_when_InvalidJSON_thenReturn400(t *testing.T) {
	// Given
	r := newRouter(newTestDeps(t, queue.New(1, 10)))

	// When
	w := doWebhook(r, testSecret, strings.NewReader("{not json"))

	// Then
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func Test_Webhook_when_TagPush_thenReturn202Ignored(t *testing.T) {
	// Given
	r := newRouter(newTestDeps(t, queue.New(1, 10)))
	ev := basePushEvent()
	ev["object_kind"] = "tag_push"
	ev["ref"] = "refs/tags/v1.0"

	// When
	w := doWebhook(r, testSecret, pushBody(t, ev))

	// Then
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ignored") {
		t.Errorf("body = %s, want ignored", w.Body.String())
	}
}

func Test_Webhook_when_DeletePush_thenReturn202Ignored(t *testing.T) {
	// Given
	r := newRouter(newTestDeps(t, queue.New(1, 10)))
	ev := basePushEvent()
	ev["after"] = "0000000000000000000000000000000000000000"
	ev["checkout_sha"] = nil

	// When
	w := doWebhook(r, testSecret, pushBody(t, ev))

	// Then
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ignored") {
		t.Errorf("body = %s, want ignored", w.Body.String())
	}
}

func Test_Webhook_when_WrongHost_thenReturn202HostNotAllowed(t *testing.T) {
	// Given
	r := newRouter(newTestDeps(t, queue.New(1, 10)))
	ev := basePushEvent()
	proj := ev["project"].(map[string]any)
	proj["git_ssh_url"] = "ssh://git@evil.example.org:20022/other/repo.git"
	proj["git_http_url"] = "https://evil.example.org/other/repo.git"

	// When
	w := doWebhook(r, testSecret, pushBody(t, ev))

	// Then
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if !strings.Contains(w.Body.String(), "host_not_allowed") {
		t.Errorf("body = %s, want host_not_allowed", w.Body.String())
	}
}

func Test_Webhook_when_BranchFiltered_thenReturn202BranchFiltered(t *testing.T) {
	// Given
	r := newRouter(newTestDeps(t, queue.New(1, 10)))
	ev := basePushEvent()
	ev["ref"] = "refs/heads/feature/x"

	// When
	w := doWebhook(r, testSecret, pushBody(t, ev))

	// Then
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if !strings.Contains(w.Body.String(), "branch_filtered") {
		t.Errorf("body = %s, want branch_filtered", w.Body.String())
	}
}

func Test_Webhook_when_ValidPush_thenReturn202QueuedWithBuild(t *testing.T) {
	// Given
	deps := newTestDeps(t, queue.New(1, 10))
	r := newRouter(deps)

	// When
	w := doWebhook(r, testSecret, pushBody(t, basePushEvent()))

	// Then
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", w.Code, w.Body.String())
	}
	var resp struct {
		Status  string `json:"status"`
		BuildID string `json:"build_id"`
		SHA     string `json:"sha"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "queued" || resp.BuildID == "" {
		t.Errorf("resp = %+v, want queued with build_id", resp)
	}
	if resp.SHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("sha = %q, want checkout_sha", resp.SHA)
	}
	b, ok := deps.Store.Get(resp.BuildID)
	if !ok {
		t.Fatal("build not in store")
	}
	if b.Status != store.StatusQueued {
		t.Errorf("status = %q, want queued", b.Status)
	}
	if b.ShortSHA != "aaaaaaaaaaaa" {
		t.Errorf("ShortSHA = %q, want 12 chars", b.ShortSHA)
	}
	if deps.Queue.Len() != 1 {
		t.Errorf("queue len = %d, want 1", deps.Queue.Len())
	}
}

func Test_Webhook_when_QueueFull_thenReturn503(t *testing.T) {
	// Given
	q := queue.New(1, 1)
	if err := q.Enqueue(queue.Job{Repo: testRepo(), Run: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	r := newRouter(newTestDeps(t, q))

	// When
	w := doWebhook(r, testSecret, pushBody(t, basePushEvent()))

	// Then
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "queue_full") {
		t.Errorf("body = %s, want queue_full", w.Body.String())
	}
}
