package notify

import (
	"fmt"
	"strings"
	"time"
)

const (
	// maxErrRunes caps the error text embedded in one card.
	maxErrRunes = 2000
	// na is the placeholder for missing values, matching the Jenkins template.
	na = "N/A"
)

// buildCard renders the Lark interactive card. Its layout mirrors the Jenkins
// shared library's LarkNotifier: header, two 2-column field rows, a summary
// block, an optional error panel and the push-time note.
func buildCard(msg Message) map[string]any {
	elements := []any{
		fieldRow(
			"**项目名称**\n"+orNA(msg.App),
			"**构建发起**\n"+orNA(msg.Actor),
		),
		fieldRow(
			"**当前版本**\n"+version(msg),
			"**持续时间**\n"+formatDuration(msg.Duration, msg.Event == EventStarted),
		),
		map[string]any{"tag": "hr"},
	}
	if s := strings.TrimSpace(msg.Subject); s != "" {
		elements = append(elements, map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "lark_md", "content": "**💬 提交摘要**\n" + s},
		})
	}
	if e := summarize(msg.Err); e != "" {
		elements = append(elements, errorPanel(e))
	}
	if e := summarize(msg.DeployErr); e != "" {
		elements = append(elements, map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "lark_md", "content": "**⚠️ 部署失败**\n```\n" + e + "\n```"},
		})
	}
	elements = append(elements, map[string]any{
		"tag": "note",
		"elements": []any{
			map[string]any{
				"tag":     "plain_text",
				"content": "推送时间: " + msg.Now.In(shanghai()).Format("2006-01-02 15:04:05"),
			},
		},
	})
	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": eventTitle(msg.Event) + " · " + orNA(msg.App)},
			"template": eventColor(msg.Event),
		},
		"elements": elements,
	}
}

// fieldRow builds one div with two short lark_md fields.
func fieldRow(left, right string) map[string]any {
	return map[string]any{
		"tag": "div",
		"fields": []any{
			map[string]any{"is_short": true, "text": map[string]any{"tag": "lark_md", "content": left}},
			map[string]any{"is_short": true, "text": map[string]any{"tag": "lark_md", "content": right}},
		},
	}
}

// errorPanel wraps the failure summary in a collapsed red panel, like Jenkins.
func errorPanel(summary string) map[string]any {
	return map[string]any{
		"tag": "collapsible_panel",
		"header": map[string]any{
			"template": "red",
			// Lark requires plain_text for panel headers (lark_md is rejected).
			"title": map[string]any{"tag": "plain_text", "content": "▶ 点击展开错误摘要"},
		},
		"elements": []any{
			map[string]any{"tag": "markdown", "content": "```\n" + summary + "\n```"},
		},
	}
}

func eventTitle(ev Event) string {
	switch ev {
	case EventStarted:
		return "🚀 流水线开始"
	case EventSuccess:
		return "✅ 流水线成功"
	case EventFailure:
		return "❌ 流水线失败"
	default:
		return "🔔 通知"
	}
}

func eventColor(ev Event) string {
	switch ev {
	case EventSuccess:
		return "green"
	case EventFailure:
		return "red"
	default:
		return "blue"
	}
}

// version renders "<branch>-<shortSHA>" exactly like the Jenkins template.
func version(msg Message) string {
	branch := strings.TrimSpace(msg.Branch)
	short := strings.TrimSpace(msg.ShortSHA)
	switch {
	case branch != "" && short != "":
		return branch + "-" + short
	case branch != "":
		return branch
	case short != "":
		return short
	default:
		return na
	}
}

// formatDuration renders a Jenkins-style duration ("3 min 46 sec"). Running
// builds (the start event) append "and counting" like Jenkins does.
func formatDuration(d time.Duration, counting bool) string {
	if d < 0 {
		d = 0
	}
	secs := int(d / time.Second)
	var s string
	switch {
	case secs >= 3600:
		s = fmt.Sprintf("%d hr %d min", secs/3600, (secs%3600)/60)
	case secs >= 60:
		s = fmt.Sprintf("%d min %d sec", secs/60, secs%60)
	default:
		s = fmt.Sprintf("%d sec", secs)
	}
	if counting {
		s += " and counting"
	}
	return s
}

// summarize trims the error and caps it at maxErrRunes to keep cards small.
func summarize(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= maxErrRunes {
		return s
	}
	return string(r[:maxErrRunes-100]) + "\n...（已截断）"
}

func orNA(s string) string {
	if strings.TrimSpace(s) == "" {
		return na
	}
	return s
}

// shanghai pins UTC+8 without loading tzdata (the runtime image is minimal).
func shanghai() *time.Location {
	return time.FixedZone("CST", 8*60*60)
}
