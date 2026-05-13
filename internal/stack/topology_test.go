package stack

import (
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"
)

// --- FetchComposeTopology bin parameter ---

func TestFetchComposeTopology_EmptyFiles_NilRegardlessOfBin(t *testing.T) {
	// No files → nil regardless of which binary is requested.
	if got := FetchComposeTopology(nil, "proj", nil, "podman"); got != nil {
		t.Errorf("expected nil for empty file list, got %v", got)
	}
}

func TestComposeNodeStatuses_EmptyFiles_NilRegardlessOfBin(t *testing.T) {
	if got := ComposeNodeStatuses(nil, "proj", nil, "podman"); got != nil {
		t.Errorf("expected nil for empty file list, got %v", got)
	}
}

// --- BuildComposeArgs ---

func TestBuildComposeArgs_WithProjectName(t *testing.T) {
	args := BuildComposeArgs("my-project", []string{"compose.yaml"}, "config")
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
	args := BuildComposeArgs("", []string{"compose.yaml"}, "ps", "--all")
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

// --- ParseTopologyFromFiles ---

func TestParseTopologyFromFiles_Empty(t *testing.T) {
	result := ParseTopologyFromFiles(nil)
	if result != nil {
		t.Errorf("expected nil for no files, got %v", result)
	}
}

func TestParseTopologyFromFiles_MissingFile(t *testing.T) {
	result := ParseTopologyFromFiles([]string{"/nonexistent/path/compose.yaml"})
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

	result := ParseTopologyFromFiles([]string{f})
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

// --- DisabledNodes ---

func TestDisabledNodes_ReturnsDisabledServiceContainers(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main":   {Type: "app", Container: "app-main", Mandatory: true},
			"second": {Type: "app", Container: "app-second", Enabled: false},
		},
		config.ToolsConfig{
			"adminer": {Enabled: false, Container: "adminer", Host: "adminer.localhost", Port: 8080},
		},
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
	)

	names := DisabledNodes(cfg)

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
			"adminer": {Enabled: true, Container: "adminer", Host: "adminer.localhost", Port: 8080},
		},
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
	)

	for _, name := range DisabledNodes(cfg) {
		if name == "adminer" {
			t.Error("enabled adminer must not appear in disabled nodes")
		}
	}
}

// --- AugmentWithDisabled ---

func TestAugmentWithDisabled_AddsDisabledNodes(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"second": {Type: "app", Container: "app-second", Enabled: false},
		},
		config.ToolsConfig(nil),
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
	)

	topo := map[string][]string{
		"nginx": {},
	}
	status := map[string]ui.NodeStatus{
		"nginx": ui.NodeRunning,
	}

	newTopo, newStatus := AugmentWithDisabled(cfg, topo, status)

	if _, ok := newTopo["app-second"]; !ok {
		t.Error("expected app-second added to topology")
	}
	if newStatus["app-second"] != ui.NodeDisabled {
		t.Errorf("expected app-second NodeDisabled, got %v", newStatus["app-second"])
	}
	if newStatus["nginx"] != ui.NodeRunning {
		t.Errorf("nginx status should be preserved, got %v", newStatus["nginx"])
	}
}

func TestAugmentWithDisabled_NilTopoInitialised(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"second": {Type: "app", Container: "app-second", Enabled: false},
		},
		config.ToolsConfig(nil),
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
	)

	newTopo, newStatus := AugmentWithDisabled(cfg, nil, nil)

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
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: "app-main", Mandatory: true},
		},
		config.ToolsConfig{
			"adminer": {Enabled: true, Container: "adminer", Host: "adminer.localhost", Port: 8080},
			"redis_insight": {Enabled: true, Container: "redis-insight", Host: "redis-insight.localhost", Port: 8081},
			"mailpit": {Enabled: true, Container: "mailpit", Host: "mailpit.localhost", Port: 8082},
		},
		config.RuntimePorts(nil),
		config.RuntimeHosts(nil),
	)

	topo := map[string][]string{"nginx": {}}
	status := map[string]ui.NodeStatus{"nginx": ui.NodeRunning}

	newTopo, newStatus := AugmentWithDisabled(cfg, topo, status)

	if len(newTopo) != 1 {
		t.Errorf("expected topo unchanged (len 1), got len %d", len(newTopo))
	}
	if len(newStatus) != 1 {
		t.Errorf("expected status unchanged (len 1), got len %d", len(newStatus))
	}
}

// --- RemoveHiddenNodes ---

func TestRemoveHiddenNodes_RemovesFromBoth(t *testing.T) {
	topo := map[string][]string{
		"web":  {"db"},
		"db":   nil,
		"tool": nil,
	}
	status := map[string]ui.NodeStatus{
		"web":  ui.NodeRunning,
		"db":   ui.NodeRunning,
		"tool": ui.NodeDisabled,
	}
	topo2, status2 := RemoveHiddenNodes(topo, status, []string{"tool"})
	if _, exists := topo2["tool"]; exists {
		t.Error("tool should be removed from topo")
	}
	if _, exists := status2["tool"]; exists {
		t.Error("tool should be removed from status")
	}
	if len(topo2) != 2 {
		t.Errorf("expected 2 nodes after removal, got %v", topo2)
	}
}

func TestRemoveHiddenNodes_PrunesDepsFromOthers(t *testing.T) {
	topo := map[string][]string{
		"web": {"db", "cache"},
		"db":  nil,
	}
	status := map[string]ui.NodeStatus{
		"web":   ui.NodeRunning,
		"db":    ui.NodeRunning,
		"cache": ui.NodeRunning,
	}
	topo2, _ := RemoveHiddenNodes(topo, status, []string{"cache"})
	for _, dep := range topo2["web"] {
		if dep == "cache" {
			t.Error("cache should be pruned from web's deps")
		}
	}
}

func TestRemoveHiddenNodes_EmptyHidden(t *testing.T) {
	topo := map[string][]string{"web": nil}
	status := map[string]ui.NodeStatus{"web": ui.NodeRunning}
	topo2, status2 := RemoveHiddenNodes(topo, status, nil)
	if len(topo2) != 1 || len(status2) != 1 {
		t.Errorf("empty hidden list should leave everything unchanged")
	}
}

// --- ResolveProjectAndDocker ---

func TestResolveProjectAndDocker_WithDockerYML(t *testing.T) {
	dir := makeMinimalProject(t)
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "test", Prefix: "devbox"},
	}
	projectName, dockerCfg, err := ResolveProjectAndDocker(filepath.Join(dir, "devbox.yml"), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if projectName != "devbox-test" {
		t.Errorf("expected project name %q from docker.yml, got %q", "devbox-test", projectName)
	}
	if dockerCfg == nil {
		t.Error("expected non-nil dockerCfg")
	}
}

func TestResolveProjectAndDocker_NoDockerYML(t *testing.T) {
	dir := t.TempDir()
	devboxYML := `project:
  name: test
  prefix: devbox
services:
  main:
    type: app
    dir: ./services/main
`
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(devboxYML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "test", Prefix: "devbox"},
	}
	projectName, dockerCfg, err := ResolveProjectAndDocker(filepath.Join(dir, "devbox.yml"), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dockerCfg != nil {
		t.Error("expected nil dockerCfg when no docker.yml")
	}
	if projectName == "" {
		t.Error("expected fallback project name")
	}
}
