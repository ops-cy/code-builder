// Command server runs the GitLab webhook image builder service.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"gitlab-builder/internal/config"
	"gitlab-builder/internal/notify"
	"gitlab-builder/internal/queue"
	"gitlab-builder/internal/server"
	"gitlab-builder/internal/store"
	"gitlab-builder/internal/webhook"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "path to config file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL")))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config", slog.Any("err", err))
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.Server.Workspace, 0o755); err != nil {
		logger.Error("create workspace", slog.Any("err", err))
		os.Exit(1)
	}

	st := store.New(cfg.Server.LogsDir)
	q := queue.New(cfg.Server.Concurrency, cfg.Server.QueueSize)
	go q.Start(ctx)

	// Optional SSH deploy key for every clone (not part of the YAML file):
	// fail fast at boot if configured but inaccessible.
	deployKey := os.Getenv("GITLAB_DEPLOY_KEY_PATH")
	if deployKey != "" {
		if _, err := os.Stat(deployKey); err != nil {
			logger.Error("GITLAB_DEPLOY_KEY_PATH is set but not accessible",
				slog.String("path", deployKey), slog.Any("err", err))
			os.Exit(1)
		}
	}

	deps := webhook.Deps{
		Domain:        cfg.Code.Domain,
		Branches:      cfg.Code.Branches,
		DeployKeyPath: deployKey,
		Secret:        cfg.Code.WebhookSecret,
		Store:         st,
		Queue:         q,
		Logger:        logger,
		Workspace:     cfg.Server.Workspace,
		Registry:      cfg.Registry,
		Build:         cfg.Build,
		Deploy:        cfg.Deploy,
		Notify:        notify.New(cfg.Notify, logger),
	}
	if err := server.New(cfg, logger, deps).Run(ctx); err != nil {
		logger.Error("server exited", slog.Any("err", err))
		os.Exit(1)
	}
}
