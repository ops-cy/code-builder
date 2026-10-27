package webhook

import "testing"

func Test_repoNameFromURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"https with .git", "https://gitlab.com/group/Atlas-Market.git", "Atlas-Market"},
		{"https without .git", "https://gitlab.com/group/atlas-market", "atlas-market"},
		{"ssh scheme", "ssh://git@gitlab.com:20022/group/atlas-market.git", "atlas-market"},
		{"scp syntax", "git@gitlab.com:group/atlas-market.git", "atlas-market"},
		{"scp syntax without .git", "git@gitlab.com:group/atlas-market", "atlas-market"},
		{"trailing slash", "https://gitlab.com/group/repo/", "repo"},
		{"trailing slash with .git", "https://gitlab.com/group/repo.git/", "repo"},
		{"embedded credentials", "https://user:token@gitlab.com/group/repo.git", "repo"},
		{"token only", "https://glpat-abc@gitlab.com/group/repo.git", "repo"},
		{"query string", "https://gitlab.com/group/repo.git?foo=bar", "repo"},
		{"fragment", "https://gitlab.com/group/repo.git#main", "repo"},
		{"nested group", "https://gitlab.com/a/b/c/deep-repo.git", "deep-repo"},
		{"single segment", "https://gitlab.com/repo.git", "repo"},
		{"scp single segment", "git@gitlab.com:repo.git", "repo"},
		{"empty", "", ""},
		{"only slashes", "https://gitlab.com///", ""},
		{"whitespace", "  https://gitlab.com/group/repo.git  ", "repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repoNameFromURL(tt.in); got != tt.want {
				t.Errorf("repoNameFromURL(%q) = %q, want %q", tt.in, got, tt.want)
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
