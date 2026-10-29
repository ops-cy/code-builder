// Package notify 将流水线事件渲染为 Lark 卡片并完成投递。
package notify

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"code-builder/internal/config"
)

// Event 是触发通知的流水线事件。
type Event string

// 流水线事件。
const (
	EventStarted Event = "started"
	EventSuccess Event = "success"
	EventFailure Event = "failure"
)

// Message 是渲染为一条通知的数据。
type Message struct {
	Event    Event
	App      string
	Actor    string
	Branch   string
	ShortSHA string
	Image    string // "<仓库>:<tag>"
	Duration time.Duration
	Subject  string
	Err      string // 构建失败摘要（成功时为空）
	LogTail  string // 最后几行日志，仅失败卡片使用
	// Deploy* 字段在成功卡片上报告构建后的部署结果。
	DeployStatus string // 取值：DeployDeployed / DeploySkipped / DeployFailed
	DeployReason string // 跳过部署的原因
	DeployTarget string // "<目录>/<compose 文件>"
	DeployError  string
	DeployOutput string // 失败后 docker compose ps 的输出
	StartedAt    time.Time
	Now          time.Time
}

// Sender 负责投递通知。实现必须可安全并发使用；Wants 决定某个事件
// 是否投递。
type Sender interface {
	Wants(ev Event) bool
	Async(msg Message, onErr func(error))
}

// Notifier 将流水线通知发送到 Lark（飞书）自定义机器人。
type Notifier struct {
	cfg     config.NotifyConfig
	client  *http.Client
	logger  *slog.Logger
	timeout time.Duration
}

// New 创建一个 Notifier。未设置投递超时时默认回退为 10 秒。
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

// Enabled 报告通知是否已开启且已配置 webhook。
func (n *Notifier) Enabled() bool { return n != nil && n.cfg.LarkEnabled() }

// Wants 报告是否应投递事件 ev。
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

// Async 在后台投递 msg。onErr（可选）接收失败信息，使调用方可以
// 记录失败，而不会阻塞或导致构建失败。
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
