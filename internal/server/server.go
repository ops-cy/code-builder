// Package server wires the HTTP API: webhook ingestion, build queries, and
// health checks.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"gitlab-builder/internal/config"
	"gitlab-builder/internal/store"
	"gitlab-builder/internal/webhook"

	"github.com/gin-gonic/gin"
)

// Server owns the HTTP listener and its lifecycle.
type Server struct {
	cfg    config.Config
	srv    *http.Server
	router *gin.Engine
	logger *slog.Logger
}

// New builds the router, mounts routes, and returns a ready Server.
func New(cfg config.Config, logger *slog.Logger, deps webhook.Deps) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(RequestID(), Recovery(logger), RequestLogger(logger))

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.POST("/webhook", webhook.Handler(deps))

	api := r.Group("/api")
	{
		api.GET("/builds", func(c *gin.Context) {
			c.JSON(http.StatusOK, deps.Store.List())
		})
		api.GET("/builds/:id", func(c *gin.Context) {
			b, ok := deps.Store.Get(c.Param("id"))
			if !ok {
				c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
				return
			}
			c.JSON(http.StatusOK, b)
		})
		api.GET("/builds/:id/log", func(c *gin.Context) {
			content, err := deps.Store.ReadLog(c.Param("id"))
			if err != nil {
				if errors.Is(err, store.ErrNotExist) || strings.Contains(err.Error(), "unknown build") {
					c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "read_log_failed"})
				return
			}
			c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(content))
		})
	}

	return &Server{
		cfg:    cfg,
		router: r,
		logger: logger,
		srv: &http.Server{
			Addr:              cfg.Server.Listen,
			Handler:           r,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      5 * time.Minute, // build log downloads can be large
			IdleTimeout:       60 * time.Second,
		},
	}
}

// Handler exposes the gin engine for tests.
func (s *Server) Handler() http.Handler { return s.router }

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.InfoContext(ctx, "server starting", slog.String("addr", s.srv.Addr))
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		s.logger.InfoContext(ctx, "shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
