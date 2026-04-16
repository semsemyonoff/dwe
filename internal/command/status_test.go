package command

import (
	"bytes"
	"os"
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

// --- aggregateHealthFromTopo ---

func TestAggregateHealthFromTopo_AllRunning(t *testing.T) {
	topo := map[string]ui.NodeStatus{
		"nginx":    ui.NodeRunning,
		"app-main": ui.NodeRunning,
		"db":       ui.NodeRunning,
		"redis":    ui.NodeRunning,
	}
	if got := aggregateHealthFromTopo(topo); got != StackRunning {
		t.Errorf("aggregateHealthFromTopo = %d, want StackRunning (%d)", got, StackRunning)
	}
}

func TestAggregateHealthFromTopo_Partial(t *testing.T) {
	topo := map[string]ui.NodeStatus{
		"nginx":    ui.NodeRunning,
		"app-main": ui.NodeStopped,
		"db":       ui.NodeRunning,
	}
	if got := aggregateHealthFromTopo(topo); got != StackPartial {
		t.Errorf("aggregateHealthFromTopo = %d, want StackPartial (%d)", got, StackPartial)
	}
}

func TestAggregateHealthFromTopo_NoneRunning(t *testing.T) {
	topo := map[string]ui.NodeStatus{
		"nginx":    ui.NodeStopped,
		"app-main": ui.NodeStopped,
	}
	if got := aggregateHealthFromTopo(topo); got != StackStopped {
		t.Errorf("aggregateHealthFromTopo = %d, want StackStopped (%d)", got, StackStopped)
	}
}

func TestAggregateHealthFromTopo_DisabledExcluded(t *testing.T) {
	// Disabled nodes do not count; only running nodes matter.
	topo := map[string]ui.NodeStatus{
		"nginx":      ui.NodeRunning,
		"app-main":   ui.NodeRunning,
		"app-second": ui.NodeDisabled,
	}
	if got := aggregateHealthFromTopo(topo); got != StackRunning {
		t.Errorf("aggregateHealthFromTopo = %d, want StackRunning (%d) (disabled should not count)", got, StackRunning)
	}
}

func TestAggregateHealthFromTopo_OnlyDisabled(t *testing.T) {
	topo := map[string]ui.NodeStatus{
		"app-second": ui.NodeDisabled,
	}
	if got := aggregateHealthFromTopo(topo); got != StackStopped {
		t.Errorf("aggregateHealthFromTopo = %d, want StackStopped (%d)", got, StackStopped)
	}
}

func TestAggregateHealthFromTopo_Empty(t *testing.T) {
	if got := aggregateHealthFromTopo(nil); got != StackStopped {
		t.Errorf("aggregateHealthFromTopo(nil) = %d, want StackStopped (%d)", got, StackStopped)
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

// --- disabledNodes ---

func TestDisabledNodes_ReturnsDisabledServiceContainers(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main":   {Type: "app", Container: "app-main", Mandatory: true},
			"second": {Type: "app", Container: "app-second", Enabled: false},
		},
		config.ToolsConfig{
			Adminer: config.ToolConfig{Enabled: false},
		},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)

	names := disabledNodes(cfg)

	// app-second and adminer should appear; app-main is mandatory so excluded.
	hasSecond := false
	hasAdminer := false
	hasMain := false
	for _, n := range names {
		switch n {
		case "app-second":
			hasSecond = true
		case "adminer":
			hasAdminer = true
		case "app-main":
			hasMain = true
		}
	}
	if !hasSecond {
		t.Error("expected app-second in disabled nodes")
	}
	if !hasAdminer {
		t.Error("expected adminer in disabled nodes")
	}
	if hasMain {
		t.Error("app-main is mandatory and must not appear in disabled nodes")
	}
}

func TestDisabledNodes_EnabledToolExcluded(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{},
		config.ToolsConfig{
			Adminer: config.ToolConfig{Enabled: true},
		},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)

	for _, name := range disabledNodes(cfg) {
		if name == "adminer" {
			t.Error("enabled adminer must not appear in disabled nodes")
		}
	}
}

// --- augmentWithDisabled ---

func TestAugmentWithDisabled_AddsDisabledNodes(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"second": {Type: "app", Container: "app-second", Enabled: false},
		},
		config.ToolsConfig{},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)

	topo := map[string][]string{
		"nginx": {},
	}
	status := map[string]ui.NodeStatus{
		"nginx": ui.NodeRunning,
	}

	newTopo, newStatus := augmentWithDisabled(cfg, topo, status)

	if _, ok := newTopo["app-second"]; !ok {
		t.Error("expected app-second added to topology")
	}
	if newStatus["app-second"] != ui.NodeDisabled {
		t.Errorf("expected app-second NodeDisabled, got %v", newStatus["app-second"])
	}
	// existing entries preserved
	if newStatus["nginx"] != ui.NodeRunning {
		t.Errorf("nginx status should be preserved, got %v", newStatus["nginx"])
	}
}

func TestAugmentWithDisabled_NilTopoInitialised(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"second": {Type: "app", Container: "app-second", Enabled: false},
		},
		config.ToolsConfig{},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)

	newTopo, newStatus := augmentWithDisabled(cfg, nil, nil)

	if newTopo == nil {
		t.Fatal("expected non-nil topo after augment")
	}
	if _, ok := newTopo["app-second"]; !ok {
		t.Error("expected app-second in topo")
	}
	if newStatus["app-second"] != ui.NodeDisabled {
		t.Errorf("expected app-second NodeDisabled, got %v", newStatus["app-second"])
	}
}

func TestAugmentWithDisabled_NoDisabledNoop(t *testing.T) {
	// All tools enabled + mandatory service → no disabled nodes → topo/status unchanged.
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig{
			Adminer:      config.ToolConfig{Enabled: true},
			RedisInsight: config.ToolConfig{Enabled: true},
			Mailpit:      config.ToolConfig{Enabled: true},
		},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)

	topo := map[string][]string{"nginx": {}}
	status := map[string]ui.NodeStatus{"nginx": ui.NodeRunning}

	newTopo, newStatus := augmentWithDisabled(cfg, topo, status)

	if len(newTopo) != 1 {
		t.Errorf("expected topo unchanged (len 1), got len %d", len(newTopo))
	}
	if len(newStatus) != 1 {
		t.Errorf("expected status unchanged (len 1), got len %d", len(newStatus))
	}
}

// --- buildComposeArgs ---

func TestBuildComposeArgs_WithProjectName(t *testing.T) {
	args := buildComposeArgs("my-project", []string{"compose.yaml"}, "config")
	// Expect: compose -p my-project -f compose.yaml config
	want := []string{"compose", "-p", "my-project", "-f", "compose.yaml", "config"}
	if len(args) != len(want) {
		t.Fatalf("args length = %d, want %d: %v", len(args), len(want), args)
	}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("args[%d] = %q, want %q", i, args[i], w)
		}
	}
}

func TestBuildComposeArgs_NoProjectName(t *testing.T) {
	args := buildComposeArgs("", []string{"compose.yaml"}, "ps", "--all")
	// Expect: compose -f compose.yaml ps --all (no -p flag)
	want := []string{"compose", "-f", "compose.yaml", "ps", "--all"}
	if len(args) != len(want) {
		t.Fatalf("args length = %d, want %d: %v", len(args), len(want), args)
	}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("args[%d] = %q, want %q", i, args[i], w)
		}
	}
}

// --- parseTopologyFromFiles ---

func TestParseTopologyFromFiles_Empty(t *testing.T) {
	result := parseTopologyFromFiles(nil)
	if result != nil {
		t.Errorf("expected nil for no files, got %v", result)
	}
}

func TestParseTopologyFromFiles_MissingFile(t *testing.T) {
	result := parseTopologyFromFiles([]string{"/nonexistent/path/compose.yaml"})
	if result != nil {
		t.Errorf("expected nil when all files missing, got %v", result)
	}
}

func TestParseTopologyFromFiles_ValidFile(t *testing.T) {
	const composeYAML = `
services:
  nginx:
    image: nginx
    depends_on:
      - app
  app:
    image: myapp
`
	dir := t.TempDir()
	f := dir + "/compose.yaml"
	if err := os.WriteFile(f, []byte(composeYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	result := parseTopologyFromFiles([]string{f})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if _, ok := result["nginx"]; !ok {
		t.Error("expected nginx in result")
	}
	if _, ok := result["app"]; !ok {
		t.Error("expected app in result")
	}
}
