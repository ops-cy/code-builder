package gitx

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func Test_SSHCommand(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "empty key yields empty command", key: "", want: ""},
		{
			name: "key path yields hardened ssh command",
			key:  "/run/secrets/deploy_key",
			want: "-i /run/secrets/deploy_key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SSHCommand(tt.key)
			if tt.want == "" {
				if got != "" {
					t.Errorf("SSHCommand(%q) = %q, want empty", tt.key, got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("SSHCommand(%q) = %q, want it to contain %q", tt.key, got, tt.want)
			}
			for _, flag := range []string{"IdentitiesOnly=yes", "BatchMode=yes", "StrictHostKeyChecking=no"} {
				if !strings.Contains(got, flag) {
					t.Errorf("SSHCommand missing %q: %q", flag, got)
				}
			}
		})
	}
}

func Test_CloneAtSHA_when_DestExists_thenReturnErrorImmediately(t *testing.T) {
	// Given
	dest := t.TempDir() // already exists

	// When
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := CloneAtSHA(ctx, CloneReq{
		URL:    "ssh://git@example.com/g/app.git",
		Branch: "main",
		SHA:    "abcdef",
		Dest:   dest,
	}, nil)

	// Then
	if err == nil {
		t.Fatal("CloneAtSHA() error = nil, want destination exists error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want mention of already exists", err)
	}
}

func Test_CloneAtSHA_when_InvalidURL_thenReturnError(t *testing.T) {
	// Given
	dest := filepath.Join(t.TempDir(), "repo")

	// When
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := CloneAtSHA(ctx, CloneReq{
		URL:    "://bad-url",
		Branch: "main",
		SHA:    "abcdef",
		Dest:   dest,
	}, nil)

	// Then
	if err == nil {
		t.Fatal("CloneAtSHA() error = nil, want clone error")
	}
}
