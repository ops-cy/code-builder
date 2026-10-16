package config

import "testing"

func Test_BranchMatches(t *testing.T) {
	tests := []struct {
		name   string
		repo   Repository
		branch string
		want   bool
	}{
		{
			name:   "exact match passes gate",
			repo:   Repository{Branches: []string{"main"}},
			branch: "main",
			want:   true,
		},
		{
			name:   "branch not in list rejected",
			repo:   Repository{Branches: []string{"main"}},
			branch: "feature/x",
			want:   false,
		},
		{
			name:   "glob matches",
			repo:   Repository{Branches: []string{"release/*"}},
			branch: "release/1.2",
			want:   true,
		},
		{
			name:   "glob does not match",
			repo:   Repository{Branches: []string{"release/*"}},
			branch: "feature/x",
			want:   false,
		},
		{
			name:   "gate regex rejects before list check",
			repo:   Repository{Branches: []string{"main"}, BranchRegex: `^release/.*`},
			branch: "main",
			want:   false,
		},
		{
			name:   "gate regex accepts and list matches",
			repo:   Repository{Branches: []string{"release/1.0"}, BranchRegex: `^release/`},
			branch: "release/1.0",
			want:   true,
		},
		{
			name:   "empty gate regex accepts all",
			repo:   Repository{Branches: []string{"main"}},
			branch: "main",
			want:   true,
		},
		{
			name:   "invalid gate regex rejects",
			repo:   Repository{Branches: []string{"main"}, BranchRegex: "("},
			branch: "main",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.repo.BranchMatches(tt.branch); got != tt.want {
				t.Errorf("BranchMatches(%q) = %v, want %v", tt.branch, got, tt.want)
			}
		})
	}
}
