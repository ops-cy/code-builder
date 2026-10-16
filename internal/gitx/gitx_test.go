package gitx

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func Test_SSHCommand_when_NoKey_thenReturnEmpty(t *testing.T) {
	// Given
	// When
	got := SSHCommand("", "/data/known_hosts")

	// Then
	if got != "" {
		t.Errorf("SSHCommand() = %q, want empty without deploy key", got)
	}
}

func Test_SSHCommand_when_KeyAndKnownHosts_thenReturnHardenendOptions(t *testing.T) {
	// Given
	key := "/run/secrets/deploy_key"

	// When
	got := SSHCommand(key, "")

	// Then
	for _, want := range []string{
		"-i",
		key,
		"IdentitiesOnly=yes",
		"BatchMode=yes",
		"StrictHostKeyChecking=accept-new",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("SSHCommand() = %q, missing %q", got, want)
		}
	}
	// host key checking must never be fully disabled
	if strings.Contains(got, "StrictHostKeyChecking=no") {
		t.Errorf("SSHCommand() = %q, must not disable host key checking", got)
	}
}

func Test_SSHCommand_when_KnownHostsPath_ThenPinUserKnownHostsFile(t *testing.T) {
	// Given
	known := filepath.Join(t.TempDir(), "known_hosts")

	// When
	got := SSHCommand("/run/secrets/deploy_key", known)

	// Then
	// the path is shell-quoted (backslashes escaped on Windows)
	if !strings.Contains(got, "UserKnownHostsFile="+strconv.Quote(known)) {
		t.Errorf("SSHCommand() = %q, missing UserKnownHostsFile=%q", got, strconv.Quote(known))
	}
}

func Test_MaskURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "http basic auth masked", in: "https://user:pass@example.com/g/app.git", want: "https://***@example.com/g/app.git"},
		{name: "ssh git user masked", in: "ssh://git@example.com:20022/g/app.git", want: "ssh://***@example.com:20022/g/app.git"},
		{name: "scp style unchanged", in: "git@example.com:group/app.git", want: "git@example.com:group/app.git"},
		{name: "no userinfo unchanged", in: "https://example.com/g/app.git", want: "https://example.com/g/app.git"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskURL(tt.in); got != tt.want {
				t.Errorf("MaskURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func Test_CloneAtSHA_when_DestExists_thenReturnError(t *testing.T) {
	// Given
	dest := t.TempDir()

	// When
	err := CloneAtSHA(context.Background(), CloneReq{
		URL:  "https://example.com/g/app.git",
		SHA:  "abc",
		Dest: dest,
	}, io.Discard)

	// Then
	if err == nil {
		t.Fatal("CloneAtSHA() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "destination already exists") {
		t.Errorf("error = %v, want destination already exists", err)
	}
}

func Test_runGit_when_Failure_ThenMasksURLCredentials(t *testing.T) {
	// Given: an unknown subcommand makes git fail locally — no network involved.

	// When
	err := runGit(context.Background(), os.Environ(), io.Discard, "",
		"not-a-real-git-subcommand", "https://user:secret@example.invalid/x.git")

	// Then
	if err == nil {
		t.Fatal("runGit() error = nil, want error")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error leaks credentials: %v", err)
	}
	if !strings.Contains(err.Error(), "https://***@example.invalid/x.git") {
		t.Errorf("error = %v, want masked url", err)
	}
}
