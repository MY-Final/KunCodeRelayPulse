package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"kuncode-relay-pulse/internal/config"
	"kuncode-relay-pulse/internal/prober"
)

type adminProxyJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	URLPreview string `json:"url_preview,omitempty"`
	URLSet     bool   `json:"url_set"`
	HasAuth    bool   `json:"has_auth"`
	Revision   int    `json:"revision"`
}

type adminProxiesJSON struct {
	Proxies []adminProxyJSON `json:"proxies"`
}

type proxyRequest struct {
	Name     string  `json:"name"`
	URL      *string `json:"url"`
	Revision *int    `json:"revision"`
}

type proxyTestJSON struct {
	OK        bool   `json:"ok"`
	HTTPCode  int    `json:"http_code,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

func loadConfiguredProxies(dir string) ([]*config.Proxy, error) {
	return config.LoadProxies(dir)
}

func (s *Server) serveAdminProxies(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, false) {
		return
	}
	snap := s.getState()
	if snap == nil || snap.Cfg == nil {
		writeJSONError(w, http.StatusInternalServerError, "服务尚未加载配置")
		return
	}
	proxies, err := loadConfiguredProxies(snap.Cfg.ProxiesDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取代理配置失败")
		return
	}
	result := adminProxiesJSON{Proxies: make([]adminProxyJSON, 0, len(proxies))}
	for _, proxy := range proxies {
		result.Proxies = append(result.Proxies, toAdminProxyJSON(proxy))
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) createAdminProxy(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, true) {
		return
	}
	var req proxyRequest
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
	proxies, err := loadConfiguredProxies(snap.Cfg.ProxiesDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取代理配置失败")
		return
	}
	if err := ensureUniqueProxy(proxies, req.Name, ""); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	proxy, err := buildProxyFromRequest(req, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	proxy.ID = config.NewID("px")
	proxy.Revision = 1
	if err := os.MkdirAll(snap.Cfg.ProxiesDir, 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "创建代理目录失败")
		return
	}
	if err := writeProxyFile(filepath.Join(snap.Cfg.ProxiesDir, proxy.ID+".yaml"), proxy); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "写入代理配置失败")
		return
	}
	s.invalidateStatusCache()
	writeJSON(w, http.StatusCreated, toAdminProxyJSON(proxy))
}

func (s *Server) updateAdminProxy(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, true) {
		return
	}
	id := r.PathValue("id")
	var req proxyRequest
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
	proxies, err := loadConfiguredProxies(snap.Cfg.ProxiesDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取代理配置失败")
		return
	}
	path, current, err := findProxyFile(snap.Cfg.ProxiesDir, id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "代理不存在")
		return
	}
	if current.Revision != *req.Revision {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "revision_conflict",
			"message": "代理已被其他操作修改，请刷新后重试",
			"current": toAdminProxyJSON(current),
		})
		return
	}
	if err := ensureUniqueProxy(proxies, req.Name, id); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	urlValue := current.URL
	if req.URL != nil && strings.TrimSpace(*req.URL) != "" {
		urlValue = strings.TrimSpace(*req.URL)
	}
	updated, err := buildProxyFromRequest(proxyRequest{Name: req.Name, URL: &urlValue}, id)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated.Revision = current.Revision + 1
	if updated.Revision <= 0 {
		updated.Revision = 1
	}
	if err := writeProxyFile(path, updated); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "写入代理配置失败")
		return
	}
	s.invalidateStatusCache()
	writeJSON(w, http.StatusOK, toAdminProxyJSON(updated))
}

func (s *Server) deleteAdminProxy(w http.ResponseWriter, r *http.Request) {
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
	path, current, err := findProxyFile(snap.Cfg.ProxiesDir, id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "代理不存在")
		return
	}
	if current.Revision != revision {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "revision_conflict",
			"message": "代理已被其他操作修改，请刷新后重试",
			"current": toAdminProxyJSON(current),
		})
		return
	}
	channels, err := config.LoadChannels(snap.Cfg.ChannelsDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取渠道配置失败")
		return
	}
	for _, channel := range channels {
		if channel.Proxy == id {
			writeJSONError(w, http.StatusConflict, fmt.Sprintf("代理仍被渠道引用：%s", channel.Name))
			return
		}
	}
	archiveDir := filepath.Join(snap.Cfg.ProxiesDir, ".archive")
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
		writeJSONError(w, http.StatusInternalServerError, "归档代理失败")
		return
	}
	s.invalidateStatusCache()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "archived_file": filepath.Base(destination)})
}

func (s *Server) testAdminProxy(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, true) {
		return
	}
	id := r.PathValue("id")
	snap := s.getState()
	if snap == nil || snap.Cfg == nil {
		writeJSONError(w, http.StatusInternalServerError, "服务尚未加载配置")
		return
	}

	// 只在读取配置时持锁，网络测试本身不能阻塞其他代理或渠道的编辑。
	s.channelMu.Lock()
	proxies, err := loadConfiguredProxies(snap.Cfg.ProxiesDir)
	var proxyURL string
	for _, proxy := range proxies {
		if proxy.ID == id {
			proxyURL = proxy.URL
			break
		}
	}
	s.channelMu.Unlock()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取代理配置失败")
		return
	}
	if proxyURL == "" {
		writeJSONError(w, http.StatusNotFound, "代理不存在")
		return
	}

	result := prober.TestProxy(r.Context(), proxyURL)
	writeJSON(w, http.StatusOK, proxyTestJSON{
		OK: result.OK, HTTPCode: result.HTTPCode, LatencyMS: result.LatencyMS, Error: result.Error,
	})
}

func ensureUniqueProxy(proxies []*config.Proxy, name, exceptID string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("代理名称必填")
	}
	for _, proxy := range proxies {
		if proxy.ID != exceptID && proxy.Name == name {
			return fmt.Errorf("代理名称已存在：%s", name)
		}
	}
	return nil
}

func buildProxyFromRequest(req proxyRequest, id string) (*config.Proxy, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("代理名称必填")
	}
	if req.URL == nil || strings.TrimSpace(*req.URL) == "" {
		return nil, fmt.Errorf("代理 URL 必填")
	}
	proxyURL := strings.TrimSpace(*req.URL)
	if err := config.ValidateProxyURL(proxyURL); err != nil {
		return nil, err
	}
	return &config.Proxy{ID: id, Name: name, URL: proxyURL}, nil
}

func findProxyFile(dir, id string) (string, *config.Proxy, error) {
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
		var proxy config.Proxy
		if err := yaml.Unmarshal(raw, &proxy); err != nil {
			return "", nil, err
		}
		if proxy.ID == id {
			return path, &proxy, nil
		}
	}
	return "", nil, os.ErrNotExist
}

func writeProxyFile(path string, proxy *config.Proxy) error {
	data, err := yaml.Marshal(proxy)
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(path, data)
}

func toAdminProxyJSON(proxy *config.Proxy) adminProxyJSON {
	return adminProxyJSON{
		ID: proxy.ID, Name: proxy.Name, URLPreview: maskProxyURL(proxy.URL),
		URLSet: proxy.URL != "", HasAuth: proxyURLHasAuth(proxy.URL), Revision: proxy.Revision,
	}
}

func maskProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	username := u.User.Username()
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(username, "***")
	}
	return strings.ReplaceAll(u.String(), "%2A%2A%2A", "***")
}

func proxyURLHasAuth(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return false
	}
	_, ok := u.User.Password()
	return ok
}
