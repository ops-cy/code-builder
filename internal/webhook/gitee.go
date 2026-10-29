package webhook

import (
	"encoding/json"
	"strings"

	"code-builder/internal/config"
)

// giteeEvent 是 Gitee Push Hook 负载的解析子集。
type giteeEvent struct {
	Ref      string `json:"ref"`
	Before   string `json:"before"`
	After    string `json:"after"`
	HookName string `json:"hook_name"`
	Password string `json:"password"`
	Pusher   struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"pusher"`
	Sender struct {
		Login string `json:"login"`
		Name  string `json:"name"`
	} `json:"sender"`
	Repository struct {
		Name              string `json:"name"`
		Path              string `json:"path"`
		FullName          string `json:"full_name"`
		PathWithNamespace string `json:"path_with_namespace"`
		Namespace         string `json:"namespace"`
		URL               string `json:"url"`
		SSHURL            string `json:"ssh_url"`
		HTTPURL           string `json:"git_http_url"`
		CloneURL          string `json:"clone_url"`
		HTMLURL           string `json:"html_url"`
		Owner             struct {
			Login string `json:"login"`
			Name  string `json:"name"`
		} `json:"owner"`
	} `json:"repository"`
	HeadCommit *giteeCommit  `json:"head_commit"`
	Commits    []giteeCommit `json:"commits"`
}

type giteeCommit struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	Title   string `json:"title"`
	Author  struct {
		Name string `json:"name"`
	} `json:"author"`
}

func parseGitee(body []byte) (Event, error) {
	var p giteeEvent
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, err
	}
	fullName := strings.TrimSpace(p.Repository.PathWithNamespace)
	if fullName == "" {
		fullName = strings.TrimSpace(p.Repository.FullName)
	}
	if fullName == "" {
		fullName = strings.TrimSpace(p.Repository.Path)
	}
	if fullName == "" && p.Repository.Owner.Login != "" {
		fullName = strings.Trim(p.Repository.Owner.Login+"/"+p.Repository.Name, "/")
	}
	httpURL := strings.TrimSpace(p.Repository.CloneURL)
	if httpURL == "" {
		httpURL = strings.TrimSpace(p.Repository.HTTPURL)
	}
	if httpURL == "" {
		httpURL = strings.TrimSpace(p.Repository.URL)
	}
	if httpURL == "" {
		httpURL = strings.TrimSpace(p.Repository.HTMLURL)
	}
	ev := Event{
		Provider:    config.ProviderGitee,
		Kind:        "other",
		Ref:         p.Ref,
		Before:      p.Before,
		After:       p.After,
		RepoSSHURL:  p.Repository.SSHURL,
		RepoHTTPURL: httpURL,
		FullName:    fullName,
		Namespace:   strings.Trim(p.Repository.Namespace, "/"),
		Actor:       p.Pusher.Name,
	}
	if ev.Namespace == "" {
		ev.Namespace = strings.Trim(p.Repository.Owner.Login, "/")
	}
	if ev.Namespace == "" {
		ev.Namespace = deriveNamespace(fullName)
	}
	if ev.Actor == "" {
		if p.Sender.Name != "" {
			ev.Actor = p.Sender.Name
		} else {
			ev.Actor = p.Sender.Login
		}
	}
	switch {
	case p.HookName == "push_hooks":
		ev.Kind = "push"
	case p.HookName == "" && p.Ref != "" && p.After != "":
		ev.Kind = "push"
	case p.Ref == "" && p.HookName != "":
		ev.Kind = "ping"
	}

	if ev.Actor == "" && p.HeadCommit != nil {
		ev.Actor = p.HeadCommit.Author.Name
	}
	if ev.Actor == "" && len(p.Commits) > 0 {
		ev.Actor = p.Commits[len(p.Commits)-1].Author.Name
	}
	if p.HeadCommit != nil {
		if s := firstLine(p.HeadCommit.Title); s != "" {
			ev.CommitSubject = s
		} else {
			ev.CommitSubject = firstLine(p.HeadCommit.Message)
		}
	}
	if ev.CommitSubject == "" && len(p.Commits) > 0 {
		c := p.Commits[len(p.Commits)-1]
		if s := firstLine(c.Title); s != "" {
			ev.CommitSubject = s
		} else {
			ev.CommitSubject = firstLine(c.Message)
		}
	}
	return ev, nil
}
