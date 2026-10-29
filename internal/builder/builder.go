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

	"code-builder/internal/config"
	"code-builder/internal/gitx"
	"code-builder/internal/network"
	"code-builder/internal/stack"
)

// Request 是解析后的构建输入。
type Request struct {
	Repo      config.Repository
	Branch    string
	SHA       string
	Tag       string // 解析后的镜像 tag（由配置驱动；未设置时为 latest）
	Log       *os.File
	Workspace string
	// Registry 是可选的推送目标；当其 URL 为空时，镜像仅保留在本地。
	Registry config.RegistryConfig
	// Build 控制额外 tag、build args、labels、平台与缓存。
	Build config.BuildConfig
	// Mirrors 是本次构建使用的国内外下载源。
	Mirrors network.Mirrors
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
	fmt.Fprintf(req.Log, "==> clone start provider=%s repo=%s url=%s branch=%s sha=%s\n",
		req.Repo.Provider, req.Repo.Name, gitx.MaskURL(req.Repo.URL), req.Branch, req.SHA)

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

	st := stack.Analyze(dest, req.Build.Stack)
	fmt.Fprintf(req.Log, "==> stack language=%s framework=%s build_tool=%s go=%s java=%s node=%s rust=%s package_manager=%s\n",
		st.Language, st.Framework, st.BuildTool, st.GoVersion, st.JavaVersion, st.NodeVersion, st.RustVersion, st.PackageManager)
	fmt.Fprintf(req.Log, "==> network %s\n", req.Mirrors.String())

	dockerfileAbs, dockerfileSource, err := resolveDockerfile(dest)
	if err != nil {
		return nil, fmt.Errorf("builder: %w", err)
	}
	fmt.Fprintf(req.Log, "==> dockerfile source=%s path=%s\n", dockerfileSource, dockerfileAbs)

	tags, pushRefs, login, err := imageRefs(req.Repo.Build.Image, req.Tag, req.Registry)
	if err != nil {
		return nil, fmt.Errorf("builder: %w", err)
	}
	tags, pushRefs = addExtraTags(req, tags, pushRefs)
	if len(pushRefs) > 0 {
		fmt.Fprintf(req.Log, "==> registry push enabled remote=%s\n", pushRefs[0])
	}

	buildArgs := map[string]string{
		"BUILD_SHA":       req.SHA,
		"BUILD_BRANCH":    req.Branch,
		"BUILD_TAG":       req.Tag,
		"BUILD_DATE":      time.Now().UTC().Format(time.RFC3339),
		"BUILD_VERSION":   req.Tag,
		"APP_NAME":        req.Repo.Name,
		"GO_VERSION":      st.GoVersion,
		"RUST_VERSION":    st.RustVersion,
		"JAVA_VERSION":    st.JavaVersion,
		"NODE_VERSION":    st.NodeVersion,
		"PACKAGE_MANAGER": st.PackageManager,
		"CGO_ENABLED":     "0",
	}
	if req.Build.Stack.CGOEnabled {
		buildArgs["CGO_ENABLED"] = "1"
	}
	setIfNotEmpty(buildArgs, "GOPROXY", req.Mirrors.GoProxy)
	setIfNotEmpty(buildArgs, "GOSUMDB", req.Mirrors.GoSumDB)
	setIfNotEmpty(buildArgs, "NPM_REGISTRY", req.Mirrors.NPMRegistry)
	setIfNotEmpty(buildArgs, "MAVEN_MIRROR", req.Mirrors.MavenMirror)
	setIfNotEmpty(buildArgs, "GRADLE_MIRROR", req.Mirrors.GradleMirror)
	setIfNotEmpty(buildArgs, "CARGO_REGISTRY", req.Mirrors.CargoRegistry)
	setIfNotEmpty(buildArgs, "APK_MIRROR", req.Mirrors.APKMirror)
	setIfNotEmpty(buildArgs, "APT_MIRROR", req.Mirrors.APTMirror)
	for k, v := range req.Build.Args {
		buildArgs[k] = v
	}
	labels := map[string]string{
		"org.opencontainers.image.revision": req.SHA,
		"org.opencontainers.image.created":  buildArgs["BUILD_DATE"],
		"org.opencontainers.image.version":  req.Tag,
		"org.opencontainers.image.source":   gitx.MaskURL(req.Repo.URL),
	}
	for k, v := range req.Build.Labels {
		labels[k] = v
	}

	if err := dockerBuild(ctx, dockerBuildRequest{
		ContextDir: dest,
		Dockerfile: dockerfileAbs,
		Tags:       tags,
		PushRefs:   pushRefs,
		Login:      login,
		BuildArgs:  buildArgs,
		Labels:     labels,
		Platform:   req.Build.Platform,
		Pull:       req.Build.Pull,
		CacheFrom:  req.Build.CacheFrom,
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

func setIfNotEmpty(m map[string]string, key, value string) {
	if strings.TrimSpace(value) != "" {
		m[key] = value
	}
}

// addExtraTags 在 build.sha_tag 启用时追加 sha-<短SHA> 本地 / 远程标签。
func addExtraTags(req Request, tags, pushRefs []string) ([]string, []string) {
	if !req.Build.ShaTag || len(req.SHA) < 8 {
		return tags, pushRefs
	}
	shaTag := "sha-" + req.SHA[:8]
	local := req.Repo.Build.Image + ":" + shaTag
	if !containsString(tags, local) {
		tags = append(tags, local)
	}
	if req.Registry.Enabled() {
		remote := req.Registry.RemoteRef(req.Repo.Build.Image, shaTag)
		if !containsString(pushRefs, remote) {
			tags = append(tags, remote)
			pushRefs = append(pushRefs, remote)
		}
	}
	return tags, pushRefs
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
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
