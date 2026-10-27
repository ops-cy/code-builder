package webhook

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gitlab-builder/internal/builder"
	"gitlab-builder/internal/config"
	"gitlab-builder/internal/deploy"
	"gitlab-builder/internal/gitx"
	"gitlab-builder/internal/notify"
	"gitlab-builder/internal/queue"
	"gitlab-builder/internal/store"

	"github.com/gin-gonic/gin"
)

// maxBodyBytes 限制 webhook 负载的大小。
const maxBodyBytes = 1 << 20 // 1 MiB

// Deps 汇总 handler 的依赖项。所有与具体仓库相关的内容都在每个事件中推导，
// 这里只存放全局策略。
type Deps struct {
	Domain        string
	Branches      []string
	DeployKeyPath string // 可选的 SSH 部署密钥，用于所有 clone（由环境变量提供）
	Secret        string
	Store         *store.Store
	Queue         *queue.Queue
	Logger        *slog.Logger
	Workspace     string
	Registry      config.RegistryConfig // 可选的镜像推送目标（受 URL 白名单限制）
	Build         config.BuildConfig    // tag 策略（为空时默认 latest）
	Timeouts      config.TimeoutsConfig // 各步骤的超时上限（job/clone/build/deploy）
	Deploy        config.DeployConfig   // 可选的构建后 compose up -d
	Notify        notify.Sender         // 可选的流水线通知（nil 表示关闭）
}

// Handler 返回处理 POST /webhook 的 gin handler。去重表和镜像 registry 都是
// 该 handler 内部、跨请求共享的状态。
func Handler(deps Deps) gin.HandlerFunc {
	dup := newDedup()
	imgs := newImageRegistry()
	return func(c *gin.Context) {
		logger := deps.Logger

		token := c.GetHeader("X-Gitlab-Token")
		if subtle.ConstantTimeCompare([]byte(token), []byte(deps.Secret)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		var ev PushEvent
		if err := json.NewDecoder(c.Request.Body).Decode(&ev); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "payload too large"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_json"})
			return
		}

		if !ev.isPush() {
			c.JSON(http.StatusAccepted, gin.H{"status": "ignored", "reason": "not a push event"})
			return
		}
		if ev.isDelete() {
			c.JSON(http.StatusAccepted, gin.H{"status": "ignored", "reason": "ref deletion"})
			return
		}
		branch, ok := ev.branch()
		if !ok {
			c.JSON(http.StatusAccepted, gin.H{"status": "ignored", "reason": "not a branch ref"})
			return
		}

		repo, reason := deriveRepository(deps.Domain, imgs, ev)
		if reason != "" {
			logger.InfoContext(c.Request.Context(), "webhook: event rejected",
				slog.String("reason", reason),
				slog.String("ssh_url", gitx.MaskURL(ev.Project.GitSSHURL)),
				slog.String("http_url", gitx.MaskURL(ev.Project.GitHTTPURL)))
			c.JSON(http.StatusAccepted, gin.H{"status": reason, "branch": branch})
			return
		}
		if !slices.Contains(deps.Branches, branch) {
			logger.InfoContext(c.Request.Context(), "webhook: branch filtered",
				slog.String("repo", repo.Name), slog.String("branch", branch))
			c.JSON(http.StatusAccepted, gin.H{"status": "branch_filtered", "repo": repo.Name, "branch": branch})
			return
		}
		sha := ev.sha()
		if !validCommitSHA(sha) {
			logger.InfoContext(c.Request.Context(), "webhook: invalid commit sha",
				slog.String("repo", repo.Name), slog.String("sha", sha))
			c.JSON(http.StatusAccepted, gin.H{"status": "ignored", "reason": "invalid commit sha"})
			return
		}
		tag := deps.Build.ResolveTag(branch, sha)

		// 重复投递防护：同一仓库的同一 commit 在其构建仍在排队或运行时，
		// 不得再次构建。
		dkey := repo.Key + "@" + sha
		if id, ok := dup.get(dkey); ok {
			logger.InfoContext(c.Request.Context(), "webhook: duplicate delivery skipped",
				slog.String("repo", repo.Name), slog.String("sha", sha),
				slog.String("build_id", id))
			c.JSON(http.StatusAccepted, gin.H{"status": "duplicate", "build_id": id, "repo": repo.Name, "sha": sha})
			return
		}

		repo.DeployKeyPath = deps.DeployKeyPath
		build := deps.Store.Create(store.Build{
			Repo:          repo.Name,
			Branch:        branch,
			SHA:           sha,
			ShortSHA:      sha[:8],
			Actor:         ev.actor(),
			CommitSubject: ev.commitSubject(sha),
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
			slog.String("build_id", build.ID),
			slog.String("repo", repo.Name),
			slog.String("branch", branch),
			slog.String("sha", sha),
			slog.String("image", repo.Build.Image))
		c.JSON(http.StatusAccepted, gin.H{
			"status":   "queued",
			"build_id": build.ID,
			"repo":     repo.Name,
			"branch":   branch,
			"sha":      sha,
			"image":    repo.Build.Image,
			"tag":      tag,
		})
	}
}

// validCommitSHA 要求完整的十六进制对象 id：40 个字符（SHA-1，Git 默认）
// 或 64 个字符（SHA-256）。后面会从中截取 8 个字符作为镜像 tag。
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
// 原因同时写入构建日志和 store，然后在启用部署且存在匹配仓库名的目标时
// 执行部署。
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

		fmt.Fprintf(logf, "build %s repo=%s url=%s branch=%s sha=%s image=%s tag=%s\n",
			buildID, repo.Name, gitx.MaskURL(repo.URL), branch, sha, repo.Build.Image, tag)

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
	deployUp = deploy.Up
	deployPs = deploy.Ps
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
	tgt, ok := deps.Deploy.Target(repoName)
	if !ok {
		reason := "no target for " + repoName
		fmt.Fprintf(logf, "deploy skipped: %s\n", reason)
		record(notify.DeploySkipped, reason, "", "", "", false)
		return
	}

	target := filepath.Join(tgt.Dir, tgt.ComposeFile)
	req := deploy.Request{
		Repo: repoName,
		Target: deploy.Target{
			Dir:         tgt.Dir,
			ComposeFile: tgt.ComposeFile,
			Service:     tgt.Service,
		},
		Timeout: deps.Timeouts.DeployDuration(),
		Log:     logf,
	}
	if err := deployUp(ctx, req); err != nil {
		output, psErr := deployPs(ctx, req)
		if psErr != nil {
			if output != "" {
				output += "\n"
			}
			output += "compose ps error: " + psErr.Error()
		}
		fmt.Fprintf(logf, "==> deploy failed repo=%s target=%s error=%s\n", repoName, target, err)
		deps.Logger.ErrorContext(ctx, "deploy failed",
			slog.String("build_id", buildID),
			slog.String("repo", repoName),
			slog.String("target", target),
			slog.Any("err", err))
		record(notify.DeployFailed, "", target, err.Error(), output, false)
		return
	}

	fmt.Fprintf(logf, "==> deploy success repo=%s target=%s\n", repoName, target)
	deps.Logger.InfoContext(ctx, "deploy succeeded",
		slog.String("build_id", buildID),
		slog.String("repo", repoName),
		slog.String("target", target))
	record(notify.DeployDeployed, "", target, "", "", true)
}

// notifyEvent 发送一条流水线通知，既不阻塞也不影响构建：投递失败只会被
// 记录到 Build.NotifyError 中。
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
