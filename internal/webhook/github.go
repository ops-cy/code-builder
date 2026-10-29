package webhook

import (
	"encoding/json"
	"net/http"

	"code-builder/internal/config"
)

// githubEvent 是 GitHub push / ping 负载的解析子集。
type githubEvent struct {
	Ref     string `json:"ref"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Deleted bool   `json:"deleted"`
	Created bool   `json:"created"`
	Forced  bool   `json:"forced"`
	Zen     string `json:"zen"`
	HookID  int64  `json:"hook_id"`
	Pusher  struct {
		Name string `json:"name"`
	} `json:"pusher"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
		SSHURL   string `json:"ssh_url"`
		CloneURL string `json:"clone_url"`
		HTMLURL  string `json:"html_url"`
		Owner    struct {
			Login string `json:"login"`
			Name  string `json:"name"`
		} `json:"owner"`
	} `json:"repository"`
	HeadCommit *githubCommit  `json:"head_commit"`
	Commits    []githubCommit `json:"commits"`
}

type githubCommit struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	Author  struct {
		Name  string `json:"name"`
		Login string `json:"username"`
	} `json:"author"`
}

func parseGitHub(r *http.Request, body []byte) (Event, error) {
	var p githubEvent
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, err
	}
	ev := Event{
		Provider:    config.ProviderGitHub,
		Kind:        "other",
		Ref:         p.Ref,
		Before:      p.Before,
		After:       p.After,
		Deleted:     p.Deleted,
		RepoSSHURL:  p.Repository.SSHURL,
		RepoHTTPURL: p.Repository.CloneURL,
		FullName:    p.Repository.FullName,
		Namespace:   p.Repository.Owner.Login,
		Actor:       p.Pusher.Name,
		DeliveryID:  r.Header.Get("X-GitHub-Delivery"),
	}
	if ev.Namespace == "" {
		ev.Namespace = deriveNamespace(ev.FullName)
	}

	eventName := r.Header.Get("X-GitHub-Event")
	switch {
	case eventName == "ping" || (p.Zen != "" && p.Ref == ""):
		ev.Kind = "ping"
	case eventName == "push":
		ev.Kind = "push"
	case eventName == "" && p.Ref != "" && p.After != "":
		ev.Kind = "push"
	}

	if ev.Actor == "" && p.HeadCommit != nil && p.HeadCommit.Author.Name != "" {
		ev.Actor = p.HeadCommit.Author.Name
	}
	if ev.Actor == "" && len(p.Commits) > 0 {
		ev.Actor = p.Commits[len(p.Commits)-1].Author.Name
	}
	if p.HeadCommit != nil {
		ev.CommitSubject = firstLine(p.HeadCommit.Message)
	}
	if ev.CommitSubject == "" && len(p.Commits) > 0 {
		ev.CommitSubject = firstLine(p.Commits[len(p.Commits)-1].Message)
	}
	return ev, nil
}
