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

	deps := webhook.Deps{
		Repos:     cfg.Repositories,
		Secret:    cfg.GitLab.WebhookSecret,
		Store:     st,
		Queue:     q,
		Logger:    logger,
		Workspace: cfg.Server.Workspace,
	}
	if err := server.New(cfg, logger, deps).Run(ctx); err != nil {
		logger.Error("server exited", slog.Any("err", err))
		os.Exit(1)
	}
}
