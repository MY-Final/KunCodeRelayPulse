package main

import (
	"strings"
	"testing"

	"kuncode-relay-pulse/internal/config"
)

func TestReloadConfigCheckRejectsStartupOnlyChanges(t *testing.T) {
	base := &config.Config{
		Listen: "127.0.0.1:8080", SQLitePath: "db",
		ChannelsDir: "channels", TemplatesDir: "templates", MaxConcurrency: 8,
	}
	next := *base
	next.MaxConcurrency = 16
	next.Listen = "127.0.0.1:9090"

	err := reloadConfigCheck(base, &next)
	if err == nil || !strings.Contains(err.Error(), "max_concurrency") || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("reload check error = %v", err)
	}
}

func TestReloadConfigCheckAllowsRuntimeChanges(t *testing.T) {
	base := &config.Config{Listen: "127.0.0.1:8080", SQLitePath: "db", ChannelsDir: "channels", TemplatesDir: "templates", MaxConcurrency: 8}
	next := *base
	next.SiteTitle = "new title"
	next.EventThreshold = 3
	if err := reloadConfigCheck(base, &next); err != nil {
		t.Fatalf("runtime config should reload: %v", err)
	}
}
