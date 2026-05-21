package stack

import (
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"
)

func TestRenderHealth_ContainsIndicator(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Dir: "./services/main", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig(nil),
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
	)
	neverRunning := func(_, _ string) bool { return false }
	out := RenderHealth(StatusInput{Cfg: cfg, IsRunning: neverRunning})
	if !strings.Contains(out, "Devbox:") {
		t.Errorf("output missing 'Devbox:' indicator: %q", out)
	}
}

func TestRenderHealth_Stopped(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig(nil),
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
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
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig(nil),
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
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
			"main":   {Type: "app", Container: "app-main", Mandatory: true},
			"second": {Type: "app", Container: "app-second", Enabled: true},
		},
		config.ToolsConfig(nil),
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
	)
	partialRunning := func(_, container string) bool {
		return container == "app-main"
	}
	out := RenderHealth(StatusInput{Cfg: cfg, IsRunning: partialRunning})
	if !strings.Contains(out, "◐ partial") {
		t.Errorf("expected '◐ partial' indicator, got: %q", out)
	}
}

func TestRenderServices_ContainsServiceName(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig(nil),
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
	)
	out, errs := RenderServices(StatusInput{Cfg: cfg, IsRunning: func(_, _ string) bool { return false }})
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if !strings.Contains(out, "main") {
		t.Errorf("services output missing 'main': %s", out)
	}
}

func TestRenderTools_ContainsToolName(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{},
		config.ToolsConfig{
			"adminer": {Enabled: true, Container: "adminer", Host: "adminer.localhost", Port: 8080},
		},
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
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
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig(nil),
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
	)
	out := RenderTopology(StatusInput{Cfg: cfg, IsRunning: func(_, _ string) bool { return false }})
	if out != "" {
		t.Errorf("expected empty topology output when topo is nil, got: %q", out)
	}
}

func TestRenderTopology_WithStatus(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig(nil),
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
	)
	topo := map[string][]string{
		"nginx":    {"app-main"},
		"app-main": {},
	}
	topoStatus := map[string]ui.NodeStatus{
		"nginx":    ui.NodeRunning,
		"app-main": ui.NodeStopped,
	}
	out := RenderTopology(StatusInput{Cfg: cfg, IsRunning: func(_, _ string) bool { return false }, Topo: topo, TopoStatus: topoStatus})
	if !strings.Contains(out, "running") {
		t.Errorf("expected 'running' status in topology output: %s", out)
	}
	if !strings.Contains(out, "stopped") {
		t.Errorf("expected 'stopped' status in topology output: %s", out)
	}
}

// TestRenderServices_CustomColumnsAggregateError verifies that
// per-row template errors are aggregated into the returned slice.
func TestRenderServices_CustomColumnsAggregateError(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {
				Type:      "app",
				Container: "app-main",
				Mandatory: true,
				Status: []config.StatusColumn{
					{Name: "X", Value: "{{ this is broken syntax"},
				},
			},
		},
		config.ToolsConfig(nil),
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
	)
	_, errs := RenderServices(StatusInput{Cfg: cfg, IsRunning: func(_, _ string) bool { return false }})
	if len(errs) == 0 {
		t.Errorf("expected template error to be aggregated")
	}
}
