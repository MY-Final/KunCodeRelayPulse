// Package store 封装 SQLite 持久化：probe_log 只 append 不 update，
// event 存状态翻转事件流。驱动为 modernc.org/sqlite（纯 Go，免 CGO）。
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type ProbeRow struct {
	ChannelID   string
	Model       string
	Status      int
	SubStatus   string
	HTTPCode    int
	LatencyMS   int64
	TS          int64
	ErrorDetail string
}

// ProbePoint 是状态页热力图使用的单次探测快照。
// 只返回展示所需字段，不暴露 error_detail。
type ProbePoint struct {
	Status    int    `json:"status"`
	SubStatus string `json:"sub_status,omitempty"`
	HTTPCode  int    `json:"http_code,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	TS        int64  `json:"ts"`
}

type Bucket struct {
	DayTS      int64   `json:"day"`
	Total      int64   `json:"total"`
	Up         int64   `json:"up"`
	Yellow     int64   `json:"yellow"`
	AvgLatency float64 `json:"avg_latency_ms"`
}

type WindowStat struct {
	Window     string  `json:"window"`
	Total      int64   `json:"total"`
	Up         int64   `json:"up"`
	Yellow     int64   `json:"yellow"`
	AvgLatency float64 `json:"avg_latency_ms"`
}

type Event struct {
	ChannelID string
	Model     string
	Type      string // down / up
	TS        int64
	Detail    string
}

type DetectorState struct {
	ChannelID           string
	Model               string
	ConsecutiveFailures int
	Down                bool
	UpdatedAt           int64
}

type Store struct {
	db *sql.DB
}

var schema = `
CREATE TABLE IF NOT EXISTS probe_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  status INTEGER NOT NULL,
  sub_status TEXT,
  http_code INTEGER,
  latency_ms INTEGER,
  ts INTEGER NOT NULL,
  error_detail TEXT
);
CREATE INDEX IF NOT EXISTS idx_probe ON probe_log(channel_id, model, ts DESC);
CREATE TABLE IF NOT EXISTS event (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL,
  ts INTEGER NOT NULL,
  detail TEXT
);
CREATE INDEX IF NOT EXISTS idx_event ON event(ts DESC);
CREATE TABLE IF NOT EXISTS detector_state (
  channel_id TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  down INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(channel_id, model)
);
`

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 单连接足够这个量级，同时天然规避 SQLITE_BUSY 并发写
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化表结构: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) InsertProbe(r ProbeRow) error {
	_, err := s.db.Exec(
		`INSERT INTO probe_log(channel_id, model, status, sub_status, http_code, latency_ms, ts, error_detail)
		 VALUES(?,?,?,?,?,?,?,?)`,
		r.ChannelID, r.Model, r.Status, r.SubStatus, r.HTTPCode, r.LatencyMS, r.TS, r.ErrorDetail)
	return err
}

// ResetChannel 删除一个渠道下的全部探测记录、事件和 detector 持久化状态。
// 调用方应同时清理内存中的 detector 状态，并在需要时串行化这两个动作。
func (s *Store) ResetChannel(channelID string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var total int64
	for _, query := range []string{
		`DELETE FROM probe_log WHERE channel_id=?`,
		`DELETE FROM event WHERE channel_id=?`,
		`DELETE FROM detector_state WHERE channel_id=?`,
	} {
		result, err := tx.Exec(query, channelID)
		if err != nil {
			return total, err
		}
		deleted, _ := result.RowsAffected()
		total += deleted
	}
	if err := tx.Commit(); err != nil {
		return total, err
	}
	return total, nil
}

// RecordProbe 原子写入探测结果、状态翻转事件和 detector 当前状态。
func (s *Store) RecordProbe(r ProbeRow, events []Event, state DetectorState) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO probe_log(channel_id, model, status, sub_status, http_code, latency_ms, ts, error_detail)
		 VALUES(?,?,?,?,?,?,?,?)`,
		r.ChannelID, r.Model, r.Status, r.SubStatus, r.HTTPCode, r.LatencyMS, r.TS, r.ErrorDetail); err != nil {
		return err
	}
	for _, e := range events {
		if _, err := tx.Exec(`INSERT INTO event(channel_id, model, type, ts, detail) VALUES(?,?,?,?,?)`,
			e.ChannelID, e.Model, e.Type, e.TS, e.Detail); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO detector_state(channel_id, model, consecutive_failures, down, updated_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(channel_id, model) DO UPDATE SET
		  consecutive_failures=excluded.consecutive_failures,
		  down=excluded.down,
		  updated_at=excluded.updated_at`,
		state.ChannelID, state.Model, state.ConsecutiveFailures, boolInt(state.Down), state.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) LoadDetectorStates() ([]DetectorState, error) {
	rows, err := s.db.Query(`
		SELECT channel_id, model, consecutive_failures, down, updated_at
		FROM detector_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DetectorState
	for rows.Next() {
		var st DetectorState
		var down int
		if err := rows.Scan(&st.ChannelID, &st.Model, &st.ConsecutiveFailures, &down, &st.UpdatedAt); err != nil {
			return nil, err
		}
		st.Down = down != 0
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) Latest(channelID, model string) (*ProbeRow, error) {
	row := s.db.QueryRow(
		`SELECT status, sub_status, http_code, latency_ms, ts, COALESCE(error_detail,'')
		 FROM probe_log WHERE channel_id=? AND model=? ORDER BY ts DESC, id DESC LIMIT 1`,
		channelID, model)
	var r ProbeRow
	r.ChannelID, r.Model = channelID, model
	if err := row.Scan(&r.Status, &r.SubStatus, &r.HTTPCode, &r.LatencyMS, &r.TS, &r.ErrorDetail); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// RecentProbes 返回最近 limit 次探测，按时间从旧到新排列，供热力图渲染。
func (s *Store) RecentProbes(channelID, model string, limit int) ([]ProbePoint, error) {
	if limit <= 0 {
		return []ProbePoint{}, nil
	}
	rows, err := s.db.Query(`
		SELECT status, sub_status, http_code, latency_ms, ts
		FROM probe_log WHERE channel_id=? AND model=?
		ORDER BY ts DESC, id DESC LIMIT ?`, channelID, model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ProbePoint, 0, limit)
	for rows.Next() {
		var p ProbePoint
		if err := rows.Scan(&p.Status, &p.SubStatus, &p.HTTPCode, &p.LatencyMS, &p.TS); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out, nil
}

// DailyBuckets 返回最近 days 天的按天聚合（UTC 日界），用于 90 天 uptime 条。
func (s *Store) DailyBuckets(channelID, model string, days int, now int64) ([]Bucket, error) {
	since := now - int64(days)*86400
	rows, err := s.db.Query(`
		SELECT ts/86400*86400 AS day, COUNT(*),
		       SUM(CASE WHEN status=1 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN status=2 THEN 1 ELSE 0 END),
		       AVG(CASE WHEN status IN (1,2) THEN latency_ms END)
		FROM probe_log
		WHERE channel_id=? AND model=? AND ts>=?
		GROUP BY day ORDER BY day`, channelID, model, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bucket
	for rows.Next() {
		var b Bucket
		var avg sql.NullFloat64
		if err := rows.Scan(&b.DayTS, &b.Total, &b.Up, &b.Yellow, &avg); err != nil {
			return nil, err
		}
		b.AvgLatency = avg.Float64
		out = append(out, b)
	}
	return out, rows.Err()
}

// WindowStats 返回 24h/7d/90d 三个窗口的可用率与平均延迟。
func (s *Store) WindowStats(channelID, model string, now int64) ([]WindowStat, error) {
	windows := []struct {
		name string
		secs int64
	}{{"24h", 86400}, {"7d", 7 * 86400}, {"90d", 90 * 86400}}
	var out []WindowStat
	for _, w := range windows {
		var total, up, yellow sql.NullInt64
		var avg sql.NullFloat64
		err := s.db.QueryRow(`
			SELECT COUNT(*),
			       SUM(CASE WHEN status=1 THEN 1 ELSE 0 END),
			       SUM(CASE WHEN status=2 THEN 1 ELSE 0 END),
			       AVG(CASE WHEN status IN (1,2) THEN latency_ms END)
			FROM probe_log WHERE channel_id=? AND model=? AND ts>=?`,
			channelID, model, now-w.secs).Scan(&total, &up, &yellow, &avg)
		if err != nil {
			return nil, err
		}
		out = append(out, WindowStat{
			Window: w.name, Total: total.Int64, Up: up.Int64, Yellow: yellow.Int64, AvgLatency: avg.Float64,
		})
	}
	return out, nil
}

func (s *Store) InsertEvent(e Event) error {
	_, err := s.db.Exec(`INSERT INTO event(channel_id, model, type, ts, detail) VALUES(?,?,?,?,?)`,
		e.ChannelID, e.Model, e.Type, e.TS, e.Detail)
	return err
}

func (s *Store) RecentEvents(limit int) ([]Event, error) {
	rows, err := s.db.Query(
		`SELECT channel_id, model, type, ts, COALESCE(detail,'') FROM event ORDER BY ts DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ChannelID, &e.Model, &e.Type, &e.TS, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Cleanup 删除超过保留期的探测记录与事件，返回删除的总行数。
func (s *Store) Cleanup(retentionDays int) (int64, error) {
	cutoff := time.Now().Unix() - int64(retentionDays)*86400
	var total int64
	for _, q := range []string{
		`DELETE FROM probe_log WHERE ts < ?`,
		`DELETE FROM event WHERE ts < ?`,
	} {
		res, err := s.db.Exec(q, cutoff)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
