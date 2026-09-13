package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDurationYAMLRoundTrip(t *testing.T) {
	type configDoc struct {
		Interval Duration `yaml:"interval"`
	}

	raw, err := yaml.Marshal(configDoc{Interval: Duration(5 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "interval: 5m0s") {
		t.Fatalf("duration should be serialized as a readable string, got %q", raw)
	}

	var got configDoc
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Interval.D() != 5*time.Minute {
		t.Fatalf("round trip duration = %s, want 5m0s", got.Interval.D())
	}
}

func TestLoadChannelsAcceptsLegacyNanosecondInterval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "channel.yaml")
	raw := []byte("provider: test\nname: legacy\ntemplate: selfcheck\nbase_url: http://127.0.0.1\ninterval: 300000000000\n")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	channels, err := LoadChannels(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 || channels[0].Interval.D() != 5*time.Minute {
		t.Fatalf("legacy interval = %s, want 5m0s", channels[0].Interval.D())
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "interval: 5m0s") {
		t.Fatalf("rewritten channel should use duration string, got %q", written)
	}
}

func TestLoadProxiesGeneratesIDAndValidatesURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overseas.yaml")
	if err := os.WriteFile(path, []byte("name: overseas\nurl: socks5://user:pass@127.0.0.1:1080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proxies, err := LoadProxies(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 1 || proxies[0].ID == "" || proxies[0].Name != "overseas" {
		t.Fatalf("proxies = %#v", proxies)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "id: px_") {
		t.Fatalf("proxy ID should be persisted, got %q", written)
	}
	for _, raw := range []string{"http://127.0.0.1:8080", "https://proxy.example:8443", "socks5://127.0.0.1:1080", "socks5h://127.0.0.1:1080"} {
		if err := ValidateProxyURL(raw); err != nil {
			t.Fatalf("ValidateProxyURL(%q) = %v", raw, err)
		}
	}
	if err := ValidateProxyURL("ftp://proxy.example:21"); err == nil {
		t.Fatal("unsupported proxy scheme should fail")
	}
}
