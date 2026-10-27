package notify

import (
	"fmt"
	"strings"
	"time"
)

const (
	// maxErrRunes 限制单个卡片中嵌入的错误文本长度。
	maxErrRunes = 2000
	// maxOutputRunes 限制单个卡片中嵌入的部署输出长度。
	maxOutputRunes = 1200
	// na 是缺失值的占位符，与 Jenkins 模板保持一致。
	na = "N/A"
)

// 最终卡片中展示的部署状态。
const (
	DeployDeployed = "deployed"
	DeploySkipped  = "skipped"
	DeployFailed   = "failed"
)

// buildCard 渲染 Lark 交互式卡片。其布局与 Jenkins 共享库中的
// LarkNotifier 保持一致：标题、两列字段行、摘要区块、可选的失败面板
// 以及推送时间提示。成功卡片还会上报部署结果（deployed / skipped / failed）。
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
	}
	thirdLeft := "**镜像**\n" + orNA(msg.Image)
	thirdRight := "**Commit**\n" + orNA(msg.ShortSHA)
	if msg.Event == EventStarted {
		thirdRight = "**开始时间**\n" + formatTime(msg.StartedAt)
	}
	elements = append(elements, fieldRow(thirdLeft, thirdRight), map[string]any{"tag": "hr"})

	if s := strings.TrimSpace(msg.Subject); s != "" {
		elements = append(elements, map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "lark_md", "content": "**💬 提交摘要**\n" + s},
		})
	}
	if msg.Event == EventFailure {
		if panel := failurePanel(msg.Err, msg.LogTail); panel != nil {
			elements = append(elements, panel)
		}
	}
	if msg.Event == EventSuccess {
		if el := deployElement(msg); el != nil {
			elements = append(elements, el)
		}
	}
	elements = append(elements, map[string]any{
		"tag": "note",
		"elements": []any{
			map[string]any{
				"tag":     "plain_text",
				"content": "推送时间: " + formatTime(msg.Now),
			},
		},
	})

	title, template := cardHeader(msg)
	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": title},
			"template": template,
		},
		"elements": elements,
	}
}

// cardHeader 返回卡片标题和颜色。即使构建本身成功，部署失败也会被
// 展示为“构建成功，部署失败”。
func cardHeader(msg Message) (title, template string) {
	app := orNA(msg.App)
	switch msg.Event {
	case EventStarted:
		return "🚀 流水线开始 · " + app, "blue"
	case EventFailure:
		return "❌ 流水线失败 · " + app, "red"
	case EventSuccess:
		if msg.DeployStatus == DeployFailed {
			return "⚠️ 构建成功，部署失败 · " + app, "orange"
		}
		return "✅ 流水线成功 · " + app, "green"
	default:
		return "🔔 通知 · " + app, "grey"
	}
}

// deployElement 在成功卡片上渲染部署结果。
func deployElement(msg Message) map[string]any {
	switch msg.DeployStatus {
	case DeployFailed:
		content := "**⚠️ 部署失败**\n```\n" + summarizeN(msg.DeployError, maxErrRunes) + "\n```"
		if out := strings.TrimSpace(msg.DeployOutput); out != "" {
			content += "\n**docker compose ps**\n```\n" + summarizeN(out, maxOutputRunes) + "\n```"
		}
		return collapsiblePanel("▶ 点击展开部署失败详情", "orange", content)
	case DeployDeployed:
		content := "**🚢 部署**\n目标: " + orNA(msg.DeployTarget)
		if out := strings.TrimSpace(msg.DeployOutput); out != "" {
			content += "\n```\n" + summarizeN(out, maxOutputRunes) + "\n```"
		}
		return markdownDiv(content)
	case DeploySkipped:
		return markdownDiv("**🚢 部署**\n未部署：" + orNA(msg.DeployReason))
	default:
		return nil
	}
}

// failurePanel 折叠展示错误摘要和日志末尾，与 Jenkins 行为一致。
func failurePanel(errMsg, logTail string) map[string]any {
	var parts []string
	if e := summarize(errMsg); e != "" {
		parts = append(parts, e)
	}
	if t := strings.TrimSpace(logTail); t != "" {
		parts = append(parts, "**日志末尾（最后 30 行）**\n```\n"+summarizeN(t, maxErrRunes)+"\n```")
	}
	if len(parts) == 0 {
		return nil
	}
	return collapsiblePanel("▶ 点击展开错误摘要", "red", strings.Join(parts, "\n\n"))
}

// collapsiblePanel 构建一个带 markdown 正文的折叠面板。
func collapsiblePanel(title, template, content string) map[string]any {
	return map[string]any{
		"tag": "collapsible_panel",
		"header": map[string]any{
			"template": template,
			// Lark 要求面板标题使用 plain_text（lark_md 会被拒绝）。
			"title": map[string]any{"tag": "plain_text", "content": title},
		},
		"elements": []any{
			map[string]any{"tag": "markdown", "content": content},
		},
	}
}

func markdownDiv(content string) map[string]any {
	return map[string]any{
		"tag":  "div",
		"text": map[string]any{"tag": "lark_md", "content": content},
	}
}

// fieldRow 构建一个包含两个短 lark_md 字段的 div。
func fieldRow(left, right string) map[string]any {
	return map[string]any{
		"tag": "div",
		"fields": []any{
			map[string]any{"is_short": true, "text": map[string]any{"tag": "lark_md", "content": left}},
			map[string]any{"is_short": true, "text": map[string]any{"tag": "lark_md", "content": right}},
		},
	}
}

// version 按 Jenkins 模板的方式渲染 "<branch>-<shortSHA>"。
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

// formatDuration 以 Jenkins 风格渲染时长（"3 min 46 sec"）。正在运行的
// 构建（开始事件）会像 Jenkins 那样追加 "and counting"。
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

// summarize 去除错误文本首尾空白并按 maxErrRunes 截断，以保持卡片精简。
func summarize(s string) string { return summarizeN(s, maxErrRunes) }

// summarizeN 去除 s 首尾空白，并截断到 n 个字符（rune）。
func summarizeN(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-100]) + "\n...（已截断）"
}

func orNA(s string) string {
	if strings.TrimSpace(s) == "" {
		return na
	}
	return s
}

// formatTime 按 Asia/Shanghai（UTC+8）渲染时间戳，零值返回 N/A。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return na
	}
	return t.In(shanghai()).Format("2006-01-02 15:04:05")
}

// shanghai 固定使用 UTC+8，无需加载 tzdata（运行时镜像为精简版）。
func shanghai() *time.Location {
	return time.FixedZone("CST", 8*60*60)
}
