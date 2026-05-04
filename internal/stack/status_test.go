package stack

import (
	"bytes"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
)

func TestRunStatus_ContainsStackIndicator(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Dir: "./services/main", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig{},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)
	neverRunning := func(_, _ string) bool { return false }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := RunStatus(w, cfg, neverRunning, nil, nil); err != nil {
		t.Fatalf("RunStatus error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Devbox:") {
		t.Errorf("output missing 'Devbox:' indicator\n%s", out)
	}
}

func TestRunStatus_StoppedIndicator(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig{},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)
	neverRunning := func(_, _ string) bool { return false }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := RunStatus(w, cfg, neverRunning, nil, nil); err != nil {
		t.Fatalf("RunStatus error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "○ stopped") {
		t.Errorf("expected '○ stopped' indicator, got:\n%s", out)
	}
}

func TestRunStatus_RunningIndicator(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig{},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)
	alwaysRunning := func(_, _ string) bool { return true }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := RunStatus(w, cfg, alwaysRunning, nil, nil); err != nil {
		t.Fatalf("RunStatus error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "● running") {
		t.Errorf("expected '● running' indicator, got:\n%s", out)
	}
}

func TestRunStatus_PartialIndicator(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main":   {Type: "app", Container: "app-main", Mandatory: true},
			"second": {Type: "app", Container: "app-second", Enabled: true},
		},
		config.ToolsConfig{},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)
	partialRunning := func(_, container string) bool {
		return container == "app-main"
	}

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := RunStatus(w, cfg, partialRunning, nil, nil); err != nil {
		t.Fatalf("RunStatus error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "◐ partial") {
		t.Errorf("expected '◐ partial' indicator, got:\n%s", out)
	}
}

func TestRunStatus_ContainsServiceAndToolTables(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig{
			Adminer: config.ToolConfig{Enabled: true},
		},
		config.RuntimePorts{Adminer: 8080},
		config.RuntimeHosts{Adminer: "adminer.localhost"},
	)
	neverRunning := func(_, _ string) bool { return false }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := RunStatus(w, cfg, neverRunning, nil, nil); err != nil {
		t.Fatalf("RunStatus error: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"main", "adminer"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

// --- topology integration ---

func TestRunStatus_WithTopologyShown(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig{},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)
	neverRunning := func(_, _ string) bool { return false }

	topo := map[string][]string{
		"nginx":    {"app-main"},
		"app-main": {"db"},
		"db":       {},
	}

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := RunStatus(w, cfg, neverRunning, topo, nil); err != nil {
		t.Fatalf("RunStatus error: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"nginx", "app-main", "db"} {
		if !strings.Contains(out, want) {
			t.Errorf("topology output missing %q:\n%s", want, out)
		}
	}
}

func TestRunStatus_TopologyNilSkipped(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig{},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)
	neverRunning := func(_, _ string) bool { return false }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := RunStatus(w, cfg, neverRunning, nil, nil); err != nil {
		t.Fatalf("RunStatus error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "nginx") {
		t.Errorf("unexpected 'nginx' in output when topo is nil:\n%s", out)
	}
}

func TestRunStatus_TopologyWithStatus(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig{},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)
	neverRunning := func(_, _ string) bool { return false }

	topo := map[string][]string{
		"nginx":    {"app-main"},
		"app-main": {},
	}
	topoStatus := map[string]ui.NodeStatus{
		"nginx":    ui.NodeRunning,
		"app-main": ui.NodeStopped,
	}

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := RunStatus(w, cfg, neverRunning, topo, topoStatus); err != nil {
		t.Fatalf("RunStatus error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "running") {
		t.Errorf("expected 'running' status in topology output:\n%s", out)
	}
	if !strings.Contains(out, "stopped") {
		t.Errorf("expected 'stopped' status in topology output:\n%s", out)
	}
}
