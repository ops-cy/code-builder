package webhook

import (
	"encoding/json"

	"code-builder/internal/config"
)

// gitlabEvent 是 GitLab Push Hook 负载的解析子集。
type gitlabEvent struct {
	ObjectKind  string `json:"object_kind"`
	EventName   string `json:"event_name"`
	Ref         string `json:"ref"`
	Before      string `json:"before"`
	After       string `json:"after"`
	CheckoutSHA string `json:"checkout_sha"`
	UserName    string `json:"user_name"`
	Project     struct {
		Name              string `json:"name"`
		PathWithNamespace string `json:"path_with_namespace"`
		GitHTTPURL        string `json:"git_http_url"`
		GitSSHURL         string `json:"git_ssh_url"`
	} `json:"project"`
	Commits []gitCommit `json:"commits"`
}

type gitCommit struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	Title   string `json:"title"`
	Author  struct {
		Name string `json:"name"`
	} `json:"author"`
}

func parseGitLab(body []byte) (Event, error) {
	var p gitlabEvent
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, err
	}
	ev := Event{
		Provider:    config.ProviderGitLab,
		Kind:        "other",
		Ref:         p.Ref,
		Before:      p.Before,
		After:       p.After,
		CheckoutSHA: p.CheckoutSHA,
		RepoSSHURL:  p.Project.GitSSHURL,
		RepoHTTPURL: p.Project.GitHTTPURL,
		FullName:    p.Project.PathWithNamespace,
		Actor:       p.UserName,
	}
	if p.ObjectKind == "push" || p.EventName == "push" {
		ev.Kind = "push"
	}
	ev.Namespace = deriveNamespace(ev.FullName)
	sha := ev.SHA()
	if ev.Actor == "" {
		for i := len(p.Commits) - 1; i >= 0; i-- {
			if n := p.Commits[i].Author.Name; n != "" {
				ev.Actor = n
				break
			}
		}
	}
	if len(p.Commits) > 0 {
		c := p.Commits[len(p.Commits)-1]
		for _, candidate := range p.Commits {
			if candidate.ID == sha {
				c = candidate
				break
			}
		}
		if s := firstLine(c.Title); s != "" {
			ev.CommitSubject = s
		} else {
			ev.CommitSubject = firstLine(c.Message)
		}
	}
	return ev, nil
}
