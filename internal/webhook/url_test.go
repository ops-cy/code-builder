package webhook

import "testing"

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
