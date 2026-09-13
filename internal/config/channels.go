package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Channel 是 channels.d/ 里一个 yaml 文件对应的通道。
// 文件即一条中转线路：同一个 provider + 一个探针模板 + 一个 base_url + 一组模型。
type Channel struct {
	ID        string   `yaml:"id"`       // 稳定 ID ch_<uuid>，首次加载自动生成并写回文件
	Revision  int      `yaml:"revision"` // 管理后台乐观锁版本
	Provider  string   `yaml:"provider"`
	Name      string   `yaml:"name"` // 展示名，随便改，不影响历史
	Hidden    bool     `yaml:"hidden"`
	Disabled  bool     `yaml:"disabled"`
	Interval  Duration `yaml:"interval"` // 为 0 时用全局 interval
	Template  string   `yaml:"template"`
	BaseURL   string   `yaml:"base_url"`
	Proxy     string   `yaml:"proxy"`       // 代理 ID；为空表示直连（或沿用环境代理）
	APIKey    string   `yaml:"api_key"`     // 明文（本地自用可接受）
	APIKeyEnv string   `yaml:"api_key_env"` // 从环境变量读，优先级高于 api_key
	Models    []string `yaml:"models"`      // 每个模型是一个探测目标；留空则整通道一个目标（model 为空串）
}

// NewID 生成 prefix_<uuid v4>。
func NewID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 失败等于系统级故障
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s_%x-%x-%x-%x-%x", prefix, b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// WriteFileAtomic 临时文件 + rename，崩溃不会留半个文件。
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后 no-op
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// LoadChannels 读取目录下全部 *.yaml，校验并给缺 ID 的通道生成稳定 ID 后写回。
// 返回按 (provider, name) 排好的切片。
func LoadChannels(dir string) ([]*Channel, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取通道目录 %s: %w", dir, err)
	}
	var out []*Channel
	seenKey := map[string]string{} // provider/name -> 文件名
	seenID := map[string]string{}  // id -> 文件名
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		var ch Channel
		if err := yaml.Unmarshal(raw, &ch); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if ch.Provider == "" || ch.Name == "" {
			return nil, fmt.Errorf("%s: provider 和 name 必填", e.Name())
		}
		if ch.Template == "" {
			return nil, fmt.Errorf("%s: template 必填（templates/ 里的模板名）", e.Name())
		}
		if !strings.HasPrefix(ch.BaseURL, "http://") && !strings.HasPrefix(ch.BaseURL, "https://") {
			return nil, fmt.Errorf("%s: base_url 必须是 http(s) URL", e.Name())
		}
		key := ch.Provider + "/" + ch.Name
		if prev, ok := seenKey[key]; ok {
			return nil, fmt.Errorf("%s: provider/name 与 %s 重复", e.Name(), prev)
		}
		seenKey[key] = e.Name()

		if ch.ID == "" {
			ch.ID = NewID("ch")
			// 写回稳定 ID；只在这一步重写文件，避免覆盖用户手写的注释以外的内容
			if data, err := yaml.Marshal(&ch); err == nil {
				if err := WriteFileAtomic(path, data); err != nil {
					return nil, fmt.Errorf("%s: 写回稳定 ID 失败: %w", e.Name(), err)
				}
			}
		}
		if prev, ok := seenID[ch.ID]; ok {
			return nil, fmt.Errorf("%s: id 与 %s 重复", e.Name(), prev)
		}
		seenID[ch.ID] = e.Name()
		out = append(out, &ch)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// APIKey 运行时解析密钥：api_key_env 优先。
func (c *Channel) APIKeyResolved() string {
	if c.APIKeyEnv != "" {
		return os.Getenv(c.APIKeyEnv)
	}
	return c.APIKey
}
