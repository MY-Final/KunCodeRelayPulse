// Package detector 做事件检测：连续 N 次失败才判 down，避免单次抖动触发通知；
// 处于 down 状态后首次成功判 up。状态按 channel_id+model 键控，可由调用方持久化。
package detector

import (
	"sync"

	"kuncode-relay-pulse/internal/store"
)

type state struct {
	consecFails int
	down        bool
}

type Detector struct {
	mu        sync.Mutex
	threshold int
	states    map[string]*state
}

func New(threshold int) *Detector {
	return &Detector{threshold: threshold, states: map[string]*state{}}
}

// Restore 恢复进程重启前的状态，避免重复或遗漏 down/up 事件。
func (d *Detector) Restore(states []store.DetectorState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, st := range states {
		fails := st.ConsecutiveFailures
		if fails < 0 {
			fails = 0
		}
		d.states[stateKey(st.ChannelID, st.Model)] = &state{
			consecFails: fails,
			down:        st.Down,
		}
	}
}

// ResetChannel 清理指定渠道的所有模型状态，使下一次探测从全新基线开始。
func (d *Detector) ResetChannel(channelID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	prefix := channelID + "\x00"
	for key := range d.states {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(d.states, key)
		}
	}
}

// Snapshot 返回指定目标的当前状态，供持久化使用。
func (d *Detector) Snapshot(channelID, model string, ts int64) store.DetectorState {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.states[stateKey(channelID, model)]
	if s == nil {
		return store.DetectorState{ChannelID: channelID, Model: model, UpdatedAt: ts}
	}
	return store.DetectorState{
		ChannelID:           channelID,
		Model:               model,
		ConsecutiveFailures: s.consecFails,
		Down:                s.down,
		UpdatedAt:           ts,
	}
}

// SetThreshold 供热更新调整连续失败阈值。
func (d *Detector) SetThreshold(n int) {
	if n <= 0 {
		return
	}
	d.mu.Lock()
	d.threshold = n
	d.mu.Unlock()
}

// Observe 记录一次探测结果，返回由此产生的翻转事件（可能为空）。
// 绿/黄都视为可用。
func (d *Detector) Observe(channelID, model string, status int, ts int64) []store.Event {
	key := stateKey(channelID, model)
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.states[key]
	if s == nil {
		s = &state{}
		d.states[key] = s
	}
	var evs []store.Event
	if status >= 1 {
		if s.down {
			s.down = false
			evs = append(evs, store.Event{ChannelID: channelID, Model: model, Type: "up", TS: ts})
		}
		s.consecFails = 0
		return evs
	}
	s.consecFails++
	if !s.down && s.consecFails >= d.threshold {
		s.down = true
		evs = append(evs, store.Event{ChannelID: channelID, Model: model, Type: "down", TS: ts})
	}
	return evs
}

func stateKey(channelID, model string) string {
	return channelID + "\x00" + model
}
