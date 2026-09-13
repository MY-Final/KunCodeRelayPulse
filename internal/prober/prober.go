// Package prober 执行一次探测：渲染模板 → 发请求 → 内容校验 → status/sub_status 判级。
// 状态：0=红（故障） 1=绿（正常） 2=黄（慢但可用）。
package prober

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"kuncode-relay-pulse/internal/probetpl"
)

const (
	StatusRed    = 0
	StatusGreen  = 1
	StatusYellow = 2
)

// 探测响应最多读 1MiB，防异常响应吃内存。
const maxBodyBytes = 1 << 20

type Target struct {
	ChannelID   string
	Provider    string
	ChannelName string
	Model       string // 通道级探测（无模型概念）时为空串
	Template    *probetpl.Template
	BaseURL     string
	APIKey      string
	ProxyURL    string        // 为空时使用环境代理；非空时使用该目标指定的代理
	Interval    time.Duration // 0 = 调度器用全局默认
}

type Result struct {
	ChannelID string
	Model     string
	Status    int
	SubStatus string
	HTTPCode  int
	LatencyMS int64
	Error     string
	TS        int64
}

var defaultClient = newHTTPClient(http.ProxyFromEnvironment)
var clientCache sync.Map // map[string]*http.Client，key 为代理 URL

// Probe 带重试执行探测。只在可自动恢复的失败上重试（4xx 不重试）。
func Probe(ctx context.Context, t Target) Result {
	res := Result{ChannelID: t.ChannelID, Model: t.Model, Status: StatusRed}
	client, err := clientForProxy(t.ProxyURL)
	if err != nil {
		res.SubStatus, res.Error = "invalid_proxy", err.Error()
		res.TS = time.Now().Unix()
		return res
	}
	attempts := t.Template.Retry + 1
	for i := 0; i < attempts; i++ {
		if i > 0 {
			backoff := time.Duration(1<<(i-1)) * time.Second
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
			backoff += time.Duration(rand.IntN(500)) * time.Millisecond
			select {
			case <-ctx.Done():
				res.SubStatus, res.Error = "canceled", "探测被取消"
				res.TS = time.Now().Unix()
				return res
			case <-time.After(backoff):
			}
		}
		status, sub, errMsg, code, latency := doOnce(ctx, t, client)
		res.Status, res.SubStatus, res.Error, res.HTTPCode, res.LatencyMS = status, sub, errMsg, code, latency
		if !retryable(sub) {
			break
		}
	}
	res.TS = time.Now().Unix()
	return res
}

func retryable(sub string) bool {
	switch sub {
	case "network_error", "timeout", "upstream_error", "content_mismatch":
		return true
	}
	return false
}

func doOnce(ctx context.Context, t Target, client *http.Client) (status int, sub, errMsg string, httpCode int, latencyMS int64) {
	start := time.Now()
	url, headers, body := t.Template.Render(t.BaseURL, t.APIKey, t.Model)

	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, t.Template.Method, url, rd)
	if err != nil {
		return StatusRed, "invalid_request", fmt.Sprintf("构造请求失败: %v", err), 0, 0
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "pulse-prober/0.1")
	}

	cctx, cancel := context.WithTimeout(ctx, t.Template.TimeoutD())
	defer cancel()
	resp, err := client.Do(req.WithContext(cctx))
	latency := time.Since(start).Milliseconds()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeoutErr(err) {
			return StatusRed, "timeout", "请求超时", 0, latency
		}
		if errors.Is(err, context.Canceled) {
			return StatusRed, "canceled", "探测被取消", 0, latency
		}
		return StatusRed, "network_error", err.Error(), 0, latency
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	latency = time.Since(start).Milliseconds()
	code := resp.StatusCode

	if code >= 200 && code < 300 {
		want := t.Template.SuccessContains
		if want == "" || strings.Contains(string(respBody), want) {
			if slow := t.Template.SlowD(); slow > 0 && latency > slow.Milliseconds() {
				return StatusYellow, "slow", "", code, latency
			}
			return StatusGreen, "ok", "", code, latency
		}
		return StatusRed, "content_mismatch", fmt.Sprintf("HTTP 200 但响应未包含 %q", want), code, latency
	}
	switch {
	case code >= 400 && code < 500:
		return StatusRed, "invalid_request", fmt.Sprintf("HTTP %d: %s", code, truncate(string(respBody), 200)), code, latency
	case code >= 500:
		return StatusRed, "upstream_error", fmt.Sprintf("HTTP %d: %s", code, truncate(string(respBody), 200)), code, latency
	default:
		return StatusRed, "http_error", fmt.Sprintf("HTTP %d", code), code, latency
	}
}

func newHTTPClient(proxyFunc func(*http.Request) (*url.URL, error)) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               proxyFunc,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
		},
		// 不设整体超时，每次尝试用 ctx 控制模板 timeout。
	}
}

func clientForProxy(raw string) (*http.Client, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultClient, nil
	}
	if cached, ok := clientCache.Load(raw); ok {
		return cached.(*http.Client), nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("代理 URL 无效")
	}
	var client *http.Client
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		transport := newHTTPClient(nil).Transport.(*http.Transport)
		transport.Proxy = http.ProxyURL(u)
		client = &http.Client{Transport: transport}
	case "socks5", "socks5h":
		forward := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		dialer, err := proxy.FromURL(u, forward)
		if err != nil {
			return nil, errors.New("SOCKS5 代理配置无效")
		}
		transport := newHTTPClient(nil).Transport.(*http.Transport)
		if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
			transport.DialContext = contextDialer.DialContext
		} else {
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return dialContext(ctx, dialer, network, address)
			}
		}
		client = &http.Client{Transport: transport}
	default:
		return nil, errors.New("代理协议不支持")
	}
	actual, _ := clientCache.LoadOrStore(raw, client)
	return actual.(*http.Client), nil
}

func dialContext(ctx context.Context, dialer proxy.Dialer, network, address string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		conn, err := dialer.Dial(network, address)
		resultCh <- result{conn: conn, err: err}
	}()
	select {
	case result := <-resultCh:
		return result.conn, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func isTimeoutErr(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
