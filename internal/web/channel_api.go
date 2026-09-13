package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"kuncode-relay-pulse/internal/config"
	"kuncode-relay-pulse/internal/prober"
	"kuncode-relay-pulse/internal/probetpl"
)

// ProbeRunner 和 ResultSink 让 Web 层不需要知道探测落库与通知的实现细节。
type ProbeRunner func(context.Context, prober.Target) prober.Result
type ResultSink func(prober.Target, prober.Result)
type ResetChannel func(string) (int64, error)

type adminChannelJSON struct {
	ID        string   `json:"id"`
	Provider  string   `json:"provider"`
	Name      string   `json:"name"`
	Hidden    bool     `json:"hidden"`
	Disabled  bool     `json:"disabled"`
	Interval  string   `json:"interval"`
	Template  string   `json:"template"`
	BaseURL   string   `json:"base_url"`
	Proxy     string   `json:"proxy,omitempty"`
	ProxyName string   `json:"proxy_name,omitempty"`
	APIKeyEnv string   `json:"api_key_env,omitempty"`
	APIKeySet bool     `json:"api_key_set"`
	Models    []string `json:"models"`
	Revision  int      `json:"revision"`
}

type adminChannelsJSON struct {
	Channels  []adminChannelJSON `json:"channels"`
	Templates []string           `json:"templates"`
	Proxies   []adminProxyJSON   `json:"proxies"`
}

type channelRequest struct {
	Provider    string   `json:"provider"`
	Name        string   `json:"name"`
	Hidden      bool     `json:"hidden"`
	Disabled    bool     `json:"disabled"`
	Interval    string   `json:"interval"`
	Template    string   `json:"template"`
	BaseURL     string   `json:"base_url"`
	Proxy       string   `json:"proxy"`
	APIKey      *string  `json:"api_key"`
	APIKeyEnv   string   `json:"api_key_env"`
	Models      []string `json:"models"`
	Revision    *int     `json:"revision"`
	ClearAPIKey bool     `json:"clear_api_key"`
}

type revisionRequest struct {
	Revision *int `json:"revision"`
}

type probeRequest struct {
	Model string `json:"model"`
}

type probeResultJSON struct {
	ChannelID string `json:"channel_id"`
	Model     string `json:"model"`
	Status    int    `json:"status"`
	SubStatus string `json:"sub_status,omitempty"`
	HTTPCode  int    `json:"http_code,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
	TS        int64  `json:"ts"`
}

func (s *Server) serveAdminChannels(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, false) {
		return
	}
	snap := s.getState()
	if snap == nil || snap.Cfg == nil {
		writeJSONError(w, http.StatusInternalServerError, "服务尚未加载配置")
		return
	}
	channels, err := config.LoadChannels(snap.Cfg.ChannelsDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取渠道配置失败")
		return
	}
	proxies, err := loadConfiguredProxies(snap.Cfg.ProxiesDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取代理配置失败")
		return
	}
	templates, err := probetpl.LoadTemplates(snap.Cfg.TemplatesDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取探测模板失败")
		return
	}
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	sort.Strings(names)
	result := adminChannelsJSON{Channels: make([]adminChannelJSON, 0, len(channels)), Templates: names, Proxies: make([]adminProxyJSON, 0, len(proxies))}
	proxyNames := make(map[string]string, len(proxies))
	for _, proxy := range proxies {
		result.Proxies = append(result.Proxies, toAdminProxyJSON(proxy))
		proxyNames[proxy.ID] = proxy.Name
	}
	for _, ch := range channels {
		dto := toAdminChannelJSON(ch)
		dto.ProxyName = proxyNames[ch.Proxy]
		result.Channels = append(result.Channels, dto)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) createAdminChannel(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, true) {
		return
	}
	var req channelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()

	snap := s.getState()
	if snap == nil || snap.Cfg == nil {
		writeJSONError(w, http.StatusInternalServerError, "服务尚未加载配置")
		return
	}
	channels, err := config.LoadChannels(snap.Cfg.ChannelsDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取渠道配置失败")
		return
	}
	proxies, err := loadConfiguredProxies(snap.Cfg.ProxiesDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取代理配置失败")
		return
	}
	if err := ensureUniqueChannel(channels, req.Provider, req.Name, ""); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	templates, err := probetpl.LoadTemplates(snap.Cfg.TemplatesDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取探测模板失败")
		return
	}
	ch, err := buildChannelFromRequest(req, templates, proxies)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !req.ClearAPIKey && req.APIKey != nil {
		ch.APIKey = strings.TrimSpace(*req.APIKey)
	}
	ch.ID = config.NewID("ch")
	ch.Revision = 1
	path := filepath.Join(snap.Cfg.ChannelsDir, ch.ID+".yaml")
	if err := writeChannelFile(path, ch); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "写入渠道配置失败")
		return
	}
	s.invalidateStatusCache()
	writeJSON(w, http.StatusCreated, toAdminChannelJSON(ch))
}

func (s *Server) updateAdminChannel(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, true) {
		return
	}
	id := r.PathValue("id")
	var req channelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Revision == nil {
		writeJSONError(w, http.StatusBadRequest, "缺少 revision")
		return
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()

	snap := s.getState()
	if snap == nil || snap.Cfg == nil {
		writeJSONError(w, http.StatusInternalServerError, "服务尚未加载配置")
		return
	}
	channels, err := config.LoadChannels(snap.Cfg.ChannelsDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取渠道配置失败")
		return
	}
	proxies, err := loadConfiguredProxies(snap.Cfg.ProxiesDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取代理配置失败")
		return
	}
	path, current, err := findChannelFile(snap.Cfg.ChannelsDir, id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "渠道不存在")
		return
	}
	if current.Revision != *req.Revision {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "revision_conflict",
			"message": "渠道已被其他操作修改，请刷新后重试",
			"current": toAdminChannelJSON(current),
		})
		return
	}
	if err := ensureUniqueChannel(channels, req.Provider, req.Name, id); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	templates, err := probetpl.LoadTemplates(snap.Cfg.TemplatesDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取探测模板失败")
		return
	}
	updated, err := buildChannelFromRequest(req, templates, proxies)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated.ID = id
	updated.Revision = current.Revision + 1
	if updated.Revision <= 0 {
		updated.Revision = 1
	}
	if req.ClearAPIKey {
		updated.APIKey = ""
	} else if req.APIKey != nil {
		updated.APIKey = strings.TrimSpace(*req.APIKey)
	} else {
		updated.APIKey = current.APIKey
	}
	if err := writeChannelFile(path, updated); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "写入渠道配置失败")
		return
	}
	s.invalidateStatusCache()
	writeJSON(w, http.StatusOK, toAdminChannelJSON(updated))
}

func (s *Server) deleteAdminChannel(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, true) {
		return
	}
	id := r.PathValue("id")
	revision, ok := readRevision(r)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "缺少 revision")
		return
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()

	snap := s.getState()
	if snap == nil || snap.Cfg == nil {
		writeJSONError(w, http.StatusInternalServerError, "服务尚未加载配置")
		return
	}
	path, current, err := findChannelFile(snap.Cfg.ChannelsDir, id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "渠道不存在")
		return
	}
	if current.Revision != revision {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "revision_conflict",
			"message": "渠道已被其他操作修改，请刷新后重试",
			"current": toAdminChannelJSON(current),
		})
		return
	}
	archiveDir := filepath.Join(snap.Cfg.ChannelsDir, ".archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "创建归档目录失败")
		return
	}
	destination := filepath.Join(archiveDir, filepath.Base(path))
	if _, err := os.Stat(destination); err == nil {
		ext := filepath.Ext(destination)
		base := strings.TrimSuffix(filepath.Base(destination), ext)
		destination = filepath.Join(archiveDir, fmt.Sprintf("%s.%d%s", base, time.Now().UnixNano(), ext))
	} else if !errors.Is(err, os.ErrNotExist) {
		writeJSONError(w, http.StatusInternalServerError, "检查归档文件失败")
		return
	}
	if err := os.Rename(path, destination); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "归档渠道失败")
		return
	}
	s.invalidateStatusCache()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "archived_file": filepath.Base(destination)})
}

func (s *Server) probeAdminChannel(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, true) {
		return
	}
	id := r.PathValue("id")
	var req probeRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, "请求体格式错误")
			return
		}
	}
	req.Model = strings.TrimSpace(req.Model)
	snap := s.getState()
	if snap == nil || snap.Cfg == nil {
		writeJSONError(w, http.StatusInternalServerError, "服务尚未加载配置")
		return
	}
	_, ch, err := findChannelFile(snap.Cfg.ChannelsDir, id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "渠道不存在")
		return
	}
	model := req.Model
	if len(ch.Models) == 0 {
		if model != "" {
			writeJSONError(w, http.StatusBadRequest, "该渠道没有可选择的模型")
			return
		}
	} else {
		if model == "" && len(ch.Models) == 1 {
			model = ch.Models[0]
		}
		found := false
		for _, candidate := range ch.Models {
			if candidate == model {
				found = true
				break
			}
		}
		if !found {
			writeJSONError(w, http.StatusBadRequest, "模型不属于该渠道")
			return
		}
	}
	templates, err := probetpl.LoadTemplates(snap.Cfg.TemplatesDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取探测模板失败")
		return
	}
	tpl := templates[ch.Template]
	if tpl == nil {
		writeJSONError(w, http.StatusBadRequest, "渠道引用的探测模板不存在")
		return
	}
	proxies, err := loadConfiguredProxies(snap.Cfg.ProxiesDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取代理配置失败")
		return
	}
	proxyURL := ""
	if ch.Proxy != "" {
		for _, candidate := range proxies {
			if candidate.ID == ch.Proxy {
				proxyURL = candidate.URL
				break
			}
		}
		if proxyURL == "" {
			writeJSONError(w, http.StatusBadRequest, "渠道引用的代理不存在")
			return
		}
	}
	s.probeMu.RLock()
	runner, sink := s.manualProbe, s.recordResult
	s.probeMu.RUnlock()
	if runner == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "手动探测尚未初始化")
		return
	}
	target := prober.Target{
		ChannelID: ch.ID, Provider: ch.Provider, ChannelName: ch.Name,
		Model: model, Template: tpl, BaseURL: ch.BaseURL,
		APIKey: ch.APIKeyResolved(), ProxyURL: proxyURL, Interval: ch.Interval.D(),
	}
	target.ProbeTimeout = snap.Cfg.ProbeTimeout.D()
	target.ProbeTimeoutSet = target.ProbeTimeout > 0
	target.SlowLatency = snap.Cfg.SlowLatency.D()
	target.SlowLatencySet = true
	res := runner(r.Context(), target)
	if sink != nil {
		sink(target, res)
	}
	s.invalidateStatusCache()
	writeJSON(w, http.StatusOK, probeResultJSON{
		ChannelID: res.ChannelID, Model: res.Model, Status: res.Status,
		SubStatus: res.SubStatus, HTTPCode: res.HTTPCode, LatencyMS: res.LatencyMS,
		Error: res.Error, TS: res.TS,
	})
}

func (s *Server) resetAdminChannel(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, true) {
		return
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	id := r.PathValue("id")
	revision, ok := readRevision(r)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "缺少 revision")
		return
	}
	snap := s.getState()
	if snap == nil || snap.Cfg == nil {
		writeJSONError(w, http.StatusInternalServerError, "服务尚未加载配置")
		return
	}
	_, current, err := findChannelFile(snap.Cfg.ChannelsDir, id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "渠道不存在")
		return
	}
	if current.Revision != revision {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "revision_conflict",
			"message": "渠道已被其他操作修改，请刷新后重试",
			"current": toAdminChannelJSON(current),
		})
		return
	}
	s.probeMu.RLock()
	reset := s.resetChannel
	s.probeMu.RUnlock()
	if reset == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "状态重置尚未初始化")
		return
	}
	deleted, err := reset(id)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "重置渠道状态失败")
		return
	}
	s.invalidateStatusCache()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_records": deleted})
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request, mutate bool) bool {
	if !s.validSession(r) {
		writeJSONError(w, http.StatusUnauthorized, "需要管理员登录")
		return false
	}
	if mutate && !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "请求来源不受信任")
		return false
	}
	return true
}

func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true // CLI / 反向代理未转发 Origin 时仍允许已认证会话。
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	// Vite 开发代理与后端端口不同，但都在本机；生产环境仍要求同源。
	return isLoopbackHost(u.Hostname()) && isLoopbackHost(hostnameOnly(r.Host))
}

func hostnameOnly(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err == nil {
		return host
	}
	return strings.Trim(hostport, "[]")
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体格式错误")
		return false
	}
	return true
}

func readRevision(r *http.Request) (int, bool) {
	if value := r.URL.Query().Get("revision"); value != "" {
		revision, err := strconv.Atoi(value)
		return revision, err == nil && revision >= 0
	}
	if r.Body == nil {
		return 0, false
	}
	var req revisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Revision == nil || *req.Revision < 0 {
		return 0, false
	}
	return *req.Revision, true
}

func ensureUniqueChannel(channels []*config.Channel, provider, name, exceptID string) error {
	provider = strings.TrimSpace(provider)
	name = strings.TrimSpace(name)
	if provider == "" || name == "" {
		return fmt.Errorf("provider 和 name 必填")
	}
	for _, ch := range channels {
		if ch.ID != exceptID && ch.Provider == provider && ch.Name == name {
			return fmt.Errorf("provider/name 已存在：%s/%s", provider, name)
		}
	}
	return nil
}

func buildChannelFromRequest(req channelRequest, templates map[string]*probetpl.Template, proxies []*config.Proxy) (*config.Channel, error) {
	provider := strings.TrimSpace(req.Provider)
	name := strings.TrimSpace(req.Name)
	templateName := strings.TrimSpace(req.Template)
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if provider == "" || name == "" {
		return nil, fmt.Errorf("provider 和 name 必填")
	}
	if templateName == "" {
		return nil, fmt.Errorf("template 必填")
	}
	if templates[templateName] == nil {
		return nil, fmt.Errorf("探测模板不存在：%s", templateName)
	}
	proxyID := strings.TrimSpace(req.Proxy)
	if proxyID != "" {
		found := false
		for _, proxy := range proxies {
			if proxy.ID == proxyID {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("代理不存在：%s", proxyID)
		}
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("base_url 必须是 http(s) URL")
	}
	interval, err := parseInterval(req.Interval)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(req.Models))
	seen := map[string]struct{}{}
	for _, raw := range req.Models {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	apiKeyEnv := strings.TrimSpace(req.APIKeyEnv)
	return &config.Channel{
		Provider: provider, Name: name, Hidden: req.Hidden, Disabled: req.Disabled,
		Interval: interval, Template: templateName, BaseURL: baseURL,
		Proxy: proxyID, APIKeyEnv: apiKeyEnv, Models: models,
	}, nil
}

func parseInterval(value string) (config.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("interval 无效：请输入 15s、5m 或 0")
	}
	return config.Duration(d), nil
}

func findChannelFile(dir, id string) (string, *config.Channel, error) {
	if id == "" || strings.ContainsAny(id, `/\\`) || id == "." || id == ".." {
		return "", nil, os.ErrNotExist
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", nil, err
		}
		var ch config.Channel
		if err := yaml.Unmarshal(raw, &ch); err != nil {
			return "", nil, err
		}
		if ch.ID == id {
			return path, &ch, nil
		}
	}
	return "", nil, os.ErrNotExist
}

func writeChannelFile(path string, ch *config.Channel) error {
	data, err := yaml.Marshal(ch)
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(path, data)
}

func toAdminChannelJSON(ch *config.Channel) adminChannelJSON {
	models := make([]string, len(ch.Models))
	copy(models, ch.Models)
	return adminChannelJSON{
		ID: ch.ID, Provider: ch.Provider, Name: ch.Name, Hidden: ch.Hidden,
		Disabled: ch.Disabled, Interval: ch.Interval.D().String(), Template: ch.Template,
		BaseURL: ch.BaseURL, Proxy: ch.Proxy, APIKeyEnv: ch.APIKeyEnv, APIKeySet: ch.APIKeyResolved() != "",
		Models: models, Revision: ch.Revision,
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
