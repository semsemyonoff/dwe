package command

import (
	"bytes"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

func makeServicesCfg(services map[string]config.ServiceConfig, tools config.ToolsConfig, ports config.RuntimePorts, hosts config.RuntimeHosts) *config.DevboxConfig {
	return &config.DevboxConfig{
		Services: services,
		Tools:    tools,
		Runtime: config.RuntimeConfig{
			Ports: ports,
			Hosts: hosts,
		},
	}
}

func TestBuildServiceRows_empty(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})
	rows := buildServiceRows(cfg)
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestBuildServiceRows_single(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main": {Type: "app", Dir: "./services/main", Container: "app-main"},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	rows := buildServiceRows(cfg)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Name != "main" {
		t.Errorf("Name = %q, want main", r.Name)
	}
	if r.Type != "app" {
		t.Errorf("Type = %q, want app", r.Type)
	}
	if r.Dir != "./services/main" {
		t.Errorf("Dir = %q, want ./services/main", r.Dir)
	}
	if r.Container != "app-main" {
		t.Errorf("Container = %q, want app-main", r.Container)
	}
}

func TestBuildServiceRows_sortedByName(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"worker": {Type: "worker", Dir: "./services/worker"},
		"api":    {Type: "app", Dir: "./services/api"},
		"main":   {Type: "app", Dir: "./services/main"},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	rows := buildServiceRows(cfg)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	names := []string{rows[0].Name, rows[1].Name, rows[2].Name}
	want := []string{"api", "main", "worker"}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("rows[%d].Name = %q, want %q", i, n, want[i])
		}
	}
}

func TestBuildToolRows_allDisabled(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})
	rows := buildToolRows(cfg)
	if len(rows) != 3 {
		t.Fatalf("expected 3 tool rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Enabled {
			t.Errorf("tool %q should be disabled", r.Name)
		}
	}
}

func TestBuildToolRows_someEnabled(t *testing.T) {
	cfg := makeServicesCfg(nil, config.ToolsConfig{
		Adminer:      config.ToolConfig{Enabled: false},
		RedisInsight: config.ToolConfig{Enabled: true},
		Mailpit:      config.ToolConfig{Enabled: true},
	}, config.RuntimePorts{
		Adminer:      8080,
		RedisInsight: 5540,
		Mailpit:      8025,
	}, config.RuntimeHosts{
		Adminer:      "adminer.localhost",
		RedisInsight: "redis.localhost",
		Mailpit:      "mail.localhost",
	})

	rows := buildToolRows(cfg)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	adminer := rows[0]
	if adminer.Name != "adminer" {
		t.Errorf("rows[0].Name = %q, want adminer", adminer.Name)
	}
	if adminer.Enabled {
		t.Error("adminer should be disabled")
	}
	if adminer.Port != 8080 {
		t.Errorf("adminer.Port = %d, want 8080", adminer.Port)
	}
	if adminer.Host != "adminer.localhost" {
		t.Errorf("adminer.Host = %q, want adminer.localhost", adminer.Host)
	}

	ri := rows[1]
	if ri.Name != "redis_insight" {
		t.Errorf("rows[1].Name = %q, want redis_insight", ri.Name)
	}
	if !ri.Enabled {
		t.Error("redis_insight should be enabled")
	}
	if ri.Port != 5540 {
		t.Errorf("redis_insight.Port = %d, want 5540", ri.Port)
	}

	mp := rows[2]
	if mp.Name != "mailpit" {
		t.Errorf("rows[2].Name = %q, want mailpit", mp.Name)
	}
	if !mp.Enabled {
		t.Error("mailpit should be enabled")
	}
}

func TestRunServiceList_LipglossTable(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Container: "app-main", Mandatory: true},
		"second": {Type: "app", Container: "app-second", Enabled: false},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	neverRunning := func(_, _ string) bool { return false }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := runServiceList(w, cfg, neverRunning); err != nil {
		t.Fatalf("runServiceList error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"NAME", "CONTAINER", "STATE", "RUNNING",
		"main", "app-main", "mandatory", "stopped",
		"second", "app-second", "disabled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRunServiceList_EnabledRunning(t *testing.T) {
	cfg := makeServicesCfg(map[string]config.ServiceConfig{
		"main": {Type: "app", Container: "app-main", Enabled: true},
	}, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})

	alwaysRunning := func(_, _ string) bool { return true }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := runServiceList(w, cfg, alwaysRunning); err != nil {
		t.Fatalf("runServiceList error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "running") {
		t.Errorf("enabled running service should show 'running'\nfull output:\n%s", out)
	}
}
