// Package webhook handles GitLab push events and enqueues build jobs.
package webhook

import "strings"

// PushEvent is the parsed subset of GitLab's push event JSON payload.
type PushEvent struct {
	ObjectKind  string `json:"object_kind"`
	EventName   string `json:"event_name"`
	Ref         string `json:"ref"`
	Before      string `json:"before"`
	After       string `json:"after"`
	CheckoutSHA string `json:"checkout_sha"`
	// UserName is the person who pushed (shown as the notification initiator).
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

// isPush reports whether the event is a branch push.
func (e PushEvent) isPush() bool {
	return e.ObjectKind == "push" || e.EventName == "push"
}

// isDelete reports whether the push deletes a ref (no new commit).
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

// branch strips the refs/heads/ prefix; ok is false for non-branch refs.
func (e PushEvent) branch() (string, bool) {
	const prefix = "refs/heads/"
	if len(e.Ref) <= len(prefix) || e.Ref[:len(prefix)] != prefix {
		return "", false
	}
	return e.Ref[len(prefix):], true
}

// sha prefers checkout_sha and falls back to after.
func (e PushEvent) sha() string {
	if e.CheckoutSHA != "" {
		return e.CheckoutSHA
	}
	return e.After
}

// actor returns the push initiator, falling back to the last commit author
// when GitLab omits user_name.
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

// commitSubject returns the one-line summary of the pushed commit: the commit
// whose id equals sha, otherwise the last commit. Title wins over the first
// line of message.
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

// firstLine trims surrounding space and keeps only the first line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
