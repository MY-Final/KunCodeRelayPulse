// Package notify 把 down/up 事件推送到外部渠道：通用 webhook + Telegram。
// 未配置任何渠道时 FromConfig 返回 nil，调用方跳过。
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"kuncode-relay-pulse/internal/config"
)

type Payload struct {
	Type     string `json:"type"` // down / up
	Provider string `json:"provider"`
	Channel  string `json:"channel"`
	Model    string `json:"model"`
	Detail   string `json:"detail"`
	TS       int64  `json:"ts"`
}

type Notifier interface {
	Notify(p Payload)
}

var httpc = &http.Client{Timeout: 10 * time.Second}

type Composite []Notifier

func (c Composite) Notify(p Payload) {
	for _, n := range c {
		n.Notify(p)
	}
}

// FromConfig 依据配置构造通知器组合。
func FromConfig(cfg config.NotifyConfig) Notifier {
	var ns []Notifier
	if cfg.Webhook != nil && cfg.Webhook.URL != "" {
		ns = append(ns, &Webhook{URL: cfg.Webhook.URL})
	}
	if cfg.Telegram != nil && cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != "" {
		ns = append(ns, &Telegram{BotToken: cfg.Telegram.BotToken, ChatID: cfg.Telegram.ChatID})
	}
	if len(ns) == 0 {
		return nil
	}
	return Composite(ns)
}

// Webhook POST JSON 载荷到自定义地址（飞书/钉钉/Server酱 的转接层都可用）。
type Webhook struct{ URL string }

func (w *Webhook) Notify(p Payload) {
	body, _ := json.Marshal(p)
	resp, err := httpc.Post(w.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[notify] webhook 发送失败: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("[notify] webhook 返回 %d", resp.StatusCode)
	}
}

type Telegram struct{ BotToken, ChatID string }

func (t *Telegram) Notify(p Payload) {
	text := fmt.Sprintf("[%s] %s / %s\n%s\n%s", p.Type, p.Provider, p.Channel, p.Model, p.Detail)
	body, _ := json.Marshal(map[string]string{
		"chat_id": t.ChatID,
		"text":    text,
	})
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.BotToken)
	resp, err := httpc.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[notify] telegram 发送失败: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("[notify] telegram 返回 %d", resp.StatusCode)
	}
}
