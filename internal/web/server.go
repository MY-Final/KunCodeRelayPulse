// Package web 提供 HTTP 服务：静态状态页、状态聚合、管理员登录、渠道管理、/ready。
// /api/status 的公开视角过滤 hidden，管理员会话可见全部；状态聚合结果缓存 30s。
package web

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"kuncode-relay-pulse/internal/config"
	"kuncode-relay-pulse/internal/prober"
	"kuncode-relay-pulse/internal/reload"
	"kuncode-relay-pulse/internal/store"
)

//go:embed static
var staticFS embed.FS

const sessionCookie = "pulse_session"

type Snapshot struct {
	Cfg      *config.Config
	Channels []*config.Channel
	Targets  []prober.Target
}

type Server struct {
	getState func() *Snapshot
	store    *store.Store
	ready    *reload.Ready

	mu       sync.Mutex
	sessions map[string]time.Time

	channelMu    sync.Mutex
	probeMu      sync.RWMutex
	manualProbe  ProbeRunner
	recordResult ResultSink
	resetChannel ResetChannel

	cacheMu sync.Mutex
	cache   map[string]cacheEntry

	settingsMu     sync.Mutex
	reloadSettings func() error
}

type cacheEntry struct {
	at   time.Time
	body []byte
}

func New(getState func() *Snapshot, st *store.Store, rd *reload.Ready) *Server {
	return &Server{
		getState: getState,
		store:    st,
		ready:    rd,
		sessions: map[string]time.Time{},
		cache:    map[string]cacheEntry{},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.serveIndex)
	mux.HandleFunc("GET /admin/channels", s.serveIndex)
	mux.HandleFunc("GET /admin/channels/", s.serveIndex)
	mux.HandleFunc("GET /admin/settings", s.serveIndex)
	mux.HandleFunc("GET /admin/settings/", s.serveIndex)
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("GET /api/status", s.serveStatus)
	mux.HandleFunc("GET /api/status/trend", s.serveStatusTrend)
	mux.HandleFunc("POST /api/login", s.serveLogin)
	mux.HandleFunc("POST /api/logout", s.serveLogout)
	mux.HandleFunc("GET /api/admin/channels", s.serveAdminChannels)
	mux.HandleFunc("POST /api/admin/channels", s.createAdminChannel)
	mux.HandleFunc("PUT /api/admin/channels/{id}", s.updateAdminChannel)
	mux.HandleFunc("DELETE /api/admin/channels/{id}", s.deleteAdminChannel)
	mux.HandleFunc("POST /api/admin/channels/{id}/probe", s.probeAdminChannel)
	mux.HandleFunc("POST /api/admin/channels/{id}/reset", s.resetAdminChannel)
	mux.HandleFunc("GET /api/admin/proxies", s.serveAdminProxies)
	mux.HandleFunc("POST /api/admin/proxies", s.createAdminProxy)
	mux.HandleFunc("PUT /api/admin/proxies/{id}", s.updateAdminProxy)
	mux.HandleFunc("DELETE /api/admin/proxies/{id}", s.deleteAdminProxy)
	mux.HandleFunc("POST /api/admin/proxies/{id}/test", s.testAdminProxy)
	mux.HandleFunc("GET /api/admin/settings", s.serveAdminSettings)
	mux.HandleFunc("PUT /api/admin/settings", s.updateAdminSettings)
	mux.HandleFunc("GET /ready", s.serveReady)
	return mux
}

// SetManualProbe 注入手动探测实现。手动探测和调度器共用同一套结果回调。
func (s *Server) SetManualProbe(runner ProbeRunner, sink ResultSink) {
	s.probeMu.Lock()
	s.manualProbe = runner
	s.recordResult = sink
	s.probeMu.Unlock()
}

// SetResetChannel 注入渠道统计重置实现，由主程序负责同步内存与持久化状态。
func (s *Server) SetResetChannel(reset ResetChannel) {
	s.probeMu.Lock()
	s.resetChannel = reset
	s.probeMu.Unlock()
}

// SetReloadSettings 注入主程序的完整热加载流程，确保设置保存后运行态同步更新。
func (s *Server) SetReloadSettings(reload func() error) {
	s.settingsMu.Lock()
	s.reloadSettings = reload
	s.settingsMu.Unlock()
}

func (s *Server) invalidateStatusCache() {
	s.cacheMu.Lock()
	s.cache = map[string]cacheEntry{}
	s.cacheMu.Unlock()
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (s *Server) serveReady(w http.ResponseWriter, r *http.Request) {
	st := s.ready.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	code := http.StatusOK
	if st.Status != "ok" {
		// 重载失败 = 服务可能还在跑旧配置，必须显性暴露
		code = http.StatusServiceUnavailable
	}
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(st)
}

func (s *Server) serveStatus(w http.ResponseWriter, r *http.Request) {
	admin := s.validSession(r)
	body, err := s.statusBody(admin)
	if err != nil {
		log.Printf("[api] 聚合状态失败: %v", err)
		http.Error(w, "aggregate failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func (s *Server) serveLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	snap := s.getState()
	okUser := subtle.ConstantTimeCompare([]byte(req.Username), []byte(snap.Cfg.Admin.Username)) == 1
	okPass := bcrypt.CompareHashAndPassword([]byte(snap.Cfg.Admin.PasswordHash), []byte(req.Password)) == nil
	if !okUser || !okPass {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	tokenHex := hex.EncodeToString(token)
	ttl := snap.Cfg.Admin.SessionTTL.D()
	s.mu.Lock()
	s.sessions[tokenHex] = time.Now().Add(ttl)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tokenHex,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) serveLogout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		delete(s.sessions, ck.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) validSession(r *http.Request) bool {
	ck, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	s.mu.Lock()
	exp, ok := s.sessions[ck.Value]
	s.mu.Unlock()
	if !ok || time.Now().After(exp) {
		return false
	}
	return true
}

// ---- 状态聚合 ----

type targetJSON struct {
	Model         string             `json:"model"`
	Status        *int               `json:"status,omitempty"`
	CurrentStatus *int               `json:"current_status,omitempty"`
	Streak        *streakJSON        `json:"streak,omitempty"`
	SubStatus     string             `json:"sub_status,omitempty"`
	HTTPCode      int                `json:"http_code,omitempty"`
	LatencyMS     int64              `json:"latency_ms,omitempty"`
	CheckedAt     int64              `json:"checked_at,omitempty"`
	Error         string             `json:"error,omitempty"`
	Windows       []store.WindowStat `json:"windows"`
	Daily         []store.Bucket     `json:"daily"`
	History       []store.ProbePoint `json:"history"`
}

type streakJSON struct {
	Kind                 string `json:"kind"`
	Count                int    `json:"count"`
	ConsecutiveSuccesses int    `json:"consecutive_successes"`
	ConsecutiveDegraded  int    `json:"consecutive_degraded"`
	ConsecutiveFailures  int    `json:"consecutive_failures"`
	ConsecutiveAnomalies int    `json:"consecutive_anomalies"`
	FailureThreshold     int    `json:"failure_threshold"`
	RecoveryThreshold    int    `json:"recovery_threshold"`
	UpdatedAt            int64  `json:"updated_at"`
}

type channelJSON struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Template string       `json:"template,omitempty"`
	Hidden   bool         `json:"hidden,omitempty"`
	Disabled bool         `json:"disabled,omitempty"`
	Targets  []targetJSON `json:"targets"`
}

type providerJSON struct {
	Name     string        `json:"name"`
	Channels []channelJSON `json:"channels"`
}

type eventJSON struct {
	Type     string `json:"type"`
	Provider string `json:"provider"`
	Channel  string `json:"channel"`
	Model    string `json:"model,omitempty"`
	Detail   string `json:"detail,omitempty"`
	TS       int64  `json:"ts"`
}

type statusJSON struct {
	View        string         `json:"view"` // public / admin
	GeneratedAt int64          `json:"generated_at"`
	SiteTitle   string         `json:"site_title"`
	Providers   []providerJSON `json:"providers"`
	Events      []eventJSON    `json:"events"`
}

const statusCacheTTL = 30 * time.Second
const heatmapProbeLimit = 48

func (s *Server) statusBody(admin bool) ([]byte, error) {
	key := "public"
	if admin {
		key = "admin"
	}
	s.cacheMu.Lock()
	e, ok := s.cache[key]
	s.cacheMu.Unlock()
	if ok && time.Since(e.at) < statusCacheTTL {
		return e.body, nil
	}
	body, err := s.buildStatus(admin)
	if err != nil {
		return nil, err
	}
	s.cacheMu.Lock()
	s.cache[key] = cacheEntry{at: time.Now(), body: body}
	s.cacheMu.Unlock()
	return body, nil
}

func (s *Server) buildStatus(admin bool) ([]byte, error) {
	snap := s.getState()
	now := time.Now().Unix()
	detectorStates, err := s.store.LoadDetectorStates()
	if err != nil {
		return nil, err
	}
	stateByTarget := make(map[string]store.DetectorState, len(detectorStates))
	for _, state := range detectorStates {
		stateByTarget[targetKey(state.ChannelID, state.Model)] = state
	}
	failureThreshold, recoveryThreshold := 2, 2
	if snap != nil && snap.Cfg != nil {
		if snap.Cfg.EventThreshold > 0 {
			failureThreshold = snap.Cfg.EventThreshold
		}
		if snap.Cfg.RecoveryThreshold > 0 {
			recoveryThreshold = snap.Cfg.RecoveryThreshold
		}
	}

	targetsByCh := map[string][]prober.Target{}
	for _, t := range snap.Targets {
		targetsByCh[t.ChannelID] = append(targetsByCh[t.ChannelID], t)
	}

	provs := make([]providerJSON, 0)
	for _, ch := range snap.Channels {
		if ch.Hidden && !admin {
			continue // 公开视角不展示隐藏通道
		}
		if len(provs) == 0 || provs[len(provs)-1].Name != ch.Provider {
			provs = append(provs, providerJSON{Name: ch.Provider})
		}
		cj := channelJSON{ID: ch.ID, Name: ch.Name, Template: ch.Template, Hidden: ch.Hidden, Disabled: ch.Disabled, Targets: make([]targetJSON, 0)}
		for _, t := range targetsByCh[ch.ID] {
			tj := targetJSON{Model: t.Model}
			if lr, err := s.store.Latest(t.ChannelID, t.Model); err == nil && lr != nil {
				st := lr.Status
				tj.Status = &st
				state, exists := stateByTarget[targetKey(t.ChannelID, t.Model)]
				if !exists {
					state = store.DetectorState{ChannelID: t.ChannelID, Model: t.Model, UpdatedAt: lr.TS}
				}
				current := logicalStatus(lr.Status, state.Down || state.ConsecutiveAnomalies >= failureThreshold)
				tj.CurrentStatus = &current
				tj.Streak = makeStreak(lr.Status, lr.TS, state, failureThreshold, recoveryThreshold)
				tj.SubStatus = lr.SubStatus
				tj.HTTPCode = lr.HTTPCode
				tj.LatencyMS = lr.LatencyMS
				tj.CheckedAt = lr.TS
				if admin {
					tj.Error = lr.ErrorDetail
				}
			}
			if ws, err := s.store.WindowStats(t.ChannelID, t.Model, now); err == nil {
				tj.Windows = ws
			}
			if db, err := s.store.DailyBuckets(t.ChannelID, t.Model, 90, now); err == nil {
				tj.Daily = db
			}
			if history, err := s.store.RecentProbes(t.ChannelID, t.Model, heatmapProbeLimit); err == nil {
				tj.History = history
			}
			cj.Targets = append(cj.Targets, tj)
		}
		provs[len(provs)-1].Channels = append(provs[len(provs)-1].Channels, cj)
	}

	evs, err := s.store.RecentEvents(30)
	if err != nil {
		return nil, err
	}
	chByID := map[string]*config.Channel{}
	for _, ch := range snap.Channels {
		chByID[ch.ID] = ch
	}
	evJSON := make([]eventJSON, 0)
	for _, e := range evs {
		ch := chByID[e.ChannelID]
		if !admin && (ch == nil || ch.Hidden) {
			continue
		}
		ej := eventJSON{Type: e.Type, Model: e.Model, Detail: e.Detail, TS: e.TS}
		if ch != nil {
			ej.Provider = ch.Provider
			ej.Channel = ch.Name
		} else {
			ej.Channel = e.ChannelID
		}
		if !admin {
			ej.Detail = ""
		}
		evJSON = append(evJSON, ej)
	}

	view := "public"
	if admin {
		view = "admin"
	}
	payload := statusJSON{
		View:        view,
		GeneratedAt: now,
		SiteTitle:   snap.Cfg.SiteTitle,
		Providers:   provs,
		Events:      evJSON,
	}
	return json.Marshal(payload)
}

func targetKey(channelID, model string) string { return channelID + "\x00" + model }

func logicalStatus(raw int, down bool) int {
	if down {
		return prober.StatusRed
	}
	if raw == prober.StatusRed {
		// 单次红色探测在升级阈值前按降级展示，避免瞬时抖动。
		return prober.StatusYellow
	}
	return raw
}

func makeStreak(raw int, ts int64, state store.DetectorState, failureThreshold, recoveryThreshold int) *streakJSON {
	kind, count := "", 0
	switch raw {
	case prober.StatusGreen:
		kind, count = "success", state.ConsecutiveSuccesses
	case prober.StatusYellow:
		kind, count = "degraded", state.ConsecutiveDegraded
	case prober.StatusRed:
		kind, count = "anomaly", state.ConsecutiveAnomalies
	}
	updatedAt := state.UpdatedAt
	if updatedAt == 0 {
		updatedAt = ts
	}
	return &streakJSON{
		Kind: kind, Count: count,
		ConsecutiveSuccesses: state.ConsecutiveSuccesses,
		ConsecutiveDegraded:  state.ConsecutiveDegraded,
		ConsecutiveFailures:  state.ConsecutiveFailures,
		ConsecutiveAnomalies: state.ConsecutiveAnomalies,
		FailureThreshold:     failureThreshold, RecoveryThreshold: recoveryThreshold,
		UpdatedAt: updatedAt,
	}
}
