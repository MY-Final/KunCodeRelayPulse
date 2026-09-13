// Package scheduler 单 goroutine 调度循环 + 最小堆 + 并发信号量。
// 启动/热更新重建时按固定步长错峰，避免所有通道同一秒打出去；
// 用 generation 计数让旧配置派生的在途探测结果作废。
package scheduler

import (
	"container/heap"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"kuncode-relay-pulse/internal/prober"
)

type item struct {
	target   prober.Target
	interval time.Duration
	next     time.Time
}

type itemHeap []*item

func (h itemHeap) Len() int            { return len(h) }
func (h itemHeap) Less(i, j int) bool  { return h[i].next.Before(h[j].next) }
func (h itemHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *itemHeap) Push(x any)         { *h = append(*h, x.(*item)) }
func (h *itemHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

type ResultFunc func(prober.Target, prober.Result)

type Scheduler struct {
	mu       sync.Mutex
	h        itemHeap
	sem      chan struct{}
	onResult ResultFunc
	gen      atomic.Uint64
	quit     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	started  bool
}

func New(concurrency int, onResult ResultFunc) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		sem:      make(chan struct{}, maxInt(1, concurrency)),
		onResult: onResult,
		quit:     make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start 首次启动（或与 Restart 等价的重建）。
func (s *Scheduler) Start(targets []prober.Target, defaultInterval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen.Add(1)
	s.seedLocked(targets, defaultInterval)
	if !s.started {
		s.started = true
		s.wg.Add(1)
		go s.loop()
	}
}

// Restart 热更新后整堆重建：gen 自增让旧在途探测的结果被丢弃。
func (s *Scheduler) Restart(targets []prober.Target, defaultInterval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen.Add(1)
	s.seedLocked(targets, defaultInterval)
}

func (s *Scheduler) Stop() {
	close(s.quit)
	s.cancel()
	s.wg.Wait()
}

func (s *Scheduler) seedLocked(targets []prober.Target, def time.Duration) {
	s.h = nil
	// 错峰步长 = 默认间隔/目标数，夹在 [500ms, 10s]
	step := def / time.Duration(maxInt(1, len(targets)))
	if step < 500*time.Millisecond {
		step = 500 * time.Millisecond
	}
	if step > 10*time.Second {
		step = 10 * time.Second
	}
	base := time.Now()
	for i, t := range targets {
		interval := t.Interval
		if interval <= 0 {
			interval = def
		}
		heap.Push(&s.h, &item{target: t, interval: interval, next: base.Add(time.Duration(i) * step)})
	}
}

func (s *Scheduler) loop() {
	defer s.wg.Done()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	var fired <-chan time.Time
	for {
		now := time.Now()
		var due []*item
		delay := time.Second
		s.mu.Lock()
		for len(s.h) > 0 && !s.h[0].next.After(now) {
			due = append(due, heap.Pop(&s.h).(*item))
		}
		if len(s.h) > 0 {
			if d := s.h[0].next.Sub(now); d < delay {
				delay = d
			}
		}
		gen := s.gen.Load()
		s.mu.Unlock()

		for _, it := range due {
			s.wg.Add(1)
			go s.run(it, gen)
		}

		if delay < 250*time.Millisecond {
			delay = 250 * time.Millisecond
		}
		if delay > time.Second {
			delay = time.Second
		}
		timer.Reset(delay)
		fired = timer.C
		select {
		case <-fired:
		case <-s.quit:
			return
		}
	}
}

func (s *Scheduler) run(it *item, gen uint64) {
	defer s.wg.Done()
	select {
	case s.sem <- struct{}{}:
	case <-s.quit:
		return
	}
	defer func() { <-s.sem }()

	// 总预算覆盖全部尝试 + 退避
	budget := it.target.Template.TimeoutD()*time.Duration(it.target.Template.Retry+1) + 15*time.Second
	ctx, cancel := context.WithTimeout(s.ctx, budget)
	defer cancel()

	res := prober.Probe(ctx, it.target)
	if s.gen.Load() != gen {
		return // 热更新后的旧任务，结果作废也不重排
	}
	s.onResult(it.target, res)
	if s.gen.Load() != gen {
		return
	}
	s.mu.Lock()
	heap.Push(&s.h, &item{target: it.target, interval: it.interval, next: time.Now().Add(it.interval)})
	s.mu.Unlock()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
