// pulse —— 单二进制的 API 服务状态监测。
//
//	pulse -config config.yaml   启动服务
//	pulse hashpass <密码>       生成 bcrypt 密码哈希（填入 config.yaml 的 admin.password_hash）
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"kuncode-relay-pulse/internal/config"
	"kuncode-relay-pulse/internal/detector"
	"kuncode-relay-pulse/internal/notify"
	"kuncode-relay-pulse/internal/prober"
	"kuncode-relay-pulse/internal/probetpl"
	"kuncode-relay-pulse/internal/reload"
	"kuncode-relay-pulse/internal/scheduler"
	"kuncode-relay-pulse/internal/store"
	"kuncode-relay-pulse/internal/web"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[pulse] ")

	if len(os.Args) >= 2 && os.Args[1] == "hashpass" {
		runHashpass()
		return
	}

	cfgPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	// 目录不存在就创建，保证空仓库首次启动可用
	for _, d := range []string{cfg.ChannelsDir, cfg.ProxiesDir, cfg.TemplatesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatalf("创建目录 %s: %v", d, err)
		}
	}

	st, err := store.Open(cfg.SQLitePath)
	if err != nil {
		log.Fatalf("打开 SQLite 失败: %v", err)
	}
	defer st.Close()

	templates, err := probetpl.LoadTemplates(cfg.TemplatesDir)
	if err != nil {
		log.Fatalf("加载探针模板失败: %v", err)
	}
	channels, err := config.LoadChannels(cfg.ChannelsDir)
	if err != nil {
		log.Fatalf("加载通道配置失败: %v", err)
	}
	proxies, err := config.LoadProxies(cfg.ProxiesDir)
	if err != nil {
		log.Fatalf("加载代理配置失败: %v", err)
	}
	targets, err := buildTargets(channels, templates, proxies, cfg)
	if err != nil {
		log.Fatalf("构建探测目标失败: %v", err)
	}
	log.Printf("配置加载完成：%d 个通道 / %d 个探测目标", len(channels), len(targets))

	det := detector.New(cfg.EventThreshold, cfg.RecoveryThreshold)
	detectorStates, err := st.LoadDetectorStates()
	if err != nil {
		log.Fatalf("加载事件状态失败: %v", err)
	}
	det.Restore(detectorStates)

	var notifPtr atomic.Pointer[notify.Notifier]
	n := notify.FromConfig(cfg.Notify)
	notifPtr.Store(&n)
	var resultMu sync.Mutex

	// onResult 是探测结果的单一流水线：事件检测 → 结果/状态原子落库 → 通知
	onResult := func(t prober.Target, res prober.Result) {
		resultMu.Lock()
		defer resultMu.Unlock()
		row := store.ProbeRow{
			ChannelID: res.ChannelID, Model: res.Model,
			Status: res.Status, SubStatus: res.SubStatus,
			HTTPCode: res.HTTPCode, LatencyMS: res.LatencyMS,
			TS: res.TS, ErrorDetail: res.Error,
		}
		events := det.Observe(res.ChannelID, res.Model, res.Status, res.TS)
		for i := range events {
			ev := &events[i]
			if ev.Type == "down" {
				ev.Detail = res.SubStatus + ": " + res.Error
			}
		}
		state := det.Snapshot(res.ChannelID, res.Model, res.TS)
		if err := st.RecordProbe(row, events, state); err != nil {
			log.Printf("[store] 写入探测结果失败: %v", err)
			return
		}
		for _, ev := range events {
			log.Printf("[event] %s %s/%s %s", ev.Type, t.Provider, t.ChannelName, t.Model)
			p := notify.Payload{
				Type: ev.Type, Provider: t.Provider, Channel: t.ChannelName,
				Model: t.Model, Detail: ev.Detail, TS: ev.TS,
			}
			if nf := *notifPtr.Load(); nf != nil {
				go nf.Notify(p)
			}
		}
	}

	sched := scheduler.New(cfg.MaxConcurrency, onResult)

	state := atomic.Pointer[web.Snapshot]{}
	state.Store(&web.Snapshot{Cfg: cfg, Channels: channels, Targets: targets})

	ready := reload.NewReady()
	var reloadMu sync.Mutex
	reloadConfig := func() error {
		reloadMu.Lock()
		defer reloadMu.Unlock()
		nc, err := config.Load(*cfgPath)
		if err != nil {
			log.Printf("[reload] 新配置无效，保留旧配置继续运行: %v", err)
			ready.Fail(err)
			return err
		}
		if err := reloadConfigCheck(state.Load().Cfg, nc); err != nil {
			log.Printf("[reload] 配置需要重启，保留旧配置继续运行: %v", err)
			ready.Fail(err)
			return err
		}
		nt, err := probetpl.LoadTemplates(nc.TemplatesDir)
		if err != nil {
			log.Printf("[reload] 新模板无效，保留旧配置继续运行: %v", err)
			ready.Fail(err)
			return err
		}
		nch, err := config.LoadChannels(nc.ChannelsDir)
		if err != nil {
			log.Printf("[reload] 新通道配置无效，保留旧配置继续运行: %v", err)
			ready.Fail(err)
			return err
		}
		npx, err := config.LoadProxies(nc.ProxiesDir)
		if err != nil {
			log.Printf("[reload] 新代理配置无效，保留旧配置继续运行: %v", err)
			ready.Fail(err)
			return err
		}
		ntg, err := buildTargets(nch, nt, npx, nc)
		if err != nil {
			log.Printf("[reload] 构建探测目标失败，保留旧配置继续运行: %v", err)
			ready.Fail(err)
			return err
		}
		// 全部校验通过才切换（fail-closed）
		nn := notify.FromConfig(nc.Notify)
		notifPtr.Store(&nn)
		det.SetThreshold(nc.EventThreshold)
		det.SetRecoveryThreshold(nc.RecoveryThreshold)
		state.Store(&web.Snapshot{Cfg: nc, Channels: nch, Targets: ntg})
		sched.Restart(ntg, nc.Interval.D())
		ready.OK()
		log.Printf("[reload] 热更新完成：%d 个通道 / %d 个探测目标", len(nch), len(ntg))
		return nil
	}
	onChange := func() { _ = reloadConfig() }
	watcher, err := reload.New([]string{filepath.Dir(*cfgPath), cfg.ChannelsDir, cfg.ProxiesDir, cfg.TemplatesDir},
		200*time.Millisecond, onChange)
	if err != nil {
		log.Fatalf("启动配置监听失败: %v", err)
	}
	defer watcher.Close()

	srv := web.New(func() *web.Snapshot { return state.Load() }, st, ready)
	srv.SetReloadSettings(reloadConfig)
	srv.SetManualProbe(
		func(ctx context.Context, target prober.Target) prober.Result {
			return prober.Probe(ctx, target)
		},
		onResult,
	)
	srv.SetResetChannel(func(channelID string) (int64, error) {
		resultMu.Lock()
		defer resultMu.Unlock()
		deleted, err := st.ResetChannel(channelID)
		if err != nil {
			return deleted, err
		}
		det.ResetChannel(channelID)
		return deleted, nil
	})
	httpServer := &http.Server{Addr: cfg.Listen, Handler: srv.Handler()}

	sched.Start(targets, cfg.Interval.D())

	// 保留期清理，每 6 小时一次（保留天数跟随当前配置）
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for range t.C {
			days := state.Load().Cfg.RetentionDays
			if n, err := st.Cleanup(days); err != nil {
				log.Printf("[store] 保留期清理失败: %v", err)
			} else if n > 0 {
				log.Printf("[store] 清理了 %d 条过期记录（保留 %d 天）", n, days)
			}
		}
	}()

	go func() {
		log.Printf("服务启动：http://%s", cfg.Listen)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务失败: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Println("正在退出…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(shutdownCtx)
	sched.Stop()
}

// buildTargets 把通道 × 模型展开成探测目标。引用不存在的模板或代理直接报错（fail-closed）。
func buildTargets(channels []*config.Channel, templates map[string]*probetpl.Template, proxies []*config.Proxy, configs ...*config.Config) ([]prober.Target, error) {
	var cfg *config.Config
	if len(configs) > 0 {
		cfg = configs[0]
	}
	proxyByID := make(map[string]*config.Proxy, len(proxies))
	for _, p := range proxies {
		proxyByID[p.ID] = p
	}
	var out []prober.Target
	for _, ch := range channels {
		if ch.Disabled {
			continue
		}
		tpl, ok := templates[ch.Template]
		if !ok {
			return nil, fmt.Errorf("通道 %s/%s 引用了不存在的模板 %q", ch.Provider, ch.Name, ch.Template)
		}
		proxyURL := ""
		if ch.Proxy != "" {
			p, ok := proxyByID[ch.Proxy]
			if !ok {
				return nil, fmt.Errorf("通道 %s/%s 引用了不存在的代理 %q", ch.Provider, ch.Name, ch.Proxy)
			}
			proxyURL = p.URL
		}
		models := ch.Models
		if len(models) == 0 {
			models = []string{""} // 无模型概念的通道（如 GET 健康检查）
		}
		for _, m := range models {
			target := prober.Target{
				ChannelID: ch.ID, Provider: ch.Provider, ChannelName: ch.Name,
				Model: m, Template: tpl,
				BaseURL: ch.BaseURL, APIKey: ch.APIKeyResolved(),
				ProxyURL: proxyURL,
				Interval: ch.Interval.D(),
			}
			if cfg != nil {
				target.ProbeTimeout = cfg.ProbeTimeout.D()
				target.ProbeTimeoutSet = cfg.ProbeTimeout.D() > 0
				target.SlowLatency = cfg.SlowLatency.D()
				target.SlowLatencySet = true
			}
			out = append(out, target)
		}
	}
	return out, nil
}

func reloadConfigCheck(old, next *config.Config) error {
	var changed []string
	if old.Listen != next.Listen {
		changed = append(changed, "listen")
	}
	if old.SQLitePath != next.SQLitePath {
		changed = append(changed, "sqlite_path")
	}
	if old.ChannelsDir != next.ChannelsDir {
		changed = append(changed, "channels_dir")
	}
	if old.ProxiesDir != next.ProxiesDir {
		changed = append(changed, "proxies_dir")
	}
	if old.TemplatesDir != next.TemplatesDir {
		changed = append(changed, "templates_dir")
	}
	if old.MaxConcurrency != next.MaxConcurrency {
		changed = append(changed, "max_concurrency")
	}
	if len(changed) > 0 {
		return fmt.Errorf("%s 修改需要重启", strings.Join(changed, ", "))
	}
	return nil
}

func runHashpass() {
	var pw string
	if len(os.Args) >= 3 {
		pw = os.Args[2]
	} else {
		fmt.Print("输入密码: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		pw = line
	}
	if pw == "" {
		fmt.Fprintln(os.Stderr, "密码不能为空")
		os.Exit(1)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(h))
}
