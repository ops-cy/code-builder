package builder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// execCommand 是 exec 的接缝；测试会覆盖它以桩掉 docker CLI。
var execCommand = exec.CommandContext

// dockerBuildTimeout 限制 docker 可用性探测加上一次构建的总时长，避免卡死的守护进程
// 或巨大的构建上下文永久占用一个工作槽位。
const dockerBuildTimeout = 25 * time.Minute

type dockerBuildRequest struct {
	ContextDir string
	Dockerfile string
	Tags       []string // 每个 -t 参数（本地 tag，推送时还包含远程 ref）
	PushRefs   []string // 构建成功后要推送的完整限定 ref
	Login      *dockerLogin
	Log        io.Writer
	Timeout    time.Duration // 为零时使用 dockerBuildTimeout
}

// dockerLogin 是可选的 registry 凭据；非 nil 时会在构建前执行 `docker login`，
// 以便随后的推送获得授权。密码通过 stdin 传入，绝不写入日志。
type dockerLogin struct {
	Registry string
	Username string
	Password string
}

// dockerBuild 通过 DOCKER_HOST 访问守护进程（服务容器中挂载的 Docker socket），
// 执行 `docker build`，随后在 registry 启用时逐个推送 PushRefs 中的条目。
// 确切的命令及其完整输出都会流式写入 req.Log。
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
	for _, tag := range req.Tags {
		args = append(args, "-t", tag)
	}
	args = append(args, req.ContextDir)

	fmt.Fprintf(req.Log, "==> docker command: docker %s\n", strings.Join(args, " "))

	var out bytes.Buffer
	cmd := execCommand(ctx, "docker", args...)
	cmd.Stdout = io.MultiWriter(req.Log, &out)
	cmd.Stderr = io.MultiWriter(req.Log, &out)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w: %s", err, tail(out.String(), 2048))
	}

	for _, ref := range req.PushRefs {
		if err := dockerPush(ctx, ref, req.Log); err != nil {
			return err
		}
	}
	return nil
}

// dockerLoginRun 使用来自 stdin 的密码完成 registry 认证。
func dockerLoginRun(ctx context.Context, l *dockerLogin, logw io.Writer) error {
	fmt.Fprintf(logw, "==> docker login %s (user=%s)\n", l.Registry, l.Username)
	cmd := execCommand(ctx, "docker", "login", l.Registry, "-u", l.Username, "--password-stdin")
	cmd.Stdin = strings.NewReader(l.Password + "\n")
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(logw, &out)
	cmd.Stderr = io.MultiWriter(logw, &out)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login %s failed: %w: %s", l.Registry, err, tail(out.String(), 1024))
	}
	return nil
}

// dockerPush 推送一个完整限定的镜像引用。
func dockerPush(ctx context.Context, ref string, logw io.Writer) error {
	fmt.Fprintf(logw, "==> docker push %s\n", ref)
	cmd := execCommand(ctx, "docker", "push", ref)
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(logw, &out)
	cmd.Stderr = io.MultiWriter(logw, &out)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker push %s failed: %w: %s", ref, err, tail(out.String(), 2048))
	}
	return nil
}

// dockerAvailable 对守护进程做一次探测，失败时给出清晰的错误。
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

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
