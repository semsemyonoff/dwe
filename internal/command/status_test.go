package command

import (
	"bytes"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
)

// --- aggregateHealth ---

func TestAggregateHealth_AllRunning(t *testing.T) {
	rows := []ui.ServiceTableRow{
		{Name: "main", Mandatory: true, Running: true},
		{Name: "second", Enabled: true, Running: true},
	}
	if got := aggregateHealth(rows); got != StackRunning {
		t.Errorf("aggregateHealth = %d, want StackRunning (%d)", got, StackRunning)
	}
}

func TestAggregateHealth_Partial(t *testing.T) {
	rows := []ui.ServiceTableRow{
		{Name: "main", Mandatory: true, Running: true},
		{Name: "second", Enabled: true, Running: false},
	}
	if got := aggregateHealth(rows); got != StackPartial {
		t.Errorf("aggregateHealth = %d, want StackPartial (%d)", got, StackPartial)
	}
}

func TestAggregateHealth_NoneRunning(t *testing.T) {
	rows := []ui.ServiceTableRow{
		{Name: "main", Mandatory: true, Running: false},
		{Name: "second", Enabled: true, Running: false},
	}
	if got := aggregateHealth(rows); got != StackStopped {
		t.Errorf("aggregateHealth = %d, want StackStopped (%d)", got, StackStopped)
	}
}

func TestAggregateHealth_OnlyDisabled(t *testing.T) {
	// Disabled (non-mandatory, non-enabled) services don't count.
	rows := []ui.ServiceTableRow{
		{Name: "second", Mandatory: false, Enabled: false, Running: false},
	}
	if got := aggregateHealth(rows); got != StackStopped {
		t.Errorf("aggregateHealth = %d, want StackStopped (%d)", got, StackStopped)
	}
}

func TestAggregateHealth_Empty(t *testing.T) {
	if got := aggregateHealth(nil); got != StackStopped {
		t.Errorf("aggregateHealth(nil) = %d, want StackStopped (%d)", got, StackStopped)
	}
}

// --- runStatus ---

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
	if err := runStatus(w, cfg, neverRunning, nil, nil); err != nil {
		t.Fatalf("runStatus error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Stack:") {
		t.Errorf("output missing 'Stack:' indicator\n%s", out)
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
	if err := runStatus(w, cfg, neverRunning, nil, nil); err != nil {
		t.Fatalf("runStatus error: %v", err)
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
	if err := runStatus(w, cfg, alwaysRunning, nil, nil); err != nil {
		t.Fatalf("runStatus error: %v", err)
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
	// only "main" is running
	partialRunning := func(_, container string) bool {
		return container == "app-main"
	}

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := runStatus(w, cfg, partialRunning, nil, nil); err != nil {
		t.Fatalf("runStatus error: %v", err)
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
	if err := runStatus(w, cfg, neverRunning, nil, nil); err != nil {
		t.Fatalf("runStatus error: %v", err)
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
	if err := runStatus(w, cfg, neverRunning, topo, nil); err != nil {
		t.Fatalf("runStatus error: %v", err)
	}
	out := buf.String()

	// Topology services should appear in output.
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
	// topo=nil means no topology section — must not error.
	if err := runStatus(w, cfg, neverRunning, nil, nil); err != nil {
		t.Fatalf("runStatus error: %v", err)
	}
	// nginx and db come from topology, not config — should not appear.
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
	if err := runStatus(w, cfg, neverRunning, topo, topoStatus); err != nil {
		t.Fatalf("runStatus error: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "running") {
		t.Errorf("expected 'running' status in topology output:\n%s", out)
	}
	if !strings.Contains(out, "stopped") {
		t.Errorf("expected 'stopped' status in topology output:\n%s", out)
	}
}
