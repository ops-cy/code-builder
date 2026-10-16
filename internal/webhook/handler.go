package webhook

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"gitlab-builder/internal/builder"
	"gitlab-builder/internal/config"
	"gitlab-builder/internal/gitx"
	"gitlab-builder/internal/queue"
	"gitlab-builder/internal/store"

	"github.com/gin-gonic/gin"
)

// maxBodyBytes limits the webhook payload size.
const maxBodyBytes = 1 << 20 // 1 MiB

// buildTimeout bounds one build job (clone + docker build) so a hung git or
// docker child process can never occupy a worker slot forever.
const buildTimeout = 30 * time.Minute

// Deps groups the handler's dependencies. Everything repository-specific is
// derived per event; only global policy lives here.
type Deps struct {
	Domain        string
	Branches      []string
	DeployKeyPath string // optional SSH deploy key for all clones (env-provided)
	Secret        string
	Store         *store.Store
	Queue         *queue.Queue
	Logger        *slog.Logger
	Workspace     string
}

// Handler returns the gin handler for POST /webhook. The dedup table and
// image registry are per-handler state shared across requests.
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

		// Redelivery guard: the same commit of the same repository must not
		// be built twice while its build is still queued or running.
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
			Repo:      repo.Name,
			Branch:    branch,
			SHA:       sha,
			ShortSHA:  sha[:8],
			Status:    store.StatusQueued,
			CreatedAt: time.Now(),
		})
		dup.put(dkey, build.ID)

		run := pipeline(deps, build.ID, repo, branch, sha)
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
			"tag":      sha[:8],
		})
	}
}

// validCommitSHA requires a full hex object id: 40 (SHA-1, Git default) or
// 64 (SHA-256) characters. The 8-char image tag is cut from it later.
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

// pipeline returns the job runner: mark running, build with timeout, record
// result and full reason in both the build log and the store.
func pipeline(deps Deps, buildID string, repo config.Repository, branch, sha string) func(context.Context) error {
	return func(ctx context.Context) error {
		started := time.Now()
		deps.Store.Update(buildID, func(b *store.Build) {
			b.Status = store.StatusRunning
			b.StartedAt = time.Now()
		})

		logf, err := deps.Store.OpenLog(buildID)
		if err != nil {
			deps.Store.Update(buildID, func(b *store.Build) {
				b.Status = store.StatusFailed
				b.Error = err.Error()
				b.FinishedAt = time.Now()
			})
			return fmt.Errorf("open log: %w", err)
		}
		defer logf.Close()

		fmt.Fprintf(logf, "build %s repo=%s url=%s branch=%s sha=%s image=%s\n",
			buildID, repo.Name, gitx.MaskURL(repo.URL), branch, sha, repo.Build.Image)

		bctx, cancel := context.WithTimeout(ctx, buildTimeout)
		defer cancel()
		tags, err := builder.Build(bctx, builder.Request{
			Repo:      repo,
			Branch:    branch,
			SHA:       sha,
			Log:       logf,
			Workspace: deps.Workspace,
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
			return err
		}
		deps.Store.Update(buildID, func(b *store.Build) {
			b.Status = store.StatusSuccess
			b.ImageTags = tags
			b.FinishedAt = time.Now()
		})
		fmt.Fprintf(logf, "==> result success duration=%s image=%s\n", duration, strings.Join(tags, ", "))
		deps.Logger.InfoContext(ctx, "build succeeded",
			slog.String("build_id", buildID),
			slog.String("repo", repo.Name),
			slog.String("tags", strings.Join(tags, ",")),
			slog.Duration("duration", duration))
		return nil
	}
}
