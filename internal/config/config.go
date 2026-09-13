// Package config 负责三层配置的加载与校验：
// config.yaml（全局）、channels.d/*.yaml（每通道一文件）、templates/*.json（探针模板）。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration 支持 "90s"/"5m" 这类字符串和纯秒数整数。
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	s := strings.TrimSpace(value.Value)
	if s == "" {
		*d = 0
		return nil
	}
	if n, err := time.ParseDuration(s); err == nil {
		*d = Duration(n)
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		// 兼容旧版本 yaml.Marshal 直接写出的纳秒整数。
		if n >= int64(time.Second) && n%int64(time.Second) == 0 {
			*d = Duration(time.Duration(n))
			return nil
		}
		const maxSeconds = int64(1<<63-1) / int64(time.Second)
		if n > maxSeconds || n < -maxSeconds {
			return fmt.Errorf("时长整数 %q 超出范围", s)
		}
		*d = Duration(time.Duration(n) * time.Second)
		return nil
	}
	return fmt.Errorf("无法解析时长 %q（支持 90s / 5m / 秒数）", s)
}

// MarshalYAML 写成可读的 duration 字符串，避免底层纳秒值被误解为秒数。
func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

func (d Duration) D() time.Duration { return time.Duration(d) }

type AdminConfig struct {
	Username     string   `yaml:"username"`
	PasswordHash string   `yaml:"password_hash"` // bcrypt，用 `pulse hashpass <密码>` 生成
	SessionTTL   Duration `yaml:"session_ttl"`
}

type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
}

type WebhookConfig struct {
	URL string `yaml:"url"`
}

type NotifyConfig struct {
	Telegram *TelegramConfig `yaml:"telegram"`
	Webhook  *WebhookConfig  `yaml:"webhook"`
}

type Config struct {
	Listen         string       `yaml:"listen"`
	SiteTitle      string       `yaml:"site_title"`
	SQLitePath     string       `yaml:"sqlite_path"`
	ChannelsDir    string       `yaml:"channels_dir"`
	ProxiesDir     string       `yaml:"proxies_dir"`
	TemplatesDir   string       `yaml:"templates_dir"`
	Interval       Duration     `yaml:"interval"` // 全局默认探测间隔
	MaxConcurrency int          `yaml:"max_concurrency"`
	EventThreshold int          `yaml:"event_threshold"` // 连续 N 次失败判 down，默认 2
	RetentionDays  int          `yaml:"retention_days"`  // probe_log 保留天数，默认 90
	Admin          AdminConfig  `yaml:"admin"`
	Notify         NotifyConfig `yaml:"notify"`
}

// Load 读取并校验全局配置，返回的路径统一转为绝对路径（相对 config.yaml 所在目录解析）。
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析 config.yaml: %w", err)
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	resolve := func(p string) (string, error) {
		if p == "" {
			return "", fmt.Errorf("必填项缺失")
		}
		if filepath.IsAbs(p) {
			return p, nil
		}
		return filepath.Join(base, p), nil
	}

	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8080"
	}
	if cfg.SiteTitle == "" {
		cfg.SiteTitle = "KunCodeRelayPulse"
	}
	if cfg.SQLitePath, err = resolve(cfg.SQLitePath); err != nil {
		return nil, fmt.Errorf("sqlite_path: %w", err)
	}
	if cfg.ChannelsDir, err = resolve(cfg.ChannelsDir); err != nil {
		return nil, fmt.Errorf("channels_dir: %w", err)
	}
	if cfg.ProxiesDir == "" {
		cfg.ProxiesDir = "./proxies.d"
	}
	if cfg.ProxiesDir, err = resolve(cfg.ProxiesDir); err != nil {
		return nil, fmt.Errorf("proxies_dir: %w", err)
	}
	if cfg.TemplatesDir, err = resolve(cfg.TemplatesDir); err != nil {
		return nil, fmt.Errorf("templates_dir: %w", err)
	}
	if cfg.Interval.D() <= 0 {
		cfg.Interval = Duration(5 * time.Minute)
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 8
	}
	if cfg.EventThreshold <= 0 {
		cfg.EventThreshold = 2
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 90
	}
	if cfg.Admin.SessionTTL.D() <= 0 {
		cfg.Admin.SessionTTL = Duration(7 * 24 * time.Hour)
	}
	if cfg.Admin.Username == "" || cfg.Admin.PasswordHash == "" {
		return nil, fmt.Errorf("admin.username / admin.password_hash 未配置（用 `pulse hashpass <密码>` 生成哈希）")
	}
	return &cfg, nil
}
