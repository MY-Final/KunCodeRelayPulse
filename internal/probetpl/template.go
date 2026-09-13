// Package probetpl 定义探针模板：一次探测的 HTTP 形态 + 成功判定 + 阈值。
// 模板里用 {{BASE_URL}}/{{API_KEY}}/{{MODEL}} 占位，运行时注入。
package probetpl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Template struct {
	Name            string            `json:"name"`
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	SuccessContains string            `json:"success_contains"`
	Timeout         string            `json:"timeout"`       // 如 "30s"
	SlowLatency     string            `json:"slow_latency"`  // 如 "5s"，超过则黄
	Retry           int               `json:"retry"`         // 失败后额外重试次数
}

// TimeoutD 解析超时，默认 30s。
func (t *Template) TimeoutD() time.Duration {
	if d, err := time.ParseDuration(t.Timeout); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

// SlowD 解析慢阈值，0 表示不判黄。
func (t *Template) SlowD() time.Duration {
	if d, err := time.ParseDuration(t.SlowLatency); err == nil {
		return d
	}
	return 0
}

// LoadTemplates 读取目录下全部 *.json，key 为模板 name（要求与文件名一致）。
func LoadTemplates(dir string) (map[string]*Template, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取模板目录 %s: %w", dir, err)
	}
	out := map[string]*Template{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		var t Template
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if t.Name == "" {
			return nil, fmt.Errorf("%s: 缺少 name 字段", e.Name())
		}
		if t.URL == "" {
			return nil, fmt.Errorf("%s: 缺少 url 字段", e.Name())
		}
		if t.Method == "" {
			t.Method = "POST"
		}
		if out[t.Name] != nil {
			return nil, fmt.Errorf("模板 name 重复: %s", t.Name)
		}
		out[t.Name] = &t
	}
	return out, nil
}

// Render 把占位符替换为实际值，返回 (url, headers, body)。
func (t *Template) Render(baseURL, apiKey, model string) (string, map[string]string, string) {
	replace := func(s string) string {
		s = strings.ReplaceAll(s, "{{BASE_URL}}", strings.TrimRight(baseURL, "/"))
		s = strings.ReplaceAll(s, "{{API_KEY}}", apiKey)
		s = strings.ReplaceAll(s, "{{MODEL}}", model)
		return s
	}
	headers := make(map[string]string, len(t.Headers))
	for k, v := range t.Headers {
		headers[replace(k)] = replace(v)
	}
	return replace(t.URL), headers, replace(t.Body)
}
