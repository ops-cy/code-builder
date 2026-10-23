package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleMessage(ev Event) Message {
	return Message{
		Event:    ev,
		App:      "atlas-admin-front",
		Actor:    "Hunter",
		Branch:   "develop",
		ShortSHA: "d8f8dddf",
		Duration: 3*time.Minute + 46*time.Second,
		Subject:  "feat: 关闭页面权限校验，没有权限的页面也可直接输入路由进入（CHECK_PAGE_PERMISSION）",
		Now:      time.Date(2026, 10, 23, 13, 28, 2, 0, shanghai()),
	}
}

func cardJSON(t *testing.T, msg Message) string {
	t.Helper()
	raw, err := json.Marshal(buildCard(msg))
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	return string(raw)
}

func header(t *testing.T, msg Message) (title, template string) {
	t.Helper()
	card := buildCard(msg)
	h, ok := card["header"].(map[string]any)
	if !ok {
		t.Fatal("card has no header")
	}
	ti, _ := h["title"].(map[string]any)
	title, _ = ti["content"].(string)
	template, _ = h["template"].(string)
	return title, template
}

func Test_buildCard_when_Success_thenJenkinsLayout(t *testing.T) {
	// Given
	msg := sampleMessage(EventSuccess)

	// Then
	title, template := header(t, msg)
	if title != "✅ 流水线成功 · atlas-admin-front" {
		t.Errorf("title = %q", title)
	}
	if template != "green" {
		t.Errorf("template = %q, want green", template)
	}
	raw := cardJSON(t, msg)
	for _, want := range []string{
		`**项目名称**\natlas-admin-front`,
		`**构建发起**\nHunter`,
		`**当前版本**\ndevelop-d8f8dddf`,
		`**持续时间**\n3 min 46 sec`,
		`**💬 提交摘要**\nfeat: 关闭页面权限校验`,
		`推送时间: 2026-10-23 13:28:02`,
		`"wide_screen_mode":true`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("card missing %q\n%s", want, raw)
		}
	}
	if strings.Contains(raw, `"button"`) || strings.Contains(raw, `"action"`) {
		t.Error("card must not contain an action button")
	}
	if strings.Contains(raw, "部署失败") {
		t.Error("success card without deploy error must not mention deploy")
	}
}

func Test_buildCard_when_Failure_thenRedPanelWithSummary(t *testing.T) {
	// Given
	msg := sampleMessage(EventFailure)
	msg.Err = "no Dockerfile at repository root and no built-in template matched"

	// Then
	title, template := header(t, msg)
	if title != "❌ 流水线失败 · atlas-admin-front" {
		t.Errorf("title = %q", title)
	}
	if template != "red" {
		t.Errorf("template = %q, want red", template)
	}
	raw := cardJSON(t, msg)
	if !strings.Contains(raw, `"collapsible_panel"`) {
		t.Errorf("failure card must collapse the error: %s", raw)
	}
	if !strings.Contains(raw, "▶ 点击展开错误摘要") {
		t.Errorf("failure card missing panel title: %s", raw)
	}
	if !strings.Contains(raw, "no Dockerfile at repository root") {
		t.Errorf("failure card missing error summary: %s", raw)
	}
}

func Test_buildCard_when_DeployFailed_thenSuccessCardShowsWarning(t *testing.T) {
	// Given: the build succeeded but the compose deploy failed.
	msg := sampleMessage(EventSuccess)
	msg.DeployErr = "docker compose up -d failed: exit status 1"

	// Then
	raw := cardJSON(t, msg)
	if !strings.Contains(raw, "⚠️ 部署失败") {
		t.Errorf("card missing deploy warning: %s", raw)
	}
	if !strings.Contains(raw, "docker compose up -d failed") {
		t.Errorf("card missing deploy error: %s", raw)
	}
	if title, template := header(t, msg); title != "✅ 流水线成功 · atlas-admin-front" || template != "green" {
		t.Errorf("deploy failure must keep the success header, got %q/%q", title, template)
	}
}

func Test_buildCard_when_Started_thenBlueAndCounting(t *testing.T) {
	// Given
	msg := sampleMessage(EventStarted)
	msg.Duration = 0

	// Then
	title, template := header(t, msg)
	if title != "🚀 流水线开始 · atlas-admin-front" {
		t.Errorf("title = %q", title)
	}
	if template != "blue" {
		t.Errorf("template = %q, want blue", template)
	}
	if raw := cardJSON(t, msg); !strings.Contains(raw, "0 sec and counting") {
		t.Errorf("started card must show a counting duration: %s", raw)
	}
}

func Test_buildCard_when_EmptyFields_thenPlaceholders(t *testing.T) {
	// Given
	msg := Message{Event: EventSuccess, Now: time.Now()}

	// Then
	raw := cardJSON(t, msg)
	if strings.Count(raw, "N/A") < 3 {
		t.Errorf("missing N/A placeholders: %s", raw)
	}
}

func Test_formatDuration(t *testing.T) {
	tests := []struct {
		name     string
		d        time.Duration
		counting bool
		want     string
	}{
		{"zero", 0, false, "0 sec"},
		{"seconds", 46 * time.Second, false, "46 sec"},
		{"minutes", 3*time.Minute + 46*time.Second, false, "3 min 46 sec"},
		{"hours", 2*time.Hour + 5*time.Minute + 9*time.Second, false, "2 hr 5 min"},
		{"counting", 3*time.Minute + 46*time.Second, true, "3 min 46 sec and counting"},
		{"negative", -time.Second, false, "0 sec"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDuration(tt.d, tt.counting); got != tt.want {
				t.Errorf("formatDuration(%v, %v) = %q, want %q", tt.d, tt.counting, got, tt.want)
			}
		})
	}
}

func Test_summarize_when_TooLong_thenTruncated(t *testing.T) {
	// Given
	long := strings.Repeat("错", maxErrRunes+500)

	// When
	got := summarize(long)

	// Then
	if !strings.HasSuffix(got, "...（已截断）") {
		t.Errorf("summary not truncated: %q", got[len(got)-20:])
	}
	if n := len([]rune(got)); n > maxErrRunes {
		t.Errorf("summary runes = %d, want <= %d", n, maxErrRunes)
	}
}

func Test_version_when_PartialData_thenFallbacks(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want string
	}{
		{"both", Message{Branch: "main", ShortSHA: "abcdef12"}, "main-abcdef12"},
		{"branch only", Message{Branch: "main"}, "main"},
		{"sha only", Message{ShortSHA: "abcdef12"}, "abcdef12"},
		{"none", Message{}, "N/A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := version(tt.msg); got != tt.want {
				t.Errorf("version() = %q, want %q", got, tt.want)
			}
		})
	}
}
