package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// larkMaxAttempts 限制每条通知的投递尝试次数。
	larkMaxAttempts = 3
	// maxRespBytes 限制读取 Lark 响应正文的最大字节数。
	maxRespBytes = 64 << 10
)

// retryBase 是两次尝试之间的首次退避间隔；测试中会将其缩短。
var retryBase = time.Second

// larkPayload 是自定义机器人请求体。
type larkPayload struct {
	Timestamp string         `json:"timestamp"`
	Sign      string         `json:"sign,omitempty"`
	MsgType   string         `json:"msg_type"`
	Card      map[string]any `json:"card"`
}

// larkResponse 兼容机器人 API 使用过的两种响应格式：{"code":0}
// 和 {"StatusCode":0}。
type larkResponse struct {
	Code          int    `json:"code"`
	Msg           string `json:"msg"`
	StatusCode    int    `json:"StatusCode"`
	StatusMessage string `json:"StatusMessage"`
}

// send 将 msg 投递到配置的 Lark webhook。
func (n *Notifier) send(ctx context.Context, msg Message) error {
	if !n.Enabled() {
		return nil
	}
	ts := time.Now().Unix()
	payload := larkPayload{
		Timestamp: strconv.FormatInt(ts, 10),
		MsgType:   "interactive",
		Card:      buildCard(msg),
	}
	if secret := strings.TrimSpace(n.cfg.Lark.Secret); secret != "" {
		payload.Sign = larkSign(ts, secret)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify: encode payload: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= larkMaxAttempts; attempt++ {
		retry, err := n.post(ctx, body)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry || attempt == larkMaxAttempts {
			break
		}
		if werr := sleepCtx(ctx, time.Duration(attempt)*retryBase); werr != nil {
			return lastErr
		}
	}
	return lastErr
}

// post 执行一次尝试，并返回是否值得重试。
func (n *Notifier) post(ctx context.Context, body []byte) (retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.cfg.Lark.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("notify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return true, fmt.Errorf("notify: post %s: %w", maskWebhook(n.cfg.Lark.WebhookURL), err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return true, fmt.Errorf("notify: lark returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("notify: lark returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return false, nil
	}
	var out larkResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return false, fmt.Errorf("notify: decode lark response: %w", err)
	}
	if out.Code != 0 || out.StatusCode != 0 {
		msg := out.Msg
		if msg == "" {
			msg = out.StatusMessage
		}
		return false, fmt.Errorf("notify: lark rejected message: code=%d %s", firstNonZero(out.Code, out.StatusCode), msg)
	}
	return false, nil
}

// larkSign 复现 Jenkins/Lark 签名算法："<timestamp>\n<secret>" 字符串
// 作为 HMAC-SHA256 密钥，被签名的载荷为空。
func larkSign(timestamp int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(strconv.FormatInt(timestamp, 10)+"\n"+secret))
	mac.Write(nil)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// maskWebhook 隐藏机器人 token，使其可以安全地出现在错误信息和日志中。
func maskWebhook(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "****"
	}
	path := u.Path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		path = path[:i]
	}
	return u.Scheme + "://" + u.Host + path + "/****"
}

// sleepCtx 等待 d 时长，或直到 context 结束。
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}
