// Package gitx clones Git repositories over SSH using deploy keys.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// CloneReq describes one clone-at-commit operation.
type CloneReq struct {
	URL       string
	Branch    string
	SHA       string
	Dest      string
	DeployKey string // path to an SSH private key; "" uses default ssh identity
}

// SSHCommand returns the GIT_SSH_COMMAND value for the given deploy key path.
// An empty key path yields "" so the default ssh configuration applies.
func SSHCommand(deployKey string) string {
	if deployKey == "" {
		return ""
	}
	return fmt.Sprintf(
		"ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes",
		deployKey,
	)
}

// CloneAtSHA shallow-clones the repository (Dest must not exist), then checks
// out exactly req.SHA. Git output streams to logw when non-nil.
func CloneAtSHA(ctx context.Context, req CloneReq, logw io.Writer) error {
	if _, err := os.Stat(req.Dest); err == nil {
		return fmt.Errorf("gitx: destination already exists: %s", req.Dest)
	}
	if logw == nil {
		logw = io.Discard
	}

	if key := SSHCommand(req.DeployKey); key != "" {
		_ = os.Setenv("GIT_SSH_COMMAND", key)
		defer func() { _ = os.Unsetenv("GIT_SSH_COMMAND") }()
	}

	if err := runGit(ctx, logw, "", "clone", "--no-checkout", "--depth", "50", "--branch", req.Branch, req.URL, req.Dest); err != nil {
		return fmt.Errorf("gitx: clone: %w", err)
	}
	// The requested commit may already be within the shallow history; fetch by
	// SHA is best-effort (servers must allow it) and failure is tolerated.
	if err := runGit(ctx, logw, req.Dest, "fetch", "--depth", "50", "origin", req.SHA); err != nil {
		fmt.Fprintf(logw, "gitx: fetch of %s failed (likely already present): %v\n", req.SHA, err)
	}
	if err := runGit(ctx, logw, req.Dest, "checkout", "--force", req.SHA); err != nil {
		return fmt.Errorf("gitx: checkout %s: %w", req.SHA, err)
	}
	return nil
}

// runGit executes git with args; dir non-empty sets the working directory.
// On failure the error carries the combined output tail.
func runGit(ctx context.Context, logw io.Writer, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(logw, &out)
	cmd.Stderr = io.MultiWriter(logw, &out)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, tail(out.String(), 2048))
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
