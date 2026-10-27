// Package deploy 在构建成功后，使用本地构建的镜像将某个 compose 服务目录
// 启动起来。它从不拉取镜像：compose 文件应当引用本地镜像。命令始终以
// 参数向量的形式执行（绝不通过 shell），工作目录设置为该服务目录。
package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// defaultTimeout 限制单次 compose 调用的时长，当调用方未指定时使用。
const defaultTimeout = 5 * time.Minute

// maxErrBytes 限制嵌入错误信息中的 compose 输出长度。
const maxErrBytes = 2048

// execCommand 是 exec 的接缝；测试会覆盖它来打桩 docker CLI。
var execCommand = exec.CommandContext

// fileLocks 将共享同一 compose 文件的运行串行化，确保同一服务的两次部署
// 绝不会交错执行。
var fileLocks sync.Map // 键：compose 文件路径 -> *sync.Mutex

// Target 是要启动的一个服务目录。
type Target struct {
	// Dir 是服务的绝对目录（compose 的工作目录）。
	Dir string
	// ComposeFile 是 Dir 内的文件名。
	ComposeFile string
	// Service 可选地将运行限定为单个 compose 服务。
	Service string
}

// Request 是一次部署。
type Request struct {
	Repo    string
	Target  Target
	Timeout time.Duration
	Log     io.Writer
}

// Up 运行 `docker compose -f <dir>/<file> up -d [service]`，工作目录设置为
// Dir，并将输出流式写入 req.Log。目录或 compose 文件缺失会作为部署错误
// 上报，以便调用方记录 —— 构建本身仍然保持成功。
func Up(ctx context.Context, req Request) error {
	dir, file, err := resolve(req.Target)
	if err != nil {
		return err
	}
	if _, err := run(ctx, dir, file, req, upArgs(file, req.Target.Service)); err != nil {
		return fmt.Errorf("deploy: docker compose up -d failed: %w", err)
	}
	return nil
}

// Ps 在同一目录下运行 `docker compose -f <file> ps` 并返回其合并输出。
// 它用于解释部署失败的原因。
func Ps(ctx context.Context, req Request) (string, error) {
	dir, file, err := resolve(req.Target)
	if err != nil {
		return "", err
	}
	out, err := run(ctx, dir, file, req, psArgs(file))
	if err != nil {
		return out, fmt.Errorf("deploy: docker compose ps failed: %w", err)
	}
	return out, nil
}

// upArgs 是 `up` 的精确参数向量；仅当配置了 service 时才追加（为空则启动
// 文件中的所有服务）。
func upArgs(file, service string) []string {
	args := []string{"compose", "-f", file, "up", "-d"}
	if service != "" {
		args = append(args, service)
	}
	return args
}

// psArgs 是 `ps` 的精确参数向量。
func psArgs(file string) []string {
	return []string{"compose", "-f", file, "ps"}
}

// resolve 校验目标并返回绝对目录和 compose 文件路径。
func resolve(t Target) (dir, file string, err error) {
	dir = filepath.Clean(t.Dir)
	if !filepath.IsAbs(dir) {
		return "", "", fmt.Errorf("deploy: service dir %q must be an absolute path", t.Dir)
	}
	if t.ComposeFile == "" || filepath.Base(t.ComposeFile) != t.ComposeFile || t.ComposeFile == "." || t.ComposeFile == ".." {
		return "", "", fmt.Errorf("deploy: compose file %q must be a single file name", t.ComposeFile)
	}
	st, statErr := os.Stat(dir)
	switch {
	case statErr != nil:
		return "", "", fmt.Errorf("deploy: service dir %q does not exist", dir)
	case !st.IsDir():
		return "", "", fmt.Errorf("deploy: service dir %q is not a directory", dir)
	}
	file = filepath.Join(dir, t.ComposeFile)
	st, statErr = os.Stat(file)
	switch {
	case statErr != nil:
		return "", "", fmt.Errorf("deploy: compose file %q does not exist", file)
	case st.IsDir():
		return "", "", fmt.Errorf("deploy: compose file %q is a directory", file)
	}
	return dir, file, nil
}

// run 执行一条 docker compose 命令，将 stdout+stderr 流式写入请求日志，
// 并返回合并后的输出。
func run(ctx context.Context, dir, lockKey string, req Request, args []string) (string, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logw := req.Log
	if logw == nil {
		logw = io.Discard
	}
	fmt.Fprintf(logw, "==> deploy command: docker %s (dir=%s)\n", strings.Join(args, " "), dir)

	unlock := lockFor(lockKey)
	defer unlock()

	var out bytes.Buffer
	// 同一个 writer 同时接 stdout 和 stderr：os/exec 检测到两者是同一个
	// interface 值时只会启动一个读取 goroutine，避免并发写坏 bytes.Buffer。
	w := io.MultiWriter(logw, &out)
	cmd := execCommand(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, tail(out.String(), maxErrBytes))
	}
	return out.String(), nil
}

// lockFor 将针对同一 compose 文件的运行串行化。
func lockFor(key string) func() {
	v, _ := fileLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
