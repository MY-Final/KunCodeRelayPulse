// Package reload 提供两件事：
//  1. Watcher：fsnotify 监听配置目录，200ms 防抖后触发重载回调；
//  2. Ready：热更新健康状态。fail-closed 的关键配套——重载失败必须
//     暴露到 /ready，否则「后台保存返回 200 但运行态一直是旧配置」没人发现。
package reload

import (
	"fmt"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	w        *fsnotify.Watcher
	debounce time.Duration
	onChange func()
	stop     chan struct{}
	done     chan struct{}
}

// New 监听一组路径（文件或目录），任意事件防抖后触发一次 onChange。
func New(paths []string, debounce time.Duration, onChange func()) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if err := w.Add(p); err != nil {
			w.Close()
			return nil, fmt.Errorf("监听 %s: %w", p, err)
		}
	}
	t := &Watcher{w: w, debounce: debounce, onChange: onChange, stop: make(chan struct{}), done: make(chan struct{})}
	go t.loop()
	return t, nil
}

func (t *Watcher) Close() {
	close(t.stop)
	<-t.done
}

func (t *Watcher) loop() {
	defer close(t.done)
	defer t.w.Close()
	var timer *time.Timer
	var fired <-chan time.Time
	for {
		select {
		case <-t.stop:
			if timer != nil {
				timer.Stop()
			}
			return
		case ev, ok := <-t.w.Events:
			if !ok {
				return
			}
			_ = ev
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(t.debounce)
			fired = timer.C
		case <-fired:
			fired = nil
			t.onChange()
		}
	}
}

// Ready 汇总热更新结果，供 /ready 端点输出。
type Ready struct {
	mu         sync.Mutex
	failures   uint64
	lastErr    string
	lastOKAt   time.Time
	lastFailAt time.Time
	lastFailed bool
	startedAt  time.Time
}

func NewReady() *Ready { return &Ready{startedAt: time.Now()} }

func (r *Ready) OK() {
	r.mu.Lock()
	r.lastFailed = false
	r.lastOKAt = time.Now()
	r.mu.Unlock()
}

func (r *Ready) Fail(err error) {
	r.mu.Lock()
	r.failures++
	r.lastErr = err.Error()
	r.lastFailAt = time.Now()
	r.lastFailed = true
	r.mu.Unlock()
}

type Status struct {
	Status           string `json:"status"` // ok / degraded
	ReloadFailures   uint64 `json:"reload_failures"`
	LastReloadError  string `json:"last_reload_error,omitempty"`
	LastReloadOKAt   int64  `json:"last_reload_ok_at,omitempty"`
	LastReloadFailAt int64  `json:"last_reload_fail_at,omitempty"`
	StartedAt        int64  `json:"started_at"`
	UptimeSecs       int64  `json:"uptime_secs"`
}

func (r *Ready) Snapshot() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := Status{
		ReloadFailures:   r.failures,
		LastReloadError:  r.lastErr,
		StartedAt:        r.startedAt.Unix(),
		UptimeSecs:       int64(time.Since(r.startedAt).Seconds()),
	}
	if !r.lastOKAt.IsZero() {
		st.LastReloadOKAt = r.lastOKAt.Unix()
	}
	if !r.lastFailAt.IsZero() {
		st.LastReloadFailAt = r.lastFailAt.Unix()
	}
	if r.lastFailed {
		st.Status = "degraded"
	} else {
		st.Status = "ok"
	}
	return st
}
