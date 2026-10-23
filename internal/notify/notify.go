// Package notify renders pipeline events as Lark cards and delivers them.
package notify

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"gitlab-builder/internal/config"
)

// Event is the pipeline event that triggers a notification.
type Event string

// Pipeline events.
const (
	EventStarted Event = "started"
	EventSuccess Event = "success"
	EventFailure Event = "failure"
)

// Message is the data rendered into one notification.
type Message struct {
	Event     Event
	App       string
	Actor     string
	Branch    string
	ShortSHA  string
	Duration  time.Duration
	Subject   string
	Err       string // build failure summary (empty on success)
	DeployErr string // deploy failure summary (the build itself succeeded)
	Now       time.Time
}

// Sender delivers notifications. Implementations must be safe for concurrent
// use; Wants decides whether an event is delivered at all.
type Sender interface {
	Wants(ev Event) bool
	Async(msg Message, onErr func(error))
}

// Notifier sends pipeline notifications to a Lark (Feishu) custom bot.
type Notifier struct {
	cfg     config.NotifyConfig
	client  *http.Client
	logger  *slog.Logger
	timeout time.Duration
}

// New builds a Notifier. The delivery timeout falls back to 10s when unset.
func New(cfg config.NotifyConfig, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	timeout := 10 * time.Second
	if d, err := time.ParseDuration(cfg.Lark.Timeout); err == nil && d > 0 {
		timeout = d
	}
	return &Notifier{
		cfg:     cfg,
		client:  &http.Client{Timeout: timeout},
		logger:  logger,
		timeout: timeout,
	}
}

// Enabled reports whether notifications are on and a webhook is configured.
func (n *Notifier) Enabled() bool { return n != nil && n.cfg.LarkEnabled() }

// Wants reports whether ev should be delivered.
func (n *Notifier) Wants(ev Event) bool {
	if !n.Enabled() {
		return false
	}
	switch ev {
	case EventStarted:
		return n.cfg.Started()
	case EventSuccess:
		return n.cfg.Success()
	case EventFailure:
		return n.cfg.Failure()
	}
	return false
}

// Async delivers msg in the background. onErr (optional) receives failures so
// callers can record them without ever blocking or failing the build.
func (n *Notifier) Async(msg Message, onErr func(error)) {
	if !n.Wants(msg.Event) {
		return
	}
	if msg.Now.IsZero() {
		msg.Now = time.Now()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), n.timeout)
		defer cancel()
		if err := n.send(ctx, msg); err != nil {
			n.logger.Warn("notify: delivery failed",
				slog.String("event", string(msg.Event)),
				slog.String("app", msg.App),
				slog.Any("err", err))
			if onErr != nil {
				onErr(err)
			}
			return
		}
		n.logger.Info("notify: delivered",
			slog.String("event", string(msg.Event)),
			slog.String("app", msg.App))
	}()
}
