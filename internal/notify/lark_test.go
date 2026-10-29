package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code-builder/internal/config"
)

func newTestNotifier(t *testing.T, url string, mutate func(*config.NotifyConfig)) *Notifier {
	t.Helper()
	cfg := config.NotifyConfig{
		Enabled: true,
		Lark:    config.LarkConfig{WebhookURL: url, Timeout: "2s"},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func Test_send_when_OK_thenInteractiveCard(t *testing.T) {
	// 准备
	var got larkPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		_, _ = io.WriteString(w, `{"code":0,"msg":"success"}`)
	}))
	defer srv.Close()
	n := newTestNotifier(t, srv.URL, nil)

	// 执行
	err := n.send(context.Background(), sampleMessage(EventSuccess))

	// 断言
	if err != nil {
		t.Fatalf("send() error = %v", err)
	}
	if got.MsgType != "interactive" {
		t.Errorf("msg_type = %q, want interactive", got.MsgType)
	}
	if got.Timestamp == "" {
		t.Error("timestamp is empty")
	}
	if got.Sign != "" {
		t.Errorf("sign = %q, want empty without secret", got.Sign)
	}
	card, _ := json.Marshal(got.Card)
	if !strings.Contains(string(card), "atlas-admin-front") {
		t.Errorf("card missing app name: %s", card)
	}
}

func Test_send_when_Secret_thenSignMatchesJenkinsFormula(t *testing.T) {
	// 准备
	var got larkPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = io.WriteString(w, `{"code":0}`)
	}))
	defer srv.Close()
	n := newTestNotifier(t, srv.URL, func(c *config.NotifyConfig) { c.Lark.Secret = "s3cret" })

	// 执行
	if err := n.send(context.Background(), sampleMessage(EventSuccess)); err != nil {
		t.Fatalf("send() error = %v", err)
	}

	// 断言
	ts, err := strconv.ParseInt(got.Timestamp, 10, 64)
	if err != nil {
		t.Fatalf("timestamp %q is not unix seconds: %v", got.Timestamp, err)
	}
	if want := larkSign(ts, "s3cret"); got.Sign != want {
		t.Errorf("sign = %q, want %q", got.Sign, want)
	}
}

func Test_send_when_5xx_thenRetriesAndSucceeds(t *testing.T) {
	// 准备
	old := retryBase
	retryBase = time.Millisecond
	t.Cleanup(func() { retryBase = old })

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "boom")
			return
		}
		_, _ = io.WriteString(w, `{"code":0}`)
	}))
	defer srv.Close()
	n := newTestNotifier(t, srv.URL, nil)

	// 执行
	err := n.send(context.Background(), sampleMessage(EventSuccess))

	// 断言
	if err != nil {
		t.Fatalf("send() error = %v, want success after retry", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

func Test_send_when_400_thenNoRetry(t *testing.T) {
	// 准备
	old := retryBase
	retryBase = time.Millisecond
	t.Cleanup(func() { retryBase = old })

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":9499,"msg":"bad request"}`)
	}))
	defer srv.Close()
	n := newTestNotifier(t, srv.URL, nil)

	// 执行
	err := n.send(context.Background(), sampleMessage(EventFailure))

	// 断言
	if err == nil {
		t.Fatal("send() error = nil, want failure")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (4xx must not retry)", got)
	}
}

func Test_send_when_LarkRejects_thenError(t *testing.T) {
	// 准备：HTTP 200，但 Lark 返回非零 code。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":19021,"msg":"sign match fail"}`)
	}))
	defer srv.Close()
	n := newTestNotifier(t, srv.URL, nil)

	// 执行
	err := n.send(context.Background(), sampleMessage(EventSuccess))

	// 断言
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("error = %v, want lark rejection", err)
	}
}

func Test_maskWebhook_when_URL_thenTokenHidden(t *testing.T) {
	// 准备
	raw := "https://open.feishu.cn/open-apis/bot/v2/hook/abc123?sign=1"

	// 执行
	got := maskWebhook(raw)

	// 断言
	if strings.Contains(got, "abc123") {
		t.Errorf("masked URL leaks token: %s", got)
	}
	if !strings.HasSuffix(got, "/****") {
		t.Errorf("masked URL = %q, want suffix /****", got)
	}
}

func Test_Notifier_Wants(t *testing.T) {
	// 准备
	on, off := true, false
	tests := []struct {
		name string
		cfg  config.NotifyConfig
		ev   Event
		want bool
	}{
		{"disabled", config.NotifyConfig{}, EventSuccess, false},
		{"no webhook", config.NotifyConfig{Enabled: true}, EventSuccess, false},
		{"default success", config.NotifyConfig{Enabled: true, Lark: config.LarkConfig{WebhookURL: "https://x"}}, EventSuccess, true},
		{"default started", config.NotifyConfig{Enabled: true, Lark: config.LarkConfig{WebhookURL: "https://x"}}, EventStarted, true},
		{"success off", config.NotifyConfig{Enabled: true, Lark: config.LarkConfig{WebhookURL: "https://x"}, OnSuccess: &off}, EventSuccess, false},
		{"success on", config.NotifyConfig{Enabled: true, Lark: config.LarkConfig{WebhookURL: "https://x"}, OnSuccess: &on}, EventSuccess, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := New(tt.cfg, nil)
			if got := n.Wants(tt.ev); got != tt.want {
				t.Errorf("Wants(%s) = %v, want %v", tt.ev, got, tt.want)
			}
		})
	}
}

func Test_Async_when_Disabled_thenNoRequest(t *testing.T) {
	// 准备
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.WriteString(w, `{"code":0}`)
	}))
	defer srv.Close()
	n := New(config.NotifyConfig{}, nil)

	// 执行
	n.Async(sampleMessage(EventSuccess), nil)
	time.Sleep(50 * time.Millisecond)

	// 断言
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("calls = %d, want 0 while disabled", got)
	}
}

func Test_Async_when_DeliveryFails_thenCallbackRuns(t *testing.T) {
	// 准备
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	n := newTestNotifier(t, srv.URL, nil)
	done := make(chan error, 1)

	// 执行
	n.Async(sampleMessage(EventSuccess), func(err error) { done <- err })

	// 断言
	select {
	case err := <-done:
		if err == nil {
			t.Error("callback error = nil, want delivery failure")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("callback never ran")
	}
}
