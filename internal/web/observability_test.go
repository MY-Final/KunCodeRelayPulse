package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kuncode-relay-pulse/internal/config"
	"kuncode-relay-pulse/internal/prober"
	"kuncode-relay-pulse/internal/probetpl"
	"kuncode-relay-pulse/internal/reload"
	"kuncode-relay-pulse/internal/store"
)

func TestStatusExposesRawAndThresholdedStatus(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.RecordProbe(
		store.ProbeRow{ChannelID: "ch_test", Model: "model-a", Status: prober.StatusRed, SubStatus: "timeout", TS: 100},
		nil,
		store.DetectorState{ChannelID: "ch_test", Model: "model-a", ConsecutiveFailures: 1, ConsecutiveAnomalies: 1, RecoveryThreshold: 2, UpdatedAt: 100},
	); err != nil {
		t.Fatal(err)
	}

	snap := &Snapshot{
		Cfg:      &config.Config{SiteTitle: "test", EventThreshold: 3, RecoveryThreshold: 2},
		Channels: []*config.Channel{{ID: "ch_test", Provider: "kuncode", Name: "主力号池", Template: "probe", Models: []string{"model-a"}}},
		Targets:  []prober.Target{{ChannelID: "ch_test", Model: "model-a", Template: &probetpl.Template{}}},
	}
	s := New(func() *Snapshot { return snap }, st, reload.NewReady())
	body, err := s.buildStatus(false)
	if err != nil {
		t.Fatal(err)
	}
	var payload statusJSON
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	target := payload.Providers[0].Channels[0].Targets[0]
	if target.Status == nil || *target.Status != prober.StatusRed || target.CurrentStatus == nil || *target.CurrentStatus != prober.StatusYellow {
		t.Fatalf("status = %#v, want raw red/current degraded", target)
	}
	if target.Streak == nil || target.Streak.ConsecutiveAnomalies != 1 || target.Streak.FailureThreshold != 3 {
		t.Fatalf("streak = %#v", target.Streak)
	}
}

func TestStatusTrendChecksModelOwnershipAndHiddenAccess(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	if err := st.InsertProbe(store.ProbeRow{ChannelID: "ch_public", Model: "model-a", Status: prober.StatusRed, LatencyMS: 300, TS: now - 1}); err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{
		Cfg: &config.Config{SiteTitle: "test"},
		Channels: []*config.Channel{
			{ID: "ch_public", Provider: "public", Name: "公开", Models: []string{"model-a"}},
			{ID: "ch_hidden", Provider: "private", Name: "隐藏", Hidden: true, Models: []string{"model-a"}},
		},
	}
	s := New(func() *Snapshot { return snap }, st, reload.NewReady())
	h := s.Handler()
	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	if response := request("/api/status/trend?channel_id=ch_public&model=wrong&window=24h"); response.Code != http.StatusBadRequest {
		t.Fatalf("wrong model status = %d, want 400", response.Code)
	}
	if response := request("/api/status/trend?channel_id=ch_public&model=model-a&window=24h"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"bucket_seconds":900`) {
		t.Fatalf("public trend = %d/%s", response.Code, response.Body.String())
	}
	if response := request("/api/status/trend?channel_id=ch_hidden&model=model-a&window=24h"); response.Code != http.StatusNotFound {
		t.Fatalf("hidden trend without session = %d, want 404", response.Code)
	}
	s.sessions["admin-session"] = time.Now().Add(time.Hour)
	r := httptest.NewRequest(http.MethodGet, "/api/status/trend?channel_id=ch_hidden&model=model-a&window=24h", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("hidden trend with session = %d/%s", w.Code, w.Body.String())
	}
}

func TestAdminSettingsUpdatesOnlyProbeFieldsAndReloads(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	raw := []byte("interval: 300s\nprobe_timeout: 30s\nslow_latency: 5s\nevent_threshold: 2\nrecovery_threshold: 1\nadmin:\n  username: admin\n  password_hash: keep-me\nnotify:\n  webhook:\n    url: https://notify.example/hook\n")
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	snap := &Snapshot{Cfg: &config.Config{ConfigPath: configPath, Interval: config.Duration(5 * time.Minute), ProbeTimeout: config.Duration(30 * time.Second), SlowLatency: config.Duration(5 * time.Second), EventThreshold: 2, RecoveryThreshold: 1, Admin: config.AdminConfig{Username: "admin"}}}
	s := New(func() *Snapshot { return snap }, st, reload.NewReady())
	s.sessions["admin-session"] = time.Now().Add(time.Hour)
	reloaded := false
	s.SetReloadSettings(func() error { reloaded = true; return nil })
	h := s.Handler()
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	updated := request(http.MethodPut, "/api/admin/settings", []byte(`{"interval":"60s","timeout":"20s","slow_latency":"5s","failure_threshold":3,"recovery_threshold":2}`))
	if updated.Code != http.StatusOK || !reloaded {
		t.Fatalf("update = %d/%s, reloaded=%v", updated.Code, updated.Body.String(), reloaded)
	}
	written, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	for _, value := range []string{"interval: 1m0s", "probe_timeout: 20s", "event_threshold: 3", "recovery_threshold: 2", "password_hash: keep-me", "url: https://notify.example/hook"} {
		if !strings.Contains(text, value) {
			t.Fatalf("updated config missing %q: %s", value, text)
		}
	}
	invalid := request(http.MethodPut, "/api/admin/settings", []byte(`{"interval":"1s","timeout":"20s","slow_latency":"5s","failure_threshold":3,"recovery_threshold":2}`))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid settings status = %d, want 400", invalid.Code)
	}
}
