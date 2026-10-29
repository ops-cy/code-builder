package builder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// execCommand 是 exec 的接缝；测试会覆盖它以桩掉 docker CLI。
var execCommand = exec.CommandContext

// dockerBuildTimeout 限制 docker 可用性探测加上一次构建的总时长。
const dockerBuildTimeout = 25 * time.Minute

// outputTailBytes 是每条命令保留在错误信息中的输出上限。
const outputTailBytes = 4096

type dockerBuildRequest struct {
	ContextDir string
	Dockerfile string
	Tags       []string
	PushRefs   []string
	Login      *dockerLogin
	BuildArgs  map[string]string
	Labels     map[string]string
	Platform   string
	Pull       bool
	CacheFrom  []string
	Log        io.Writer
	Timeout    time.Duration
}

// dockerLogin 是可选的 registry 凭据；密码通过 stdin 传入，绝不写入日志。
type dockerLogin struct {
	Registry string
	Username string
	Password string
}

// dockerBuild 通过 DOCKER_HOST 访问守护进程，执行 docker build，随后在
// registry 启用时逐个推送 PushRefs。
func dockerBuild(ctx context.Context, req dockerBuildRequest) error {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = dockerBuildTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := dockerAvailable(ctx); err != nil {
		return err
	}

	if req.Login != nil {
		if err := dockerLoginRun(ctx, req.Login, req.Log); err != nil {
			return err
		}
	}

	args := []string{"build", "-f", req.Dockerfile}
	if req.Platform != "" {
		args = append(args, "--platform", req.Platform)
	}
	if req.Pull {
		args = append(args, "--pull")
	}
	for _, ref := range req.CacheFrom {
		if strings.TrimSpace(ref) != "" {
			args = append(args, "--cache-from", ref)
		}
	}
	for _, k := range sortedKeys(req.BuildArgs) {
		args = append(args, "--build-arg", k+"="+req.BuildArgs[k])
	}
	for _, k := range sortedKeys(req.Labels) {
		args = append(args, "--label", k+"="+req.Labels[k])
	}
	for _, tag := range req.Tags {
		args = append(args, "-t", tag)
	}
	args = append(args, req.ContextDir)

	fmt.Fprintf(req.Log, "==> docker command: docker %s\n", strings.Join(args, " "))

	var out tailBuffer
	cmd := execCommand(ctx, "docker", args...)
	// 同一个 writer 同时接 stdout 和 stderr，避免并发写坏缓冲区。
	w := io.MultiWriter(req.Log, &out)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w: %s", err, out.String())
	}

	for _, ref := range req.PushRefs {
		if err := dockerPush(ctx, ref, req.Log); err != nil {
			return err
		}
	}
	return nil
}

func dockerLoginRun(ctx context.Context, l *dockerLogin, logw io.Writer) error {
	fmt.Fprintf(logw, "==> docker login %s (user=%s)\n", l.Registry, l.Username)
	cmd := execCommand(ctx, "docker", "login", l.Registry, "-u", l.Username, "--password-stdin")
	cmd.Stdin = strings.NewReader(l.Password + "\n")
	var out tailBuffer
	w := io.MultiWriter(logw, &out)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login %s failed: %w: %s", l.Registry, err, out.String())
	}
	return nil
}

func dockerPush(ctx context.Context, ref string, logw io.Writer) error {
	fmt.Fprintf(logw, "==> docker push %s\n", ref)
	cmd := execCommand(ctx, "docker", "push", ref)
	var out tailBuffer
	w := io.MultiWriter(logw, &out)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker push %s failed: %w: %s", ref, err, out.String())
	}
	return nil
}

func dockerAvailable(ctx context.Context) error {
	var out bytes.Buffer
	cmd := execCommand(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker daemon not reachable (is DOCKER_HOST set / the socket mounted?): %w: %s", err, tail(out.String(), 1024))
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// tailBuffer 只保留写入内容末尾的 max 字节，避免大构建把整段输出留在内存。
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		b.max = outputTailBytes
	}
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
