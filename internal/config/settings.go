package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type ProbeSettings struct {
	Interval          Duration
	Timeout           Duration
	SlowLatency       Duration
	FailureThreshold  int
	RecoveryThreshold int
}

func (c *Config) ProbeSettings() ProbeSettings {
	return ProbeSettings{
		Interval:          c.Interval,
		Timeout:           c.ProbeTimeout,
		SlowLatency:       c.SlowLatency,
		FailureThreshold:  c.EventThreshold,
		RecoveryThreshold: c.RecoveryThreshold,
	}
}

func ValidateProbeSettings(s ProbeSettings) error {
	interval := s.Interval.D()
	timeout := s.Timeout.D()
	slow := s.SlowLatency.D()
	if interval < 5*time.Second || interval > 24*time.Hour {
		return fmt.Errorf("interval 必须在 5s 到 24h 之间")
	}
	if timeout < time.Second || timeout > 10*time.Minute {
		return fmt.Errorf("probe_timeout 必须在 1s 到 10m 之间")
	}
	if slow < 0 || (slow > 0 && slow >= timeout) {
		return fmt.Errorf("slow_latency 必须为 0 或小于 probe_timeout")
	}
	if s.FailureThreshold < 1 || s.FailureThreshold > 100 {
		return fmt.Errorf("event_threshold 必须在 1 到 100 之间")
	}
	if s.RecoveryThreshold < 1 || s.RecoveryThreshold > 100 {
		return fmt.Errorf("recovery_threshold 必须在 1 到 100 之间")
	}
	return nil
}

// UpdateProbeSettings 只更新探测配置节点，保留其他配置和敏感字段。
func UpdateProbeSettings(path string, settings ProbeSettings) error {
	if err := ValidateProbeSettings(settings); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取配置: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("解析配置: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("配置根节点必须是对象")
	}
	root := doc.Content[0]
	setYAMLScalar(root, "interval", time.Duration(settings.Interval).String(), "!!str")
	setYAMLScalar(root, "probe_timeout", time.Duration(settings.Timeout).String(), "!!str")
	setYAMLScalar(root, "slow_latency", time.Duration(settings.SlowLatency).String(), "!!str")
	setYAMLScalar(root, "event_threshold", fmt.Sprintf("%d", settings.FailureThreshold), "!!int")
	setYAMLScalar(root, "recovery_threshold", fmt.Sprintf("%d", settings.RecoveryThreshold), "!!int")
	updated, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("序列化配置: %w", err)
	}
	return WriteFileAtomic(path, updated)
}

func setYAMLScalar(root *yaml.Node, key, value, tag string) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content[i+1].Value = value
			root.Content[i+1].Tag = tag
			return
		}
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value},
	)
}
