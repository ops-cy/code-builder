// Package gitx 通过 SSH 或 HTTPS 在精确的提交上克隆 Git 仓库。
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

// cloneTimeout 限制整个 clone+fetch+checkout 过程的时长，确保障碍的网络
// 永远不会一直占住一个构建工作槽位。
const cloneTimeout = 15 * time.Minute

// CloneReq 描述一次 clone-at-commit 操作。
type CloneReq struct {
	URL        string
	Branch     string
	SHA        string
	Dest       string
	DeployKey  string        // SSH 私钥路径；为 "" 时使用默认 ssh 身份
	KnownHosts string        // 持久化 known_hosts 的路径；为 "" 时使用 ssh 默认值
	Timeout    time.Duration // 整个 clone+checkout 的时限；为 0 时使用 cloneTimeout
}

// SSHCommand 返回给定部署密钥对应的 GIT_SSH_COMMAND 值。
// 密钥路径为空时返回 ""，从而沿用默认 ssh 配置。
//
// 主机密钥会被验证：accept-new 在首次连接时信任该主机（TOFU）并写入 knownHosts，
// 但之后仍会拒绝已变化的密钥 —— 验证绝不会被完全关闭。
func SSHCommand(deployKey, knownHosts string) string {
	if deployKey == "" {
		return ""
	}
	cmd := "ssh -i " + strconv.Quote(deployKey) +
		" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new" +
		" -o ConnectTimeout=10 -o ServerAliveInterval=15"
	if knownHosts != "" {
		cmd += " -o UserKnownHostsFile=" + strconv.Quote(knownHosts)
	}
	return cmd
}

// CloneAtSHA 浅克隆仓库（Dest 必须不存在），然后精确检出 req.SHA —— 而不是分支尖端。
// Git 输出在 logw 非 nil 时流式写入。环境变量按命令设置（不做进程级修改），
// 并且禁用交互式凭据提示，使缺失凭据能快速失败，而不是挂起到构建超时。
func CloneAtSHA(ctx context.Context, req CloneReq, logw io.Writer) error {
	if _, err := os.Stat(req.Dest); err == nil {
		return fmt.Errorf("gitx: destination already exists: %s", req.Dest)
	}
	if logw == nil {
		logw = io.Discard
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = cloneTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
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

	// 优先 blobless clone：拿到完整提交历史但不下载文件内容，checkout 时按需
	// 取 blob；比 --depth 50 更能保证 checkout 到较老的 commit。部分服务端不支持
	// partial clone，失败后回退到浅克隆。
	if err := runGit(ctx, env, logw, "", "clone", "--no-checkout", "--filter=blob:none", "--branch", req.Branch, req.URL, req.Dest); err != nil {
		fmt.Fprintf(logw, "gitx: blobless clone failed, falling back to shallow clone: %v\n", err)
		_ = os.RemoveAll(req.Dest)
		if err := runGit(ctx, env, logw, "", "clone", "--no-checkout", "--depth", "50", "--branch", req.Branch, req.URL, req.Dest); err != nil {
			return fmt.Errorf("gitx: clone: %w", err)
		}
	}

	// 确保目标 commit 存在；不存在时按 SHA fetch，仍失败则拉全量引用。
	if err := runGit(ctx, env, logw, req.Dest, "cat-file", "-e", req.SHA+"^{commit}"); err != nil {
		fmt.Fprintf(logw, "gitx: commit %s not in clone, fetching it\n", req.SHA)
		if err := runGit(ctx, env, logw, req.Dest, "fetch", "--filter=blob:none", "origin", req.SHA); err != nil {
			fmt.Fprintf(logw, "gitx: fetch by sha failed, fetching all branches: %v\n", err)
			_ = runGit(ctx, env, logw, req.Dest, "fetch", "--filter=blob:none", "origin", "+refs/heads/*:refs/remotes/origin/*")
			_ = runGit(ctx, env, logw, req.Dest, "fetch", "--unshallow")
		}
	}
	if err := runGit(ctx, env, logw, req.Dest, "checkout", "--force", req.SHA); err != nil {
		return fmt.Errorf("gitx: checkout %s: %w", req.SHA, err)
	}
	return nil
}

// runGit 使用给定环境执行 git；dir 非空时设置工作目录。失败时返回的错误会附带
// 合并输出的尾部，并对 URL 中的 userinfo 凭据做掩码处理。
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

// MaskURL 在 URL 进入日志或错误信息之前隐藏其中的 userinfo 凭据：
// "https://user:pass@host/x" 变成 "https://***@host/x"。
// scp 形式的 "git@host:path" 不携带密码，原样返回。
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
		return u // '@' 属于路径的一部分，而不是 userinfo
	}
	return u[:schemeEnd+3] + "***" + rest[at:]
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
