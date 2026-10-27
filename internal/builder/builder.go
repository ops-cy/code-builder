// Package builder 在精确的提交上克隆仓库，并通过 docker CLI（经由挂载的 Docker
// socket 与守护进程通信）基于该仓库构建本地 Docker 镜像。
package builder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab-builder/internal/config"
	"gitlab-builder/internal/gitx"
)

// Request 是解析后的构建输入。
type Request struct {
	Repo      config.Repository
	Branch    string
	SHA       string
	Tag       string // 解析后的镜像 tag（由配置驱动；未设置时为 sha8）
	Log       *os.File
	Workspace string
	// Registry 是可选的推送目标；当其 URL 为空时，镜像仅保留在本地。
	Registry config.RegistryConfig
	// CloneTimeout / BuildTimeout 约束克隆和 docker build 步骤的时长；为零时沿用包内的默认值。
	CloneTimeout time.Duration
	BuildTimeout time.Duration
}

// Build 在 SHA 处克隆仓库，选择构建配方（优先使用仓库自带的根目录 Dockerfile，否则使用
// 内置语言模板），计算 sha8 tag，然后执行 docker build。当 req.Registry 启用时，镜像还会被
// 打上完整限定的远程 ref，并在构建成功后推送。
// 进度写入 req.Log：克隆耗时、所选 dockerfile 来源、docker 命令及其输出、最终镜像 tag。
// 错误信息中绝不包含凭据（URL 会被掩码，registry 密码从不写入日志）。
func Build(ctx context.Context, req Request) ([]string, error) {
	fmt.Fprintf(req.Log, "==> clone start repo=%s url=%s branch=%s sha=%s\n",
		req.Repo.Name, gitx.MaskURL(req.Repo.URL), req.Branch, req.SHA)

	dest, err := workspaceDir(req.Workspace, req.Repo, req.SHA)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(dest); err != nil {
		return nil, fmt.Errorf("builder: clean workspace %s: %w", dest, err)
	}
	defer func() { _ = os.RemoveAll(dest) }()

	cloneStart := time.Now()
	if err := gitx.CloneAtSHA(ctx, gitx.CloneReq{
		URL:        req.Repo.URL,
		Branch:     req.Branch,
		SHA:        req.SHA,
		Dest:       dest,
		DeployKey:  req.Repo.DeployKeyPath,
		KnownHosts: filepath.Join(req.Workspace, ".ssh", "known_hosts"),
		Timeout:    req.CloneTimeout,
	}, req.Log); err != nil {
		return nil, fmt.Errorf("builder: clone: %w", err)
	}
	fmt.Fprintf(req.Log, "==> clone done duration=%s\n", time.Since(cloneStart).Round(time.Millisecond))

	dockerfileAbs, dockerfileSource, err := resolveDockerfile(dest)
	if err != nil {
		return nil, fmt.Errorf("builder: %w", err)
	}
	fmt.Fprintf(req.Log, "==> dockerfile source=%s path=%s\n", dockerfileSource, dockerfileAbs)

	tags, pushRefs, login, err := imageRefs(req.Repo.Build.Image, req.Tag, req.Registry)
	if err != nil {
		return nil, fmt.Errorf("builder: %w", err)
	}
	if len(pushRefs) > 0 {
		fmt.Fprintf(req.Log, "==> registry push enabled remote=%s\n", pushRefs[0])
	}

	if err := dockerBuild(ctx, dockerBuildRequest{
		ContextDir: dest,
		Dockerfile: dockerfileAbs,
		Tags:       tags,
		PushRefs:   pushRefs,
		Login:      login,
		Log:        req.Log,
		Timeout:    req.BuildTimeout,
	}); err != nil {
		return nil, fmt.Errorf("builder: docker build: %w", err)
	}
	fmt.Fprintf(req.Log, "==> image tags: %s (full sha %s)\n", strings.Join(tags, ", "), req.SHA)
	return tags, nil
}

// imageRefs 依据镜像名和已解析的 tag，组合出 docker tag 参数、需要推送的子集以及可选的
// registry 登录信息。该函数是纯函数（不涉及 docker，也不涉及 git），因此推送组合逻辑可做单元测试：
//   - 本地 tag   {image}:{tag}                     （始终生成）
//   - 远程 ref   {url}[/{project}]/{image}:{tag}   （仅在启用时生成）
//   - 登录       仅当配置了用户名时才设置
func imageRefs(image, tag string, reg config.RegistryConfig) (tags, pushRefs []string, login *dockerLogin, err error) {
	if err := config.ValidateTag(tag); err != nil {
		return nil, nil, nil, err
	}
	tags = []string{image + ":" + tag}
	if !reg.Enabled() {
		return tags, nil, nil, nil
	}
	remote := reg.RemoteRef(image, tag)
	tags = append(tags, remote)
	pushRefs = append(pushRefs, remote)
	if reg.Username != "" {
		login = &dockerLogin{
			Registry: reg.URL,
			Username: reg.Username,
			Password: reg.Password,
		}
	}
	return tags, pushRefs, login, nil
}

// workspaceDir 返回每个任务的克隆目录：
// {workspace}/{name}-{repoHash}-{sha8}。name 已预先净化，但结果仍会对照 workspace 根目录
// 再次校验，确保任何来自事件的值都无法逃逸出 workspace（纵深防御式的路径穿越防护）。
func workspaceDir(workspace string, repo config.Repository, sha string) (string, error) {
	repoHash := sha256hex(repo.Key)[:8]
	short := sha
	if len(short) > 8 {
		short = short[:8]
	}
	dir := fmt.Sprintf("%s-%s-%s", repo.Name, repoHash, short)
	dest := filepath.Join(workspace, dir)
	root := filepath.Clean(workspace)
	abs, err := filepath.Abs(dest)
	if err != nil {
		return "", fmt.Errorf("builder: resolve workspace path: %w", err)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("builder: resolve workspace root: %w", err)
	}
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("builder: workspace path %q escapes root %q", dest, workspace)
	}
	return dest, nil
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
