package webhook

import (
	"testing"

	"gitlab-builder/internal/config"
)

func Test_MatchRepo(t *testing.T) {
	repos := []config.Repository{
		{Name: "app", URL: "ssh://git@example.com:20022/group/app.git"},
		{Name: "web", URL: "git@example.com:group/web.git"},
	}
	tests := []struct {
		name    string
		sshURL  string
		httpURL string
		want    string
	}{
		{name: "exact ssh url", sshURL: "ssh://git@example.com:20022/group/app.git", want: "app"},
		{name: "http url matches ssh config", httpURL: "https://example.com:20022/group/app.git", want: "app"},
		{name: "scp style", sshURL: "git@example.com:group/web.git", want: "web"},
		{name: "no match", sshURL: "ssh://git@example.com/group/nope.git"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, ok := MatchRepo(repos, tt.sshURL, tt.httpURL)
			if tt.want == "" {
				if ok {
					t.Errorf("MatchRepo() = %v, want no match", r)
				}
				return
			}
			if !ok || r.Name != tt.want {
				t.Errorf("MatchRepo() = (%v, %v), want %q", r, ok, tt.want)
			}
		})
	}
}

func Test_NormalizeGitURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "ssh scheme stripped", in: "ssh://git@example.com:20022/g/app.git", want: "example.com:20022/g/app"},
		{name: "git@ prefix stripped", in: "git@example.com:g/app.git", want: "example.com/g/app"},
		{name: "https stripped", in: "https://example.com/g/app.git", want: "example.com/g/app"},
		{name: "empty stays empty", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeGitURL(tt.in); got != tt.want {
				t.Errorf("normalizeGitURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
