package web

import (
	"net/http"
	"strings"
	"time"

	"kuncode-relay-pulse/internal/config"
)

func (s *Server) serveStatusTrend(w http.ResponseWriter, r *http.Request) {
	snap := s.getState()
	if snap == nil || snap.Cfg == nil {
		writeJSONError(w, http.StatusInternalServerError, "服务尚未加载配置")
		return
	}
	channelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	window := strings.TrimSpace(r.URL.Query().Get("window"))
	if channelID == "" {
		writeJSONError(w, http.StatusBadRequest, "缺少 channel_id")
		return
	}
	if window == "" {
		window = "24h"
	}

	channel := channelByID(snap.Channels, channelID)
	if channel == nil {
		writeJSONError(w, http.StatusNotFound, "渠道不存在")
		return
	}
	if channel.Hidden && !s.validSession(r) {
		// 隐藏渠道对未登录用户表现为不存在，避免泄露渠道 ID。
		writeJSONError(w, http.StatusNotFound, "渠道不存在")
		return
	}
	if !channelHasModel(channel, model) {
		writeJSONError(w, http.StatusBadRequest, "模型不属于该渠道")
		return
	}
	trend, err := s.store.LatencyTrend(channelID, model, window, nowUnix())
	if err != nil {
		if strings.Contains(err.Error(), "不支持的趋势窗口") {
			writeJSONError(w, http.StatusBadRequest, "window 必须是 24h、7d 或 90d")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "聚合响应时间趋势失败")
		return
	}
	writeJSON(w, http.StatusOK, trend)
}

func channelByID(channels []*config.Channel, id string) *config.Channel {
	for _, channel := range channels {
		if channel.ID == id {
			return channel
		}
	}
	return nil
}

func channelHasModel(channel *config.Channel, model string) bool {
	if len(channel.Models) == 0 {
		return model == ""
	}
	for _, candidate := range channel.Models {
		if candidate == model {
			return true
		}
	}
	return false
}

func nowUnix() int64 { return time.Now().Unix() }
