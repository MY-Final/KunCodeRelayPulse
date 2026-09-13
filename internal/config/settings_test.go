package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateProbeSettingsPreservesUnrelatedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte("interval: 300s\nadmin:\n  username: admin\n  password_hash: secret-hash\nnotify:\n  telegram:\n    bot_token: real-token\n")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	settings := ProbeSettings{
		Interval: Duration(time.Minute), Timeout: Duration(30 * time.Second),
		SlowLatency: Duration(5 * time.Second), FailureThreshold: 3, RecoveryThreshold: 2,
	}
	if err := UpdateProbeSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	for _, value := range []string{"interval: 1m0s", "probe_timeout: 30s", "slow_latency: 5s", "event_threshold: 3", "recovery_threshold: 2", "password_hash: secret-hash", "bot_token: real-token"} {
		if !strings.Contains(text, value) {
			t.Fatalf("config missing %q after update: %s", value, text)
		}
	}
}

func TestValidateProbeSettingsBounds(t *testing.T) {
	valid := ProbeSettings{Interval: Duration(5 * time.Second), Timeout: Duration(time.Second), FailureThreshold: 1, RecoveryThreshold: 100}
	if err := ValidateProbeSettings(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Interval = Duration(4 * time.Second)
	if err := ValidateProbeSettings(invalid); err == nil {
		t.Fatal("interval below 5s should fail")
	}
	invalid = valid
	invalid.SlowLatency = Duration(time.Second)
	if err := ValidateProbeSettings(invalid); err == nil {
		t.Fatal("slow latency equal to timeout should fail")
	}
}
