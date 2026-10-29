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

	"code-builder/internal/config"
)

// defaultTimeout 限制单次 compose 调用的时长，当调用方未指定时使用。
const defaultTimeout = 5 * time.Minute

// maxErrBytes 限制嵌入错误信息中的 compose 输出长度。
const maxErrBytes = 2048

// execCommand 是 exec 的接缝；测试会覆盖它来打桩 docker CLI。
var execCommand = exec.CommandContext

// dirLocks 将同一服务目录的部署串行化，确保两次部署绝不会交错执行。
var dirLocks sync.Map // 键：目录 -> *sync.Mutex

// Target 是要启动的一个服务目录。
type Target struct {
	// Dir 是服务的绝对目录（compose 的工作目录）。
	Dir string
	// ComposeFile 是 Dir 内的文件名；为空时按 ComposeFiles 自动探测。
	ComposeFile string
	// ComposeFiles 是自动探测时尝试的文件名顺序。
	ComposeFiles []string
	// Service 可选地将运行限定为单个 compose 服务。
	Service string
	// ProjectName 是可选的 compose 项目名。
	ProjectName string
	// Env 是传给 docker compose 的额外环境变量。
	Env map[string]string
	// Wait 为 true 时使用 compose --wait 等待容器健康。
	Wait bool
	// WaitTimeout 是 --wait-timeout 的值。
	WaitTimeout string
	// Pull 传给 compose --pull：never / missing / always。
	Pull string
	// RemoveOrphans 为 true 时传 --remove-orphans。
	RemoveOrphans bool
}

// File 返回 compose 文件路径。调用方应确保 ComposeFile 已解析。
func (t Target) File() string { return filepath.Join(t.Dir, t.ComposeFile) }

// Request 是一次部署。
type Request struct {
	Repo     string
	FullName string
	Provider string
	Image    string
	Tag      string
	Branch   string
	SHA      string
	Target   Target
	Timeout  time.Duration
	Log      io.Writer
}

// Resolve 校验目标并补齐 Dir / ComposeFile（自动探测时）。
func Resolve(t Target) (Target, error) {
	dir, file, err := resolve(t)
	if err != nil {
		return t, err
	}
	t.Dir = dir
	t.ComposeFile = filepath.Base(file)
	return t, nil
}

// Describe 校验显式配置的部署目标并补齐 compose 文件名。ComposeFile 为空时
// 在目录内按 candidates 顺序自动探测。
func Describe(t config.DeployTarget, candidates []string) (Target, error) {
	dir, file, err := resolveDir(t.Dir, t.ComposeFile, candidates)
	if err != nil {
		return Target{}, err
	}
	return Target{
		Dir:           dir,
		ComposeFile:   filepath.Base(file),
		ComposeFiles:  candidates,
		Service:       t.Service,
		ProjectName:   t.ProjectName,
		Env:           t.Env,
		Wait:          true,
		WaitTimeout:   "120s",
		Pull:          "never",
		RemoveOrphans: true,
	}, nil
}

// TargetFromDir 在自动发现模式下检查目录是否存在并找到 compose 文件。
func TargetFromDir(dir string, candidates []string) (config.DeployTarget, bool) {
	clean := filepath.Clean(dir)
	if !filepath.IsAbs(clean) {
		return config.DeployTarget{}, false
	}
	st, err := os.Stat(clean)
	if err != nil || !st.IsDir() {
		return config.DeployTarget{}, false
	}
	file, err := findComposeFile(clean, candidates)
	if err != nil {
		return config.DeployTarget{}, false
	}
	return config.DeployTarget{Dir: clean, ComposeFile: file}, true
}

// Up 运行 `docker compose up -d`，工作目录设置为 Dir，并将输出流式写入
// req.Log。目录或 compose 文件缺失会作为部署错误上报，以便调用方记录。
func Up(ctx context.Context, req Request) error {
	dir, file, err := resolve(req.Target)
	if err != nil {
		return err
	}
	if _, err := run(ctx, dir, req, upArgs(file, req.Target)); err != nil {
		return fmt.Errorf("deploy: docker compose up -d failed: %w", err)
	}
	return nil
}

// Ps 在同一目录下运行 `docker compose ps` 并返回其合并输出。
func Ps(ctx context.Context, req Request) (string, error) {
	dir, file, err := resolve(req.Target)
	if err != nil {
		return "", err
	}
	out, err := run(ctx, dir, req, psArgs(file, req.Target))
	if err != nil {
		return out, fmt.Errorf("deploy: docker compose ps failed: %w", err)
	}
	return out, nil
}

// Logs 收集服务最近的容器日志，用于部署失败后的诊断。
func Logs(ctx context.Context, req Request) (string, error) {
	dir, file, err := resolve(req.Target)
	if err != nil {
		return "", err
	}
	out, err := run(ctx, dir, req, logsArgs(file, req.Target))
	if err != nil {
		return out, fmt.Errorf("deploy: docker compose logs failed: %w", err)
	}
	return out, nil
}

// upArgs 是 `up` 的精确参数向量。
func upArgs(file string, t Target) []string {
	args := composeBase(file, t)
	args = append(args, "up", "-d", "--no-build", "--pull", pullValue(t.Pull))
	if t.RemoveOrphans {
		args = append(args, "--remove-orphans")
	}
	if t.Wait {
		timeout := strings.TrimSpace(t.WaitTimeout)
		if timeout == "" {
			timeout = "120s"
		}
		args = append(args, "--wait", "--wait-timeout", timeout)
	}
	if t.Service != "" {
		args = append(args, t.Service)
	}
	return args
}

func psArgs(file string, t Target) []string {
	args := composeBase(file, t)
	return append(args, "ps")
}

func logsArgs(file string, t Target) []string {
	args := composeBase(file, t)
	args = append(args, "logs", "--tail", "100", "--no-color")
	if t.Service != "" {
		args = append(args, t.Service)
	}
	return args
}

func composeBase(file string, t Target) []string {
	args := []string{"compose", "-f", file}
	if t.ProjectName != "" {
		args = append(args, "--project-name", t.ProjectName)
	}
	return args
}

func pullValue(p string) string {
	switch p {
	case "missing", "always":
		return p
	default:
		return "never"
	}
}

// resolve 校验目标并返回绝对目录和 compose 文件路径。
func resolve(t Target) (dir, file string, err error) {
	// ComposeFile 为空时由 resolveDir 在目录内自动探测。
	return resolveDir(t.Dir, t.ComposeFile, t.ComposeFiles)
}

func resolveDir(rawDir, rawFile string, candidates []string) (dir, file string, err error) {
	dir = filepath.Clean(rawDir)
	if !filepath.IsAbs(dir) {
		return "", "", fmt.Errorf("deploy: service dir %q must be an absolute path", rawDir)
	}
	if rawFile != "" && (filepath.Base(rawFile) != rawFile || rawFile == "." || rawFile == "..") {
		return "", "", fmt.Errorf("deploy: compose file %q must be a single file name", rawFile)
	}
	st, statErr := os.Stat(dir)
	switch {
	case statErr != nil:
		return "", "", fmt.Errorf("deploy: service dir %q does not exist", dir)
	case !st.IsDir():
		return "", "", fmt.Errorf("deploy: service dir %q is not a directory", dir)
	}
	if rawFile == "" {
		rawFile, err = findComposeFile(dir, candidates)
		if err != nil {
			return "", "", err
		}
	}
	file = filepath.Join(dir, rawFile)
	st, statErr = os.Stat(file)
	switch {
	case statErr != nil:
		return "", "", fmt.Errorf("deploy: compose file %q does not exist", file)
	case st.IsDir():
		return "", "", fmt.Errorf("deploy: compose file %q is a directory", file)
	}
	return dir, file, nil
}

func findComposeFile(dir string, candidates []string) (string, error) {
	if len(candidates) == 0 {
		candidates = config.DefaultComposeFiles
	}
	tried := make([]string, 0, len(candidates))
	for _, name := range candidates {
		if name == "" || filepath.Base(name) != name {
			continue
		}
		path := filepath.Join(dir, name)
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return name, nil
		}
		tried = append(tried, name)
	}
	return "", fmt.Errorf("deploy: no compose file in %q (tried: %s)", dir, strings.Join(tried, ", "))
}

// run 执行一条 docker compose 命令，将 stdout+stderr 流式写入请求日志，
// 并返回合并后的输出。
func run(ctx context.Context, dir string, req Request, args []string) (string, error) {
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

	unlock := lockFor(dir)
	defer unlock()

	var out bytes.Buffer
	w := io.MultiWriter(logw, &out)
	cmd := execCommand(ctx, "docker", args...)
	cmd.Dir = dir
	baseEnv := cmd.Env
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	cmd.Env = deployEnv(req, baseEnv)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, tail(out.String(), maxErrBytes))
	}
	return out.String(), nil
}

// deployEnv 向 compose 注入本次构建的上下文，compose 可用
// ${BUILDER_IMAGE} / ${BUILDER_TAG} 等变量引用刚构建出的本地镜像。
func deployEnv(req Request, base []string) []string {
	env := append(base,
		"BUILDER_IMAGE="+req.Image,
		"BUILDER_TAG="+req.Tag,
		"BUILDER_SHA="+req.SHA,
		"BUILDER_BRANCH="+req.Branch,
		"BUILDER_REPO="+req.Repo,
		"BUILDER_FULL_NAME="+req.FullName,
		"BUILDER_PROVIDER="+req.Provider,
	)
	for k, v := range req.Target.Env {
		env = append(env, k+"="+v)
	}
	return env
}

func lockFor(key string) func() {
	v, _ := dirLocks.LoadOrStore(filepath.Clean(key), &sync.Mutex{})
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
