package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Proxy 是 proxies.d/ 中的一条可复用代理配置。
// URL 允许包含 user:password@，管理 API 不会把它明文返回。
type Proxy struct {
	ID       string `yaml:"id"`
	Revision int    `yaml:"revision"`
	Name     string `yaml:"name"`
	URL      string `yaml:"url"`
}

// ValidateProxyURL 校验探测器支持的代理协议。
func ValidateProxyURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("代理 URL 无效")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("代理协议不支持：%s（支持 http、https、socks5、socks5h）", u.Scheme)
	}
	if u.User != nil && u.User.Username() == "" {
		return fmt.Errorf("代理用户名不能为空")
	}
	return nil
}

// LoadProxies 读取目录下全部代理配置。目录不存在时按空代理池处理。
func LoadProxies(dir string) ([]*Proxy, error) {
	if strings.TrimSpace(dir) == "" {
		return []*Proxy{}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Proxy{}, nil
		}
		return nil, fmt.Errorf("读取代理目录 %s: %w", dir, err)
	}
	var out []*Proxy
	seenID := map[string]string{}
	seenName := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		var p Proxy
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		p.Name = strings.TrimSpace(p.Name)
		p.URL = strings.TrimSpace(p.URL)
		if p.Name == "" {
			return nil, fmt.Errorf("%s: name 必填", entry.Name())
		}
		if err := ValidateProxyURL(p.URL); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		if p.ID == "" {
			p.ID = NewID("px")
			data, marshalErr := yaml.Marshal(&p)
			if marshalErr != nil {
				return nil, fmt.Errorf("%s: 写回稳定 ID 失败: %w", entry.Name(), marshalErr)
			}
			if err := WriteFileAtomic(path, data); err != nil {
				return nil, fmt.Errorf("%s: 写回稳定 ID 失败: %w", entry.Name(), err)
			}
		}
		if prev, ok := seenID[p.ID]; ok {
			return nil, fmt.Errorf("%s: id 与 %s 重复", entry.Name(), prev)
		}
		if prev, ok := seenName[p.Name]; ok {
			return nil, fmt.Errorf("%s: name 与 %s 重复", entry.Name(), prev)
		}
		seenID[p.ID] = entry.Name()
		seenName[p.Name] = entry.Name()
		out = append(out, &p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
