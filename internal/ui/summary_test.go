package ui

import (
	"strings"
	"testing"

	"devbox-cli/internal/command/statusview"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/deploy/journal"
)

func TestRenderSummary_OmitsProjectIdentity(t *testing.T) {
	// Project identity has moved to RenderBrandHeader. The summary itself must
	// no longer contain the project name nor a "project —" label.
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "laravel", Prefix: "devbox"},
	}
	out := RenderSummary(cfg, nil)
	if strings.Contains(out, "devbox-laravel") {
		t.Errorf("did not expect project full name in summary, got:\n%s", out)
	}
	if strings.Contains(out, "project ") {
		t.Errorf("did not expect 'project' label in summary, got:\n%s", out)
	}
}

func TestRenderSummary_State(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		State:   "running",
	}
	out := RenderSummary(cfg, nil)
	if !strings.Contains(out, "running") {
		t.Errorf("expected state in summary, got:\n%s", out)
	}
}

func TestRenderSummary_NoState(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	out := RenderSummary(cfg, nil)
	// "state" label should not appear when state is empty
	if strings.Contains(out, "state") {
		t.Errorf("did not expect 'state' label when state is empty, got:\n%s", out)
	}
}

func TestRenderSummary_NoURL(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		Runtime: config.RuntimeConfig{
			UseHTTPS: false,
		},
		Services: map[string]config.ServiceConfig{
			"main": {Type: config.ServiceTypeApp, Hosts: map[string]string{"main": "myapp.localhost"}},
		},
	}
	out := RenderSummary(cfg, nil)
	// URL must not appear in the compact summary.
	if strings.Contains(out, "http://") || strings.Contains(out, "https://") {
		t.Errorf("did not expect URL in compact summary, got:\n%s", out)
	}
}

func TestRenderSummary_ServiceCounts(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		Services: map[string]config.ServiceConfig{
			"main":   {Type: config.ServiceTypeApp, Enabled: true},
			"second": {Type: config.ServiceTypeApp, Enabled: false},
		},
	}
	out := RenderSummary(cfg, nil)
	if !strings.Contains(out, "1/2") {
		t.Errorf("expected '1/2' service count in summary, got:\n%s", out)
	}
}

func TestRenderSummary_MandatoryCountsAsEnabled(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		Services: map[string]config.ServiceConfig{
			"main":   {Type: config.ServiceTypeApp, Required: true},
			"second": {Type: config.ServiceTypeApp, Enabled: true},
			"third":  {Type: config.ServiceTypeApp, Enabled: false},
		},
	}
	out := RenderSummary(cfg, nil)
	if !strings.Contains(out, "2/3") {
		t.Errorf("expected '2/3' service count (mandatory counts as enabled), got:\n%s", out)
	}
}

// TestRenderSummary_ServiceCountIsAppOnly verifies that tools and infra are
// excluded from the "services N/M enabled" count — only apps are reported.
func TestRenderSummary_ServiceCountIsAppOnly(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		Services: map[string]config.ServiceConfig{
			"web":     {Type: config.ServiceTypeApp, Enabled: true},
			"worker":  {Type: config.ServiceTypeApp, Enabled: false},
			"db":      {Type: config.ServiceTypeInfra, Required: true},
			"redis":   {Type: config.ServiceTypeInfra, Required: true},
			"adminer": {Type: config.ServiceTypeTool, Enabled: true},
		},
	}
	out := RenderSummary(cfg, nil)
	if !strings.Contains(out, "services 1/2 enabled") {
		t.Errorf("expected 'services 1/2 enabled' (apps only), got:\n%s", out)
	}
	// Infra services must not bleed into the apps counter.
	if strings.Contains(out, "1/4") || strings.Contains(out, "3/5") {
		t.Errorf("infra/tool services must not affect the app counter, got:\n%s", out)
	}
}

func TestRenderSummary_ToolCounts(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		Services: map[string]config.ServiceConfig{
			"adminer":       {Type: config.ServiceTypeTool, Enabled: true},
			"redis_insight": {Type: config.ServiceTypeTool, Enabled: false},
			"mailpit":       {Type: config.ServiceTypeTool, Enabled: true},
		},
	}
	out := RenderSummary(cfg, nil)
	if !strings.Contains(out, "2 enabled") {
		t.Errorf("expected '2 enabled' tools in summary, got:\n%s", out)
	}
}

func TestRenderSummary_OneLineWhenNoState(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	out := RenderSummary(cfg, nil)
	lines := strings.Split(out, "\n")
	if len(lines) != 1 {
		t.Errorf("expected exactly 1 line in summary (counts only), got %d:\n%s", len(lines), out)
	}
}

func TestRenderSummary_TwoLinesWithState(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
		State:   "running",
	}
	out := RenderSummary(cfg, nil)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Errorf("expected exactly 2 lines in summary (state + counts), got %d:\n%s", len(lines), out)
	}
}

func TestRenderSummary_NilTools(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project:  config.ProjectConfig{Name: "myapp"},
		Services: nil,
	}
	out := RenderSummary(cfg, nil)
	if !strings.Contains(out, "tools 0 enabled") {
		t.Errorf("expected 'tools 0 enabled' when tools are nil, got:\n%s", out)
	}
}

func TestRenderSummary_WithDeploySummaryNil(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	out := RenderSummary(cfg, nil)
	// Should not show "deployed" when summary is nil
	if strings.Contains(out, "deployed") {
		t.Errorf("did not expect 'deployed' in summary when nil, got:\n%s", out)
	}
}

func TestRenderSummary_WithDeploySummary_AllDeployed(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	summary := &statusview.DeploySummary{
		Deployed:      2,
		Total:         2,
		ProjectStatus: journal.StatusDeployed,
	}
	out := RenderSummary(cfg, summary)
	if !strings.Contains(out, "services 2/2 deployed") {
		t.Errorf("expected 'services 2/2 deployed' in summary, got:\n%s", out)
	}
}

func TestRenderSummary_WithDeploySummary_PartiallyDeployed(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	summary := &statusview.DeploySummary{
		Deployed:      1,
		Total:         3,
		ProjectStatus: journal.StatusPartial,
	}
	out := RenderSummary(cfg, summary)
	if !strings.Contains(out, "services 1/3 deployed") {
		t.Errorf("expected 'services 1/3 deployed' in summary, got:\n%s", out)
	}
}

func TestRenderSummary_WithDeploySummary_ZeroTotal(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp"},
	}
	summary := &statusview.DeploySummary{
		Deployed:      0,
		Total:         0,
		ProjectStatus: journal.StatusNotDeployed,
	}
	out := RenderSummary(cfg, summary)
	// When total is 0, deploy summary should not appear
	if strings.Contains(out, "deployed") {
		t.Errorf("did not expect 'deployed' in summary when total is 0, got:\n%s", out)
	}
}
