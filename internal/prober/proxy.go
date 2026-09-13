package prober

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const proxyTestURL = "https://www.google.com/generate_204"

// ProxyTestResult 是管理页测试代理出口时返回的最小结果集。
type ProxyTestResult struct {
	OK        bool
	HTTPCode  int
	LatencyMS int64
	Error     string
}

// TestProxy 通过指定代理访问 Google 的无内容探测地址，验证代理是否真的可用。
func TestProxy(ctx context.Context, proxyURL string) ProxyTestResult {
	return testProxyURL(ctx, proxyURL, proxyTestURL)
}

func testProxyURL(ctx context.Context, proxyURL, targetURL string) ProxyTestResult {
	result := ProxyTestResult{}
	client, err := clientForProxy(proxyURL)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	testCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	start := time.Now()
	req, err := http.NewRequestWithContext(testCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		result.Error = fmt.Sprintf("构造测试请求失败: %v", err)
		return result
	}
	req.Header.Set("User-Agent", "pulse-proxy-test/0.1")
	resp, err := client.Do(req)
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	result.HTTPCode = resp.StatusCode
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		result.Error = fmt.Sprintf("目标地址返回 HTTP %d", resp.StatusCode)
		return result
	}
	result.OK = true
	return result
}
