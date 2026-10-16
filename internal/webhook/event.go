// Package webhook handles GitLab push events and enqueues build jobs.
package webhook

// PushEvent is the parsed subset of GitLab's push event JSON payload.
type PushEvent struct {
	ObjectKind  string `json:"object_kind"`
	EventName   string `json:"event_name"`
	Ref         string `json:"ref"`
	Before      string `json:"before"`
	After       string `json:"after"`
	CheckoutSHA string `json:"checkout_sha"`
	Project     struct {
		Name              string `json:"name"`
		PathWithNamespace string `json:"path_with_namespace"`
		GitHTTPURL        string `json:"git_http_url"`
		GitSSHURL         string `json:"git_ssh_url"`
	} `json:"project"`
	Commits []struct {
		ID string `json:"id"`
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
