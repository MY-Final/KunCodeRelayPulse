package prober

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kuncode-relay-pulse/internal/probetpl"
)

func tpl(url, contains string, modify func(*probetpl.Template)) Target {
	t := &probetpl.Template{
		Name:            "test",
		URL:             url,
		Method:          "GET",
		SuccessContains: contains,
		Timeout:         "2s",
	}
	if modify != nil {
		modify(t)
	}
	return Target{ChannelID: "ch_test", Model: "", Template: t}
}

func TestProbeGreen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"text":"PONG"}`))
	}))
	defer srv.Close()
	res := Probe(context.Background(), tpl(srv.URL+"/x", "PONG", nil))
	if res.Status != StatusGreen || res.SubStatus != "ok" {
		t.Fatalf("want green/ok, got %d/%s (%s)", res.Status, res.SubStatus, res.Error)
	}
}

func TestProbeContentMismatch(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"text":"garbage"}`))
	}))
	defer srv.Close()
	res := Probe(context.Background(), tpl(srv.URL+"/x", "PONG", func(t *probetpl.Template) { t.Retry = 1 }))
	if res.Status != StatusRed || res.SubStatus != "content_mismatch" {
		t.Fatalf("want red/content_mismatch, got %d/%s", res.Status, res.SubStatus)
	}
	if hits != 2 {
		t.Fatalf("content_mismatch 应重试一次，实际请求数 %d", hits)
	}
}

func TestProbeYellow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(60 * time.Millisecond)
		w.Write([]byte(`ok`))
	}))
	defer srv.Close()
	res := Probe(context.Background(), tpl(srv.URL+"/x", "ok", func(t *probetpl.Template) { t.SlowLatency = "10ms" }))
	if res.Status != StatusYellow || res.SubStatus != "slow" {
		t.Fatalf("want yellow/slow, got %d/%s", res.Status, res.SubStatus)
	}
}

func TestProbeTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte(`ok`))
	}))
	defer srv.Close()
	res := Probe(context.Background(), tpl(srv.URL+"/x", "ok", func(t *probetpl.Template) { t.Timeout = "50ms" }))
	if res.Status != StatusRed || res.SubStatus != "timeout" {
		t.Fatalf("want red/timeout, got %d/%s", res.Status, res.SubStatus)
	}
}

func TestProbeUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
	}))
	defer srv.Close()
	res := Probe(context.Background(), tpl(srv.URL+"/x", "ok", func(t *probetpl.Template) { t.Retry = 1 }))
	if res.Status != StatusYellow || res.SubStatus != "upstream_error" {
		t.Fatalf("want yellow/upstream_error, got %d/%s", res.Status, res.SubStatus)
	}
}

func TestProbeInvalidRequestNoRetry(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(401)
	}))
	defer srv.Close()
	res := Probe(context.Background(), tpl(srv.URL+"/x", "ok", func(t *probetpl.Template) { t.Retry = 3 }))
	if res.Status != StatusRed || res.SubStatus != "invalid_request" {
		t.Fatalf("want red/invalid_request, got %d/%s", res.Status, res.SubStatus)
	}
	if hits != 1 {
		t.Fatalf("4xx 不应重试，实际请求数 %d", hits)
	}
}

func TestProbeNetworkError(t *testing.T) {
	// 192.0.2.0/24 是 TEST-NET-1，保证不可达
	tg := tpl("http://192.0.2.1:1/x", "ok", func(t *probetpl.Template) { t.Timeout = "1s"; t.Retry = 1 })
	res := Probe(context.Background(), tg)
	if res.Status != StatusRed || (res.SubStatus != "network_error" && res.SubStatus != "timeout") {
		t.Fatalf("want red/network_error|timeout, got %d/%s", res.Status, res.SubStatus)
	}
}

func TestProbeUsesTargetHTTPProxy(t *testing.T) {
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Scheme != "http" || r.URL.Host != "upstream.example" || r.URL.Path != "/health" {
			t.Errorf("proxy received URL = %s", r.URL)
		}
		w.Write([]byte(`PONG`))
	}))
	defer proxyServer.Close()

	target := tpl("http://upstream.example/health", "PONG", nil)
	target.ProxyURL = proxyServer.URL
	res := Probe(context.Background(), target)
	if res.Status != StatusGreen || res.SubStatus != "ok" {
		t.Fatalf("want green/ok through proxy, got %d/%s (%s)", res.Status, res.SubStatus, res.Error)
	}
}

func TestTemplateThresholdsOverrideGlobalDefaults(t *testing.T) {
	template := &probetpl.Template{Timeout: "80ms", SlowLatency: "10ms"}
	target := Target{Template: template, ProbeTimeout: 2 * time.Second, ProbeTimeoutSet: true, SlowLatency: 5 * time.Second, SlowLatencySet: true}
	if got := target.TimeoutD(); got != 80*time.Millisecond {
		t.Fatalf("template timeout = %s, want 80ms", got)
	}
	if got := target.SlowD(); got != 10*time.Millisecond {
		t.Fatalf("template slow latency = %s, want 10ms", got)
	}

	template.Timeout = ""
	template.SlowLatency = "0s"
	if got := target.TimeoutD(); got != 2*time.Second {
		t.Fatalf("global timeout = %s, want 2s", got)
	}
	if got := target.SlowD(); got != 0 {
		t.Fatalf("explicit template zero slow latency = %s, want 0", got)
	}
}

func TestProbeRejectsUnsupportedTargetProxy(t *testing.T) {
	target := tpl("http://upstream.example/health", "PONG", nil)
	target.ProxyURL = "ftp://proxy.example:21"
	res := Probe(context.Background(), target)
	if res.Status != StatusRed || res.SubStatus != "invalid_proxy" {
		t.Fatalf("want red/invalid_proxy, got %d/%s", res.Status, res.SubStatus)
	}
}

func TestTestProxyThroughHTTPProxy(t *testing.T) {
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer targetServer.Close()

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Scheme != "http" || r.URL.Host == "" {
			t.Errorf("proxy received URL = %s", r.URL)
		}
		transport := http.DefaultTransport
		outgoing, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		outgoing.Header = r.Header.Clone()
		resp, err := transport.RoundTrip(outgoing)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxyServer.Close()

	result := testProxyURL(context.Background(), proxyServer.URL, targetServer.URL)
	if !result.OK || result.HTTPCode != http.StatusNoContent {
		t.Fatalf("want successful proxy test, got %#v", result)
	}
}
