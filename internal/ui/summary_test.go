package ui

import (
	"strings"
	"testing"

	"devbox-cli/internal/config"
)

func TestRenderSummary_ProjectName(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "laravel", Prefix: "devbox"},
	}
	out := RenderSummary(cfg)
	if !strings.Contains(out, "devbox-laravel") {
		t.Errorf("expected project full name in summary, got:\n%s", out)
	}
}

func TestRenderSummary_State(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		State:   "running",
	}
	out := RenderSummary(cfg)
	if !strings.Contains(out, "running") {
		t.Errorf("expected state in summary, got:\n%s", out)
	}
}

func TestRenderSummary_NoState(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	out := RenderSummary(cfg)
	// "state" label should not appear when state is empty
	if strings.Contains(out, "state") {
		t.Errorf("did not expect 'state' label when state is empty, got:\n%s", out)
	}
}

func TestRenderSummary_URL_HTTP(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		Runtime: config.RuntimeConfig{
			UseHTTPS: false,
			Hosts:    config.RuntimeHosts{Main: "myapp.localhost"},
		},
	}
	out := RenderSummary(cfg)
	if !strings.Contains(out, "http://myapp.localhost") {
		t.Errorf("expected http URL in summary, got:\n%s", out)
	}
}

func TestRenderSummary_URL_HTTPS(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		Runtime: config.RuntimeConfig{
			UseHTTPS: true,
			Hosts:    config.RuntimeHosts{Main: "myapp.localhost"},
		},
	}
	out := RenderSummary(cfg)
	if !strings.Contains(out, "https://myapp.localhost") {
		t.Errorf("expected https URL in summary, got:\n%s", out)
	}
}

func TestRenderSummary_NoURL_WhenHostEmpty(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	out := RenderSummary(cfg)
	if strings.Contains(out, "http://") || strings.Contains(out, "https://") {
		t.Errorf("did not expect URL when host is empty, got:\n%s", out)
	}
}

func TestRenderSummary_ServiceCounts(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		Services: map[string]config.ServiceConfig{
			"main":   {Enabled: true},
			"second": {Enabled: false},
		},
	}
	out := RenderSummary(cfg)
	if !strings.Contains(out, "1/2") {
		t.Errorf("expected '1/2' service count in summary, got:\n%s", out)
	}
}

func TestRenderSummary_ToolCounts(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		Tools: config.ToolsConfig{
			Adminer:      config.ToolConfig{Enabled: true},
			RedisInsight: config.ToolConfig{Enabled: false},
			Mailpit:      config.ToolConfig{Enabled: true},
		},
	}
	out := RenderSummary(cfg)
	if !strings.Contains(out, "2 enabled") {
		t.Errorf("expected '2 enabled' tools in summary, got:\n%s", out)
	}
}

func TestRenderSummary_TwoLines(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	out := RenderSummary(cfg)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Errorf("expected exactly 2 lines in summary, got %d:\n%s", len(lines), out)
	}
}
