// Package webhook 处理 GitLab push 事件并入队构建任务。
package webhook

import "strings"

// PushEvent 是 GitLab push 事件 JSON 负载中解析出的子集。
type PushEvent struct {
	ObjectKind  string `json:"object_kind"`
	EventName   string `json:"event_name"`
	Ref         string `json:"ref"`
	Before      string `json:"before"`
	After       string `json:"after"`
	CheckoutSHA string `json:"checkout_sha"`
	// UserName 是发起推送的人（作为通知发起者展示）。
	UserName string `json:"user_name"`
	Project  struct {
		Name              string `json:"name"`
		PathWithNamespace string `json:"path_with_namespace"`
		GitHTTPURL        string `json:"git_http_url"`
		GitSSHURL         string `json:"git_ssh_url"`
	} `json:"project"`
	Commits []struct {
		ID        string `json:"id"`
		Message   string `json:"message"`
		Title     string `json:"title"`
		Timestamp string `json:"timestamp"`
		Author    struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"commits"`
}

// isPush 判断该事件是否为分支 push。
func (e PushEvent) isPush() bool {
	return e.ObjectKind == "push" || e.EventName == "push"
}

// isDelete 判断该 push 是否删除 ref（没有新的 commit）。
func (e PushEvent) isDelete() bool {
	if e.CheckoutSHA == "" {
		return true
	}
	for _, c := range e.After {
		if c != '0' {
			return false
		}
	}
	return true
}

// branch 去掉 refs/heads/ 前缀；对于非分支 ref，ok 为 false。
func (e PushEvent) branch() (string, bool) {
	const prefix = "refs/heads/"
	if len(e.Ref) <= len(prefix) || e.Ref[:len(prefix)] != prefix {
		return "", false
	}
	return e.Ref[len(prefix):], true
}

// sha 优先使用 checkout_sha，缺失时回退到 after。
func (e PushEvent) sha() string {
	if e.CheckoutSHA != "" {
		return e.CheckoutSHA
	}
	return e.After
}

// actor 返回推送发起者；当 GitLab 未提供 user_name 时，回退到最后一次
// commit 的作者。
func (e PushEvent) actor() string {
	if s := strings.TrimSpace(e.UserName); s != "" {
		return s
	}
	for i := len(e.Commits) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(e.Commits[i].Author.Name); s != "" {
			return s
		}
	}
	return ""
}

// commitSubject 返回被推送 commit 的单行摘要：优先取 id 等于 sha 的 commit，
// 否则取最后一个 commit。Title 优先于 message 的第一行。
func (e PushEvent) commitSubject(sha string) string {
	if len(e.Commits) == 0 {
		return ""
	}
	c := e.Commits[len(e.Commits)-1]
	for _, candidate := range e.Commits {
		if candidate.ID == sha {
			c = candidate
			break
		}
	}
	if s := firstLine(c.Title); s != "" {
		return s
	}
	return firstLine(c.Message)
}

// firstLine 去掉首尾空白，只保留第一行。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
