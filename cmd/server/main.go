// Command server 运行多平台代码托管 Webhook 触发的 Docker 镜像构建服务。
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"code-builder/internal/config"
	"code-builder/internal/notify"
	"code-builder/internal/queue"
	"code-builder/internal/server"
	"code-builder/internal/store"
	"code-builder/internal/webhook"
)

// version 由 Dockerfile 通过 -ldflags "-X main.version=..." 注入。
var version = "dev"

func main() {
	configPath := flag.String("config", "config/config.yaml", "path to config file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL")))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	logger.Info("starting code-builder", slog.String("version", version))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config", slog.Any("err", err))
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.Server.Workspace, 0o755); err != nil {
		logger.Error("create workspace", slog.Any("err", err))
		os.Exit(1)
	}
	providers, err := cfg.Code.ResolveProviders()
	if err != nil {
		logger.Error("resolve providers", slog.Any("err", err))
		os.Exit(1)
	}

	st := store.NewPersistent(cfg.Server.LogsDir, cfg.Server.StateFile)
	q := queue.New(cfg.Server.Concurrency, cfg.Server.QueueSize)

	// 每次 clone 可选使用的 SSH 部署密钥（不在 YAML 配置文件中）：
	// 若已配置但无法访问，则在启动时快速失败。
	deployKey := os.Getenv("DEPLOY_KEY_PATH")
	if deployKey == "" {
		deployKey = os.Getenv("GITLAB_DEPLOY_KEY_PATH")
	}
	if deployKey != "" {
		if _, err := os.Stat(deployKey); err != nil {
			logger.Error("DEPLOY_KEY_PATH is set but not accessible",
				slog.String("path", deployKey), slog.Any("err", err))
			os.Exit(1)
		}
	}

	// 队列与 HTTP 服务共享同一个可取消上下文；服务退出时先停止接收新任务，
	// 再给正在运行的构建一段收尾时间。
	runCtx, cancel := context.WithCancel(ctx)
	queueDone := make(chan struct{})
	go func() {
		q.Start(runCtx)
		close(queueDone)
	}()

	deps := webhook.Deps{
		Providers:     providers,
		Branches:      cfg.Code.Branches,
		DeployKeyPath: deployKey,
		Secret:        cfg.Code.WebhookSecret,
		Store:         st,
		Queue:         q,
		Logger:        logger,
		Workspace:     cfg.Server.Workspace,
		Registry:      cfg.Registry,
		Build:         cfg.Build,
		Timeouts:      cfg.Timeouts,
		Deploy:        cfg.Deploy,
		Notify:        notify.New(cfg.Notify, logger),
	}
	runErr := server.New(cfg, logger, deps).Run(runCtx)
	cancel()
	select {
	case <-queueDone:
	case <-time.After(30 * time.Second):
		logger.Warn("queue shutdown timed out; exiting with unfinished builds")
	}
	failUnfinished(st, "service shutdown before the build finished")
	if runErr != nil {
		logger.Error("server exited", slog.Any("err", runErr))
		os.Exit(1)
	}
}

// failUnfinished 在关停时把仍在排队/运行的构建标记为失败，避免重启后
// 持久化记录永远停留在 queued / running。
func failUnfinished(st *store.Store, reason string) {
	for _, b := range st.List() {
		if b.Status != store.StatusQueued && b.Status != store.StatusRunning {
			continue
		}
		st.Update(b.ID, func(bb *store.Build) {
			bb.Status = store.StatusFailed
			bb.Error = reason
			bb.FinishedAt = time.Now()
		})
	}
}
