package stack

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
)

func TestHealthIndicator_Stopped(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Required: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	neverRunning := func(_, _ string) bool { return false }
	out := HealthIndicator(StatusInput{Cfg: cfg, IsRunning: neverRunning})
	if !strings.Contains(out, "○ stopped") {
		t.Errorf("expected '○ stopped' indicator, got: %q", out)
	}
	if strings.Contains(out, "DWE:") {
		t.Errorf("HealthIndicator should not include 'DWE:' prefix, got: %q", out)
	}
}

func TestHealthIndicator_Running(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Required: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	alwaysRunning := func(_, _ string) bool { return true }
	out := HealthIndicator(StatusInput{Cfg: cfg, IsRunning: alwaysRunning})
	if !strings.Contains(out, "● running") {
		t.Errorf("expected '● running' indicator, got: %q", out)
	}
	if strings.Contains(out, "DWE:") {
		t.Errorf("HealthIndicator should not include 'DWE:' prefix, got: %q", out)
	}
}

func TestHealthIndicator_Partial(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main":   {Type: "app", Container: "app-main", Required: true},
			"second": {Type: "app", Container: "app-second", Enabled: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	partialRunning := func(_, container string) bool {
		return container == "app-main"
	}
	out := HealthIndicator(StatusInput{Cfg: cfg, IsRunning: partialRunning})
	if !strings.Contains(out, "◐ partial") {
		t.Errorf("expected '◐ partial' indicator, got: %q", out)
	}
	if strings.Contains(out, "DWE:") {
		t.Errorf("HealthIndicator should not include 'DWE:' prefix, got: %q", out)
	}
}

func TestRenderHealth_ContainsIndicator(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Dir: "./services/main", Container: "app-main", Required: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	neverRunning := func(_, _ string) bool { return false }
	out := RenderHealth(StatusInput{Cfg: cfg, IsRunning: neverRunning})
	if !strings.Contains(out, "DWE:") {
		t.Errorf("output missing 'DWE:' indicator: %q", out)
	}
}

func TestRenderHealth_Stopped(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Required: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	neverRunning := func(_, _ string) bool { return false }
	out := RenderHealth(StatusInput{Cfg: cfg, IsRunning: neverRunning})
	if !strings.Contains(out, "○ stopped") {
		t.Errorf("expected '○ stopped' indicator, got: %q", out)
	}
}

func TestRenderHealth_Running(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Required: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	alwaysRunning := func(_, _ string) bool { return true }
	out := RenderHealth(StatusInput{Cfg: cfg, IsRunning: alwaysRunning})
	if !strings.Contains(out, "● running") {
		t.Errorf("expected '● running' indicator, got: %q", out)
	}
}

func TestRenderHealth_Partial(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main":   {Type: "app", Container: "app-main", Required: true},
			"second": {Type: "app", Container: "app-second", Enabled: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	partialRunning := func(_, container string) bool {
		return container == "app-main"
	}
	out := RenderHealth(StatusInput{Cfg: cfg, IsRunning: partialRunning})
	if !strings.Contains(out, "◐ partial") {
		t.Errorf("expected '◐ partial' indicator, got: %q", out)
	}
}

func TestRenderApps_ContainsServiceName(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Required: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	out, errs := RenderApps(StatusInput{Cfg: cfg, IsRunning: func(_, _ string) bool { return false }})
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if !strings.Contains(out, "main") {
		t.Errorf("apps output missing 'main': %s", out)
	}
	if !strings.Contains(out, "Apps") {
		t.Errorf("apps output missing 'Apps' title: %s", out)
	}
}

func TestRenderInfra_FiltersByType(t *testing.T) {
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"db":   {Type: config.ServiceTypeInfra, Container: "db", Required: true},
			"main": {Type: config.ServiceTypeApp, Container: "app-main", Required: true},
		},
	}
	out, _ := RenderInfra(StatusInput{Cfg: cfg, IsRunning: func(_, _ string) bool { return false }})
	if !strings.Contains(out, "Infra") {
		t.Errorf("infra output missing title: %s", out)
	}
	if !strings.Contains(out, "db") {
		t.Errorf("infra output missing 'db': %s", out)
	}
	if strings.Contains(out, "main") {
		t.Errorf("infra output should NOT contain app 'main': %s", out)
	}
}

func TestRenderTools_ContainsToolName(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{},
		map[string]testTool{
			"adminer": {Enabled: true, Container: "adminer", Host: "adminer.localhost", Port: 8080},
		},
		nil,
		nil,
	)
	out, errs := RenderTools(StatusInput{Cfg: cfg, IsRunning: func(_, _ string) bool { return false }})
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if !strings.Contains(out, "adminer") {
		t.Errorf("tools output missing 'adminer': %s", out)
	}
}

func TestRenderTopology_NilSkipped(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Required: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	out := RenderTopology(StatusInput{Cfg: cfg, IsRunning: func(_, _ string) bool { return false }})
	if out != "" {
		t.Errorf("expected empty topology output when topo is nil, got: %q", out)
	}
}

func TestRenderTopology_WithStatus(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Required: true},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	topo := map[string][]string{
		"nginx":    {"app-main"},
		"app-main": {},
	}
	topoStatus := map[string]render.NodeStatus{
		"nginx":    render.NodeRunning,
		"app-main": render.NodeStopped,
	}
	out := RenderTopology(StatusInput{Cfg: cfg, IsRunning: func(_, _ string) bool { return false }, Topo: topo, TopoStatus: topoStatus})
	if !strings.Contains(out, "running") {
		t.Errorf("expected 'running' status in topology output: %s", out)
	}
	if !strings.Contains(out, "stopped") {
		t.Errorf("expected 'stopped' status in topology output: %s", out)
	}
}

// TestRenderApps_CustomColumnsAggregateError verifies that
// per-row template errors are aggregated into the returned slice.
func TestRenderApps_CustomColumnsAggregateError(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {
				Type:      "app",
				Container: "app-main",
				Required:  true,
				Status: []config.StatusColumn{
					{Name: "X", Value: "{{ this is broken syntax"},
				},
			},
		},
		map[string]testTool(nil),
		nil,
		nil,
	)
	_, errs := RenderApps(StatusInput{Cfg: cfg, IsRunning: func(_, _ string) bool { return false }})
	if len(errs) == 0 {
		t.Errorf("expected template error to be aggregated")
	}
}
