package webhook

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"gitlab-builder/internal/builder"
	"gitlab-builder/internal/config"
	"gitlab-builder/internal/queue"
	"gitlab-builder/internal/store"

	"github.com/gin-gonic/gin"
)

// maxBodyBytes limits the webhook payload size.
const maxBodyBytes = 1 << 20 // 1 MiB

// Deps groups the handler's dependencies.
type Deps struct {
	Repos     []config.Repository
	Secret    string
	Store     *store.Store
	Queue     *queue.Queue
	Logger    *slog.Logger
	Workspace string
}

// Handler returns the gin handler for POST /webhook.
func Handler(deps Deps) gin.HandlerFunc {
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

		repo, found := MatchRepo(deps.Repos, ev.Project.GitSSHURL, ev.Project.GitHTTPURL)
		if !found {
			logger.InfoContext(c.Request.Context(), "webhook: unmatched repository",
				slog.String("ssh_url", ev.Project.GitSSHURL),
				slog.String("http_url", ev.Project.GitHTTPURL))
			c.JSON(http.StatusAccepted, gin.H{"status": "unmatched"})
			return
		}
		if !repo.BranchMatches(branch) {
			logger.InfoContext(c.Request.Context(), "webhook: branch filtered",
				slog.String("repo", repo.Name), slog.String("branch", branch))
			c.JSON(http.StatusAccepted, gin.H{"status": "branch_filtered", "repo": repo.Name, "branch": branch})
			return
		}

		sha := ev.sha()
		short := sha
		if len(short) > 12 {
			short = short[:12]
		}
		build := deps.Store.Create(store.Build{
			Repo:      repo.Name,
			Branch:    branch,
			SHA:       sha,
			ShortSHA:  short,
			Status:    store.StatusQueued,
			CreatedAt: time.Now(),
		})

		job := queue.Job{
			BuildID: build.ID,
			Repo:    repo,
			Branch:  branch,
			SHA:     sha,
			Run:     pipeline(deps, build.ID, repo, branch, sha),
		}
		if err := deps.Queue.Enqueue(job); err != nil {
			deps.Store.Update(build.ID, func(b *store.Build) {
				b.Status = store.StatusFailed
				b.Error = err.Error()
				b.FinishedAt = time.Now()
			})
			logger.ErrorContext(c.Request.Context(), "webhook: enqueue failed",
				slog.String("build_id", build.ID), slog.Any("err", err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "queue_full", "build_id": build.ID})
			return
		}

		logger.InfoContext(c.Request.Context(), "webhook: build queued",
			slog.String("build_id", build.ID),
			slog.String("repo", repo.Name),
			slog.String("branch", branch),
			slog.String("sha", sha))
		c.JSON(http.StatusAccepted, gin.H{
			"status":   "queued",
			"build_id": build.ID,
			"repo":     repo.Name,
			"branch":   branch,
			"sha":      sha,
		})
	}
}

// pipeline returns the job runner: mark running, build, mark success/failed.
func pipeline(deps Deps, buildID string, repo config.Repository, branch, sha string) func(context.Context) error {
	return func(ctx context.Context) error {
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

		fmt.Fprintf(logf, "build %s repo=%s branch=%s sha=%s\n", buildID, repo.Name, branch, sha)
		tags, err := builder.Build(ctx, builder.Request{
			Repo:      repo,
			Branch:    branch,
			SHA:       sha,
			Log:       logf,
			Workspace: deps.Workspace,
		})
		if err != nil {
			deps.Store.Update(buildID, func(b *store.Build) {
				b.Status = store.StatusFailed
				b.Error = err.Error()
				b.FinishedAt = time.Now()
			})
			deps.Logger.ErrorContext(ctx, "build failed",
				slog.String("build_id", buildID), slog.Any("err", err))
			return err
		}
		deps.Store.Update(buildID, func(b *store.Build) {
			b.Status = store.StatusSuccess
			b.ImageTags = tags
			b.FinishedAt = time.Now()
		})
		deps.Logger.InfoContext(ctx, "build succeeded",
			slog.String("build_id", buildID), slog.String("tags", strings.Join(tags, ",")))
		return nil
	}
}
