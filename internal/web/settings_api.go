package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"kuncode-relay-pulse/internal/config"
)

type adminSettingsJSON struct {
	Interval          string `json:"interval"`
	Timeout           string `json:"timeout"`
	SlowLatency       string `json:"slow_latency"`
	FailureThreshold  int    `json:"failure_threshold"`
	RecoveryThreshold int    `json:"recovery_threshold"`
}

func (s *Server) serveAdminSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, false) {
		return
	}
	snap := s.getState()
	if snap == nil || snap.Cfg == nil {
		writeJSONError(w, http.StatusInternalServerError, "服务尚未加载配置")
		return
	}
	writeJSON(w, http.StatusOK, settingsJSON(snap.Cfg.ProbeSettings()))
}

func (s *Server) updateAdminSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, true) {
		return
	}
	var req adminSettingsJSON
	if !decodeJSON(w, r, &req) {
		return
	}
	settings, err := parseSettings(req)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	snap := s.getState()
	if snap == nil || snap.Cfg == nil || snap.Cfg.ConfigPath == "" {
		writeJSONError(w, http.StatusInternalServerError, "配置文件路径不可用")
		return
	}
	if err := config.UpdateProbeSettings(snap.Cfg.ConfigPath, settings); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.reloadSettings != nil {
		if err := s.reloadSettings(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "热加载失败",
				"message": err.Error(),
				"saved":   true,
			})
			return
		}
	}
	s.invalidateStatusCache()
	writeJSON(w, http.StatusOK, settingsJSON(settings))
}

func settingsJSON(settings config.ProbeSettings) adminSettingsJSON {
	return adminSettingsJSON{
		Interval:          time.Duration(settings.Interval).String(),
		Timeout:           time.Duration(settings.Timeout).String(),
		SlowLatency:       time.Duration(settings.SlowLatency).String(),
		FailureThreshold:  settings.FailureThreshold,
		RecoveryThreshold: settings.RecoveryThreshold,
	}
}

func parseSettings(req adminSettingsJSON) (config.ProbeSettings, error) {
	interval, err := parseSettingsDuration("interval", req.Interval)
	if err != nil {
		return config.ProbeSettings{}, err
	}
	timeout, err := parseSettingsDuration("timeout", req.Timeout)
	if err != nil {
		return config.ProbeSettings{}, err
	}
	slow, err := parseSettingsDuration("slow_latency", req.SlowLatency)
	if err != nil {
		return config.ProbeSettings{}, err
	}
	settings := config.ProbeSettings{
		Interval: interval, Timeout: timeout, SlowLatency: slow,
		FailureThreshold: req.FailureThreshold, RecoveryThreshold: req.RecoveryThreshold,
	}
	if err := config.ValidateProbeSettings(settings); err != nil {
		return config.ProbeSettings{}, err
	}
	return settings, nil
}

func parseSettingsDuration(name, raw string) (config.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "0" || raw == "0s" {
		return 0, nil
	}
	if raw == "" {
		return 0, fmt.Errorf("%s 不能为空", name)
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 无效：请输入 60s、5m 等时长", name)
	}
	return config.Duration(d), nil
}
