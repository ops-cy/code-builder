package webhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"code-builder/internal/builder"
	"code-builder/internal/config"
	"code-builder/internal/deploy"
	"code-builder/internal/gitx"
	"code-builder/internal/notify"
	"code-builder/internal/queue"
	"code-builder/internal/store"

	"github.com/gin-gonic/gin"
)

// maxBodyBytes 限制 webhook 负载的大小。
const maxBodyBytes = 1 << 20 // 1 MiB

// Deps 汇总 handler 的依赖项。所有与具体仓库相关的内容都在每个事件中推导，
// 这里只存放全局策略。
type Deps struct {
	// Providers 是已启用的平台配置（gitlab / github / gitee）。
	Providers     map[string]config.Provider
	Branches      []string
	DeployKeyPath string // 可选的 SSH 部署密钥，用于所有 clone（由环境变量提供）
	Secret        string // 所有平台共用的 Webhook Secret
	Store         *store.Store
	Queue         *queue.Queue
	Logger        *slog.Logger
	Workspace     string
	Registry      config.RegistryConfig
	Build         config.BuildConfig
	Timeouts      config.TimeoutsConfig
	Deploy        config.DeployConfig
	Notify        notify.Sender
}

// Handler 返回处理 POST /webhook 与 POST /webhook/:provider 的 gin handler。
// 去重表和镜像 registry 都是该 handler 内部、跨请求共享的状态。
func Handler(deps Deps) gin.HandlerFunc {
	dup := newDedup()
	imgs := newImageRegistry()
	return func(c *gin.Context) {
		logger := deps.Logger

		providerName := strings.ToLower(strings.TrimSpace(c.Param("provider")))
		if providerName == "" {
			providerName = strings.ToLower(strings.TrimSpace(c.GetHeader("X-Builder-Provider")))
		}

		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "payload too large"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "read_body_failed"})
			return
		}

		if providerName == "" {
			detected, err := detectProvider(c.Request, body)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "unknown_provider"})
				return
			}
			providerName = detected
		}
		provider, ok := deps.Providers[providerName]
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider_not_enabled", "provider": providerName})
			return
		}

		if err := verifyWebhook(providerName, deps.Secret, c.Request, body); err != nil {
			logger.InfoContext(c.Request.Context(), "webhook: verification failed",
				slog.String("provider", providerName), slog.Any("err", err))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "provider": providerName})
			return
		}

		ev, err := parseEvent(providerName, c.Request, body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_json", "provider": providerName})
			return
		}
		if ev.Kind == "ping" {
			c.JSON(http.StatusOK, gin.H{"status": "pong", "provider": providerName})
			return
		}
		if !ev.IsPush() {
			c.JSON(http.StatusAccepted, gin.H{"status": "ignored", "reason": "not a push event", "provider": providerName})
			return
		}
		if ev.IsDelete() {
			c.JSON(http.StatusAccepted, gin.H{"status": "ignored", "reason": "ref deletion", "provider": providerName})
			return
		}
		branch, ok := ev.Branch()
		if !ok {
			c.JSON(http.StatusAccepted, gin.H{"status": "ignored", "reason": "not a branch ref", "provider": providerName})
			return
		}

		repo, reason := deriveRepository(provider, deps.DeployKeyPath != "", imgs, ev)
		if reason != "" {
			logger.InfoContext(c.Request.Context(), "webhook: event rejected",
				slog.String("provider", providerName),
				slog.String("reason", reason),
				slog.String("ssh_url", gitx.MaskURL(ev.RepoSSHURL)),
				slog.String("http_url", gitx.MaskURL(ev.RepoHTTPURL)))
			c.JSON(http.StatusAccepted, gin.H{"status": reason, "provider": providerName, "branch": branch})
			return
		}
		if !(config.CodeConfig{Branches: deps.Branches}).BranchAllowed(branch) {
			logger.InfoContext(c.Request.Context(), "webhook: branch filtered",
				slog.String("provider", providerName),
				slog.String("repo", repo.Name), slog.String("branch", branch))
			c.JSON(http.StatusAccepted, gin.H{"status": "branch_filtered", "provider": providerName, "repo": repo.Name, "branch": branch})
			return
		}
		sha := ev.SHA()
		if !validCommitSHA(sha) {
			logger.InfoContext(c.Request.Context(), "webhook: invalid commit sha",
				slog.String("provider", providerName),
				slog.String("repo", repo.Name), slog.String("sha", sha))
			c.JSON(http.StatusAccepted, gin.H{"status": "ignored", "reason": "invalid commit sha", "provider": providerName})
			return
		}
		tag := deps.Build.ResolveTag(branch, sha)

		// 重复投递防护：同一仓库的同一 commit 在其构建仍在排队或运行时不得再次构建。
		dkey := repo.Key + "@" + sha
		if id, ok := dup.get(dkey); ok {
			logger.InfoContext(c.Request.Context(), "webhook: duplicate delivery skipped",
				slog.String("provider", providerName),
				slog.String("repo", repo.Name), slog.String("sha", sha),
				slog.String("build_id", id))
			c.JSON(http.StatusAccepted, gin.H{"status": "duplicate", "provider": providerName, "build_id": id, "repo": repo.Name, "sha": sha})
			return
		}

		repo.DeployKeyPath = deps.DeployKeyPath
		build := deps.Store.Create(store.Build{
			Repo:          repo.Name,
			FullName:      repo.FullName,
			Namespace:     repo.Namespace,
			Provider:      repo.Provider,
			Branch:        branch,
			SHA:           sha,
			ShortSHA:      sha[:8],
			Actor:         ev.Actor,
			CommitSubject: ev.CommitSubject,
			Status:        store.StatusQueued,
			Tag:           tag,
			CreatedAt:     time.Now(),
		})
		dup.put(dkey, build.ID)

		run := pipeline(deps, build.ID, repo, branch, sha, tag)
		job := queue.Job{
			BuildID: build.ID,
			Repo:    repo,
			Branch:  branch,
			SHA:     sha,
			Run: func(ctx context.Context) error {
				defer dup.release(dkey, build.ID)
				return run(ctx)
			},
		}
		if err := deps.Queue.Enqueue(job); err != nil {
			dup.release(dkey, build.ID)
			deps.Store.Update(build.ID, func(b *store.Build) {
				b.Status = store.StatusFailed
				b.Error = err.Error()
				b.FinishedAt = time.Now()
			})
			notifyEvent(deps, build.ID, notify.EventFailure, err.Error())
			logger.ErrorContext(c.Request.Context(), "webhook: enqueue failed",
				slog.String("build_id", build.ID),
				slog.String("reason", err.Error()),
				slog.Any("err", err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "queue_full", "build_id": build.ID})
			return
		}

		logger.InfoContext(c.Request.Context(), "webhook: build queued",
			slog.String("provider", providerName),
			slog.String("build_id", build.ID),
			slog.String("repo", repo.Name),
			slog.String("full_name", repo.FullName),
			slog.String("branch", branch),
			slog.String("sha", sha),
			slog.String("image", repo.Build.Image))
		c.JSON(http.StatusAccepted, gin.H{
			"status":    "queued",
			"provider":  providerName,
			"build_id":  build.ID,
			"repo":      repo.Name,
			"full_name": repo.FullName,
			"branch":    branch,
			"sha":       sha,
			"image":     repo.Build.Image,
			"tag":       tag,
		})
	}
}

// validCommitSHA 要求完整的十六进制对象 id：40 个字符（SHA-1）或 64 个字符（SHA-256）。
func validCommitSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// pipeline 返回任务执行函数：标记为运行中、带超时地执行构建、把结果和完整
// 原因同时写入构建日志和 store，然后在启用部署且存在匹配目标时执行部署。
func pipeline(deps Deps, buildID string, repo config.Repository, branch, sha, tag string) func(context.Context) error {
	return func(ctx context.Context) error {
		started := time.Now()
		deps.Store.Update(buildID, func(b *store.Build) {
			b.Status = store.StatusRunning
			b.StartedAt = time.Now()
		})
		notifyEvent(deps, buildID, notify.EventStarted, "")

		logf, err := deps.Store.OpenLog(buildID)
		if err != nil {
			deps.Store.Update(buildID, func(b *store.Build) {
				b.Status = store.StatusFailed
				b.Error = err.Error()
				b.FinishedAt = time.Now()
			})
			notifyEvent(deps, buildID, notify.EventFailure, err.Error())
			return fmt.Errorf("open log: %w", err)
		}
		defer logf.Close()

		fmt.Fprintf(logf, "build %s provider=%s repo=%s url=%s branch=%s sha=%s image=%s tag=%s\n",
			buildID, repo.Provider, repo.Name, gitx.MaskURL(repo.URL), branch, sha, repo.Build.Image, tag)

		jobCtx, cancel := context.WithTimeout(ctx, deps.Timeouts.JobDuration())
		defer cancel()
		tags, err := builder.Build(jobCtx, builder.Request{
			Repo:         repo,
			Branch:       branch,
			SHA:          sha,
			Tag:          tag,
			Log:          logf,
			Workspace:    deps.Workspace,
			Registry:     deps.Registry,
			Build:        deps.Build,
			CloneTimeout: deps.Timeouts.CloneDuration(),
			BuildTimeout: deps.Timeouts.BuildDuration(),
		})
		duration := time.Since(started).Round(time.Millisecond)
		if err != nil {
			deps.Store.Update(buildID, func(b *store.Build) {
				b.Status = store.StatusFailed
				b.Error = err.Error()
				b.FinishedAt = time.Now()
			})
			fmt.Fprintf(logf, "==> result failed duration=%s error=%s\n", duration, err)
			deps.Logger.ErrorContext(ctx, "build failed",
				slog.String("build_id", buildID),
				slog.String("repo", repo.Name),
				slog.Duration("duration", duration),
				slog.Any("err", err))
			notifyEvent(deps, buildID, notify.EventFailure, err.Error())
			return err
		}

		deps.Store.Update(buildID, func(b *store.Build) {
			b.Status = store.StatusSuccess
			b.ImageTags = tags
		})

		deployAfterBuild(ctx, deps, buildID, repo.Name, logf)

		deps.Store.Update(buildID, func(b *store.Build) { b.FinishedAt = time.Now() })
		fmt.Fprintf(logf, "==> result success duration=%s image=%s\n", duration, strings.Join(tags, ", "))
		deps.Logger.InfoContext(ctx, "build succeeded",
			slog.String("build_id", buildID),
			slog.String("repo", repo.Name),
			slog.String("tags", strings.Join(tags, ",")),
			slog.Duration("duration", duration))
		notifyEvent(deps, buildID, notify.EventSuccess, "")
		return nil
	}
}

// 部署接缝：使用包级变量，便于测试替换 docker CLI。
var (
	deployUp   = deploy.Up
	deployPs   = deploy.Ps
	deployLogs = deploy.Logs
)

// deployAfterBuild 应用与 repoName 匹配的已配置部署目标。跳过或失败的部署
// 永远不会改变构建成功的状态；其结果会记录到 store 中，供成功卡片使用。
func deployAfterBuild(ctx context.Context, deps Deps, buildID, repoName string, logf io.Writer) {
	record := func(status, reason, target, errMsg, output string, deployed bool) {
		deps.Store.Update(buildID, func(b *store.Build) {
			b.DeployStatus = status
			b.DeployReason = reason
			b.DeployTarget = target
			b.DeployError = errMsg
			b.DeployOutput = output
			b.Deployed = deployed
		})
	}

	if !deps.Deploy.Enabled {
		reason := "deploy not enabled"
		fmt.Fprintf(logf, "deploy skipped: %s\n", reason)
		record(notify.DeploySkipped, reason, "", "", "", false)
		return
	}
	b, _ := deps.Store.Get(buildID)
	tgt, ok := deps.Deploy.Match(repoName, b.FullName)
	if !ok {
		tgt, ok = discoverTarget(deps.Deploy, repoName, b.FullName, b.Namespace)
	}
	if !ok {
		reason := "no target for " + repoName
		fmt.Fprintf(logf, "deploy skipped: %s\n", reason)
		record(notify.DeploySkipped, reason, "", "", "", false)
		return
	}

	target := deploy.Target{
		Dir:           tgt.Dir,
		ComposeFile:   tgt.ComposeFile,
		ComposeFiles:  deps.Deploy.ComposeFileCandidates(),
		Service:       tgt.Service,
		ProjectName:   tgt.ProjectName,
		Env:           tgt.Env,
		Wait:          deps.Deploy.WaitEnabled(),
		WaitTimeout:   deps.Deploy.WaitTimeout,
		Pull:          deps.Deploy.Pull,
		RemoveOrphans: deps.Deploy.RemoveOrphansEnabled(),
	}
	// 尽量提前解析 compose 文件名，便于日志与通知展示；失败时交给
	// deploy.Up 返回更完整的错误。
	if resolved, err := deploy.Resolve(target); err == nil {
		target = resolved
	}

	req := deploy.Request{
		Repo:     repoName,
		FullName: b.FullName,
		Provider: b.Provider,
		Image:    b.Repo + ":" + b.Tag,
		Tag:      b.Tag,
		Branch:   b.Branch,
		SHA:      b.SHA,
		Target:   target,
		Timeout:  deps.Timeouts.DeployDuration(),
		Log:      logf,
	}
	if err := deployUp(ctx, req); err != nil {
		output, psErr := deployPs(ctx, req)
		if psErr != nil {
			if output != "" {
				output += "\n"
			}
			output += "compose ps error: " + psErr.Error()
		}
		logs, logsErr := deployLogs(ctx, req)
		if logsErr != nil {
			if logs != "" {
				logs += "\n"
			}
			logs += "compose logs error: " + logsErr.Error()
		}
		if strings.TrimSpace(logs) != "" {
			if output != "" {
				output += "\n"
			}
			output += "compose logs:\n" + logs
		}
		fmt.Fprintf(logf, "==> deploy failed repo=%s target=%s error=%s\n", repoName, target.Dir, err)
		deps.Logger.ErrorContext(ctx, "deploy failed",
			slog.String("build_id", buildID),
			slog.String("repo", repoName),
			slog.String("target", target.Dir),
			slog.Any("err", err))
		record(notify.DeployFailed, "", target.File(), err.Error(), output, false)
		return
	}

	fmt.Fprintf(logf, "==> deploy success repo=%s target=%s\n", repoName, target.Dir)
	deps.Logger.InfoContext(ctx, "deploy succeeded",
		slog.String("build_id", buildID),
		slog.String("repo", repoName),
		slog.String("target", target.Dir))
	record(notify.DeployDeployed, "", target.File(), "", "", true)
}

// discoverTarget 在 deploy.root 下按标准布局查找服务目录：
//
//	<root>/<repo_name>
//	<root>/<namespace>/<repo_name>
//	<root>/<full_name>
func discoverTarget(cfg config.DeployConfig, repoName, fullName, namespace string) (config.DeployTarget, bool) {
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		return config.DeployTarget{}, false
	}
	candidates := make([]string, 0, 3)
	if dir, ok := safeJoin(root, repoName); ok {
		candidates = append(candidates, dir)
	}
	if namespace != "" {
		if dir, ok := safeJoin(root, namespace+"/"+repoName); ok {
			candidates = append(candidates, dir)
		}
	}
	if fullName != "" {
		if dir, ok := safeJoin(root, fullName); ok {
			candidates = append(candidates, dir)
		}
	}
	seen := make(map[string]bool, len(candidates))
	for _, dir := range candidates {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if t, ok := deploy.TargetFromDir(dir, cfg.ComposeFileCandidates()); ok {
			return t, true
		}
	}
	return config.DeployTarget{}, false
}

// safeJoin 将事件中的相对仓库路径安全地拼到部署根目录下，拒绝空段、
// ".."、绝对路径与反斜杠，防止 payload 逃逸 deploy.root。
func safeJoin(root, rel string) (string, bool) {
	root = filepath.Clean(root)
	raw := strings.TrimSpace(rel)
	if raw == "" || strings.HasPrefix(raw, "/") || filepath.IsAbs(raw) {
		return "", false
	}
	rel = strings.Trim(raw, "/")
	if rel == "" {
		return "", false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, `\:`) {
			return "", false
		}
	}
	dir := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if dir != root && !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		return "", false
	}
	return dir, true
}

// notifyEvent 发送一条流水线通知，既不阻塞也不影响构建。
func notifyEvent(deps Deps, buildID string, ev notify.Event, errMsg string) {
	if deps.Notify == nil || !deps.Notify.Wants(ev) {
		return
	}
	b, ok := deps.Store.Get(buildID)
	if !ok {
		return
	}
	var dur time.Duration
	if !b.StartedAt.IsZero() {
		end := b.FinishedAt
		if end.IsZero() {
			end = time.Now()
		}
		dur = end.Sub(b.StartedAt)
	}
	msg := notify.Message{
		Event:        ev,
		App:          b.Repo,
		Actor:        b.Actor,
		Branch:       b.Branch,
		ShortSHA:     b.ShortSHA,
		Image:        b.Repo,
		Duration:     dur,
		Subject:      b.CommitSubject,
		Err:          errMsg,
		DeployStatus: b.DeployStatus,
		DeployReason: b.DeployReason,
		DeployTarget: b.DeployTarget,
		DeployError:  b.DeployError,
		DeployOutput: b.DeployOutput,
		StartedAt:    b.StartedAt,
		Now:          time.Now(),
	}
	if b.Tag != "" {
		msg.Image = b.Repo + ":" + b.Tag
	}
	if ev == notify.EventFailure {
		msg.LogTail = deps.Store.ReadLogTail(buildID, 30)
	}
	deps.Notify.Async(msg, func(err error) {
		if err == nil {
			return
		}
		deps.Store.Update(buildID, func(bb *store.Build) { bb.NotifyError = err.Error() })
	})
}
