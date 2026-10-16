// Package gitx clones Git repositories over SSH or HTTPS at an exact commit.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cloneTimeout bounds a whole clone+fetch+checkout sequence so a stalled
// network can never hold a build worker slot forever.
const cloneTimeout = 15 * time.Minute

// CloneReq describes one clone-at-commit operation.
type CloneReq struct {
	URL        string
	Branch     string
	SHA        string
	Dest       string
	DeployKey  string // path to an SSH private key; "" uses default ssh identity
	KnownHosts string // path to a persistent known_hosts; "" uses the ssh default
}

// SSHCommand returns the GIT_SSH_COMMAND value for the given deploy key.
// An empty key path yields "" so the default ssh configuration applies.
//
// Host keys are verified: accept-new trusts a host on first contact (TOFU)
// and stores it in knownHosts, but still REJECTS a changed key afterwards —
// verification is never disabled outright.
func SSHCommand(deployKey, knownHosts string) string {
	if deployKey == "" {
		return ""
	}
	cmd := "ssh -i " + strconv.Quote(deployKey) +
		" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new"
	if knownHosts != "" {
		cmd += " -o UserKnownHostsFile=" + strconv.Quote(knownHosts)
	}
	return cmd
}

// CloneAtSHA shallow-clones the repository (Dest must not exist), then checks
// out exactly req.SHA — never the branch tip. Git output streams to logw
// when non-nil. Environment is per-command (no process-global mutation), and
// interactive credential prompts are disabled so a missing credential fails
// fast instead of hanging until the build timeout.
func CloneAtSHA(ctx context.Context, req CloneReq, logw io.Writer) error {
	if _, err := os.Stat(req.Dest); err == nil {
		return fmt.Errorf("gitx: destination already exists: %s", req.Dest)
	}
	if logw == nil {
		logw = io.Discard
	}
	ctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	if req.KnownHosts != "" {
		if err := os.MkdirAll(filepath.Dir(req.KnownHosts), 0o700); err != nil {
			return fmt.Errorf("gitx: create known_hosts dir: %w", err)
		}
	}

	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if cmd := SSHCommand(req.DeployKey, req.KnownHosts); cmd != "" {
		env = append(env, "GIT_SSH_COMMAND="+cmd)
	}

	if err := runGit(ctx, env, logw, "", "clone", "--no-checkout", "--depth", "50", "--branch", req.Branch, req.URL, req.Dest); err != nil {
		return fmt.Errorf("gitx: clone: %w", err)
	}
	// The requested commit may already be within the shallow history; fetch by
	// SHA is best-effort (servers must allow it) and failure is tolerated —
	// the checkout below is the actual correctness gate.
	if err := runGit(ctx, env, logw, req.Dest, "fetch", "--depth", "50", "origin", req.SHA); err != nil {
		fmt.Fprintf(logw, "gitx: fetch of %s failed (likely already present): %v\n", req.SHA, err)
	}
	if err := runGit(ctx, env, logw, req.Dest, "checkout", "--force", req.SHA); err != nil {
		return fmt.Errorf("gitx: checkout %s: %w", req.SHA, err)
	}
	return nil
}

// runGit executes git with the given environment; dir non-empty sets the
// working directory. On failure the error carries the combined output tail
// with URL userinfo credentials masked.
func runGit(ctx context.Context, env []string, logw io.Writer, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(logw, &out)
	cmd.Stderr = io.MultiWriter(logw, &out)
	if err := cmd.Run(); err != nil {
		masked := make([]string, len(args))
		for i, a := range args {
			masked[i] = MaskURL(a)
		}
		return fmt.Errorf("git %s: %w: %s", strings.Join(masked, " "), err, tail(out.String(), 2048))
	}
	return nil
}

// MaskURL redacts userinfo credentials in a URL before it reaches a log or
// an error message: "https://user:pass@host/x" becomes "https://***@host/x".
// scp-style "git@host:path" carries no password and is returned unchanged.
func MaskURL(u string) string {
	schemeEnd := strings.Index(u, "://")
	if schemeEnd < 0 {
		return u
	}
	rest := u[schemeEnd+3:]
	at := strings.Index(rest, "@")
	if at < 0 {
		return u
	}
	if slash := strings.IndexByte(rest, '/'); slash >= 0 && slash < at {
		return u // the '@' is part of the path, not userinfo
	}
	return u[:schemeEnd+3] + "***" + rest[at:]
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
