package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func TestPublicStatusFiltersHiddenEventsAndErrors(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.InsertEvent(store.Event{ChannelID: "ch_hidden", Model: "secret-model", Type: "down", TS: 200, Detail: "secret event"}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEvent(store.Event{ChannelID: "ch_public", Model: "public-model", Type: "down", TS: 100, Detail: "public event"}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertProbe(store.ProbeRow{
		ChannelID: "ch_public", Model: "public-model", Status: 0,
		TS: 100, ErrorDetail: "secret upstream response",
	}); err != nil {
		t.Fatal(err)
	}

	snap := &Snapshot{
		Cfg: &config.Config{SiteTitle: "test"},
		Channels: []*config.Channel{
			{ID: "ch_hidden", Provider: "private", Name: "Hidden", Hidden: true},
			{ID: "ch_public", Provider: "public", Name: "Public"},
		},
		Targets: []prober.Target{{
			ChannelID: "ch_public", Model: "public-model",
			Template: &probetpl.Template{},
		}},
	}
	s := New(func() *Snapshot { return snap }, st, reload.NewReady())

	publicBody, err := s.buildStatus(false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicBody), "secret") {
		t.Fatalf("public status leaked private data: %s", publicBody)
	}
	var public statusJSON
	if err := json.Unmarshal(publicBody, &public); err != nil {
		t.Fatal(err)
	}
	if len(public.Events) != 1 || public.Events[0].Channel != "Public" || public.Events[0].Detail != "" {
		t.Fatalf("public events = %#v", public.Events)
	}

	adminBody, err := s.buildStatus(true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(adminBody), "secret event") || !strings.Contains(string(adminBody), "secret upstream response") {
		t.Fatalf("admin status should retain diagnostics: %s", adminBody)
	}
}

func TestAdminChannelCRUDProtectsSecretsAndUsesRevision(t *testing.T) {
	root := t.TempDir()
	channelsDir := filepath.Join(root, "channels.d")
	templatesDir := filepath.Join(root, "templates")
	if err := os.MkdirAll(channelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templatesDir, "probe.json"), []byte(`{"name":"probe","url":"{{BASE_URL}}/health","method":"GET","timeout":"1s"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	channelYAML := `id: ch_existing
revision: 4
provider: kuncode
name: 主力号池
template: probe
base_url: https://relay.example
api_key: super-secret
models:
  - gpt-5.6-luna
`
	channelPath := filepath.Join(channelsDir, "main.yaml")
	if err := os.WriteFile(channelPath, []byte(channelYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(root, "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	snap := &Snapshot{
		Cfg: &config.Config{
			SiteTitle:    "test",
			ChannelsDir:  channelsDir,
			TemplatesDir: templatesDir,
			Admin:        config.AdminConfig{Username: "admin", PasswordHash: "unused"},
		},
		Channels: []*config.Channel{{
			ID: "ch_existing", Revision: 4, Provider: "kuncode", Name: "主力号池",
			Template: "probe", BaseURL: "https://relay.example", APIKey: "super-secret",
			Models: []string{"gpt-5.6-luna"},
		}},
		Targets: []prober.Target{{ChannelID: "ch_existing", Model: "gpt-5.6-luna"}},
	}
	s := New(func() *Snapshot { return snap }, st, reload.NewReady())
	s.sessions["test-session"] = time.Now().Add(time.Hour)
	var sinkCalls int
	s.SetManualProbe(func(_ context.Context, target prober.Target) prober.Result {
		return prober.Result{ChannelID: target.ChannelID, Model: target.Model, Status: prober.StatusGreen, HTTPCode: 200, LatencyMS: 42, TS: 123}
	}, func(_ prober.Target, _ prober.Result) {
		sinkCalls++
	})
	h := s.Handler()

	unauthorized := httptest.NewRequest(http.MethodGet, "/api/admin/channels", nil)
	unauthorizedRecorder := httptest.NewRecorder()
	h.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", unauthorizedRecorder.Code)
	}

	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	list := request(http.MethodGet, "/api/admin/channels", nil)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "super-secret") || !strings.Contains(list.Body.String(), `"api_key_set":true`) {
		t.Fatalf("admin list leaked secret or omitted key state: %s", list.Body.String())
	}

	created := request(http.MethodPost, "/api/admin/channels", []byte(`{
  "provider":"kuncode",
  "name":"备用号池",
  "interval":"15s",
  "template":"probe",
  "base_url":"https://backup.example",
  "api_key":"created-secret",
  "models":["gpt-5.6-luna","gpt-5.6"]
}`))
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), "created-secret") {
		t.Fatalf("create status/body = %d/%s", created.Code, created.Body.String())
	}
	var createdChannel adminChannelJSON
	if err := json.Unmarshal(created.Body.Bytes(), &createdChannel); err != nil {
		t.Fatal(err)
	}
	if createdChannel.ID == "" || createdChannel.Revision != 1 || !createdChannel.APIKeySet {
		t.Fatalf("created channel = %#v", createdChannel)
	}

	updatedBody := []byte(`{
  "provider":"kuncode",
  "name":"备用号池改名",
  "interval":"30s",
  "template":"probe",
  "base_url":"https://backup.example/v2",
  "models":["gpt-5.6-luna"],
  "revision":1
}`)
	updated := request(http.MethodPut, "/api/admin/channels/"+createdChannel.ID, updatedBody)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"revision":2`) || !strings.Contains(updated.Body.String(), `"api_key_set":true`) {
		t.Fatalf("update status/body = %d/%s", updated.Code, updated.Body.String())
	}
	stale := request(http.MethodPut, "/api/admin/channels/"+createdChannel.ID, updatedBody)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale update status = %d, want 409", stale.Code)
	}

	probe := request(http.MethodPost, "/api/admin/channels/ch_existing/probe", []byte(`{"model":"gpt-5.6-luna"}`))
	if probe.Code != http.StatusOK || !strings.Contains(probe.Body.String(), `"latency_ms":42`) || sinkCalls != 1 {
		t.Fatalf("probe status/body/calls = %d/%s/%d", probe.Code, probe.Body.String(), sinkCalls)
	}

	deleted := request(http.MethodDelete, "/api/admin/channels/"+createdChannel.ID, []byte(`{"revision":2}`))
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status/body = %d/%s", deleted.Code, deleted.Body.String())
	}
	if _, err := os.Stat(filepath.Join(channelsDir, createdChannel.ID+".yaml")); !os.IsNotExist(err) {
		t.Fatalf("deleted channel file still exists, err=%v", err)
	}
	archiveEntries, err := os.ReadDir(filepath.Join(channelsDir, ".archive"))
	if err != nil {
		t.Fatal(err)
	}
	if len(archiveEntries) != 1 {
		t.Fatalf("archive entries = %d, want 1", len(archiveEntries))
	}
}

func TestAdminProxyCRUDMasksURLAndProtectsReferences(t *testing.T) {
	root := t.TempDir()
	channelsDir := filepath.Join(root, "channels.d")
	templatesDir := filepath.Join(root, "templates")
	proxiesDir := filepath.Join(root, "proxies.d")
	for _, dir := range []string{channelsDir, templatesDir, proxiesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	st, err := store.Open(filepath.Join(root, "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	snap := &Snapshot{
		Cfg: &config.Config{
			SiteTitle:    "test",
			ChannelsDir:  channelsDir,
			TemplatesDir: templatesDir,
			ProxiesDir:   proxiesDir,
			Admin:        config.AdminConfig{Username: "admin", PasswordHash: "unused"},
		},
	}
	s := New(func() *Snapshot { return snap }, st, reload.NewReady())
	s.sessions["test-session"] = time.Now().Add(time.Hour)
	h := s.Handler()
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	created := request(http.MethodPost, "/api/admin/proxies", []byte(`{
  "name":"海外出口",
  "url":"socks5://alice:secret@127.0.0.1:1080"
}`))
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), "secret") {
		t.Fatalf("create status/body = %d/%s", created.Code, created.Body.String())
	}
	var proxy adminProxyJSON
	if err := json.Unmarshal(created.Body.Bytes(), &proxy); err != nil {
		t.Fatal(err)
	}
	if proxy.ID == "" || proxy.Revision != 1 || proxy.URLPreview != "socks5://alice:***@127.0.0.1:1080" || !proxy.URLSet || !proxy.HasAuth {
		t.Fatalf("created proxy = %#v", proxy)
	}

	list := request(http.MethodGet, "/api/admin/proxies", nil)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "secret") || !strings.Contains(list.Body.String(), proxy.ID) {
		t.Fatalf("list status/body = %d/%s", list.Code, list.Body.String())
	}

	updated := request(http.MethodPut, "/api/admin/proxies/"+proxy.ID, []byte(`{
  "name":"海外出口改名",
  "revision":1
}`))
	if updated.Code != http.StatusOK || strings.Contains(updated.Body.String(), "secret") || !strings.Contains(updated.Body.String(), `"revision":2`) {
		t.Fatalf("update status/body = %d/%s", updated.Code, updated.Body.String())
	}

	channelPath := filepath.Join(channelsDir, "reference.yaml")
	channelYAML := fmt.Sprintf(`id: ch_reference
revision: 1
provider: kuncode
name: 引用代理的渠道
template: probe
base_url: https://relay.example
proxy: %s
`, proxy.ID)
	if err := os.WriteFile(channelPath, []byte(channelYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := request(http.MethodDelete, "/api/admin/proxies/"+proxy.ID, []byte(`{"revision":2}`))
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "仍被渠道引用") {
		t.Fatalf("referenced proxy delete = %d/%s", blocked.Code, blocked.Body.String())
	}

	if err := os.Remove(channelPath); err != nil {
		t.Fatal(err)
	}
	deleted := request(http.MethodDelete, "/api/admin/proxies/"+proxy.ID, []byte(`{"revision":2}`))
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status/body = %d/%s", deleted.Code, deleted.Body.String())
	}
	archiveEntries, err := os.ReadDir(filepath.Join(proxiesDir, ".archive"))
	if err != nil {
		t.Fatal(err)
	}
	if len(archiveEntries) != 1 {
		t.Fatalf("proxy archive entries = %d, want 1", len(archiveEntries))
	}
}

func TestAdminResetChannelClearsStoredState(t *testing.T) {
	root := t.TempDir()
	channelsDir := filepath.Join(root, "channels.d")
	if err := os.MkdirAll(channelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	channelYAML := `id: ch_reset
revision: 7
provider: kuncode
name: 可重置渠道
template: probe
base_url: https://relay.example
`
	if err := os.WriteFile(filepath.Join(channelsDir, "reset.yaml"), []byte(channelYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(root, "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.RecordProbe(
		store.ProbeRow{ChannelID: "ch_reset", Model: "gpt-5.6-luna", Status: 0, TS: 100},
		[]store.Event{{ChannelID: "ch_reset", Model: "gpt-5.6-luna", Type: "down", TS: 100}},
		store.DetectorState{ChannelID: "ch_reset", Model: "gpt-5.6-luna", ConsecutiveFailures: 2, Down: true, UpdatedAt: 100},
	); err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{
		Cfg: &config.Config{
			ChannelsDir: channelsDir,
			Admin:       config.AdminConfig{Username: "admin", PasswordHash: "unused"},
		},
	}
	s := New(func() *Snapshot { return snap }, st, reload.NewReady())
	s.sessions["test-session"] = time.Now().Add(time.Hour)
	s.SetResetChannel(st.ResetChannel)
	h := s.Handler()
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	reset := request(http.MethodPost, "/api/admin/channels/ch_reset/reset", []byte(`{"revision":7}`))
	if reset.Code != http.StatusOK || !strings.Contains(reset.Body.String(), `"deleted_records":3`) {
		t.Fatalf("reset status/body = %d/%s", reset.Code, reset.Body.String())
	}
	latest, err := st.Latest("ch_reset", "gpt-5.6-luna")
	if err != nil {
		t.Fatal(err)
	}
	if latest != nil {
		t.Fatalf("latest probe after reset = %#v, want nil", latest)
	}
	stale := request(http.MethodPost, "/api/admin/channels/ch_reset/reset", []byte(`{"revision":6}`))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale reset status = %d, want 409", stale.Code)
	}
}

func TestAdminProxyTestReturnsBusinessResult(t *testing.T) {
	root := t.TempDir()
	channelsDir := filepath.Join(root, "channels.d")
	proxiesDir := filepath.Join(root, "proxies.d")
	for _, dir := range []string{channelsDir, proxiesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(proxiesDir, "local.yaml"), []byte(`id: px_local
revision: 1
name: 不可用代理
url: http://127.0.0.1:1
`), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(root, "pulse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	snap := &Snapshot{
		Cfg: &config.Config{
			ChannelsDir: channelsDir,
			ProxiesDir:  proxiesDir,
			Admin:       config.AdminConfig{Username: "admin", PasswordHash: "unused"},
		},
	}
	s := New(func() *Snapshot { return snap }, st, reload.NewReady())
	s.sessions["test-session"] = time.Now().Add(time.Hour)
	h := s.Handler()
	r := httptest.NewRequest(http.MethodPost, "/api/admin/proxies/px_local/test", bytes.NewReader([]byte(`{}`)))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("proxy test status/body = %d/%s", w.Code, w.Body.String())
	}
}
