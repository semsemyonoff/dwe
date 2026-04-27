package command

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
)

// --- hasRuntimeStatuses ---

func TestHasRuntimeStatuses_EmptyMap(t *testing.T) {
	if hasRuntimeStatuses(nil) {
		t.Error("expected false for nil map")
	}
	if hasRuntimeStatuses(map[string]ui.NodeStatus{}) {
		t.Error("expected false for empty map")
	}
}

func TestHasRuntimeStatuses_OnlyDisabled(t *testing.T) {
	m := map[string]ui.NodeStatus{"web": ui.NodeDisabled}
	if hasRuntimeStatuses(m) {
		t.Error("expected false when only disabled nodes")
	}
}

func TestHasRuntimeStatuses_WithRunning(t *testing.T) {
	m := map[string]ui.NodeStatus{"web": ui.NodeRunning}
	if !hasRuntimeStatuses(m) {
		t.Error("expected true when running node exists")
	}
}

func TestHasRuntimeStatuses_WithStopped(t *testing.T) {
	m := map[string]ui.NodeStatus{"web": ui.NodeStopped}
	if !hasRuntimeStatuses(m) {
		t.Error("expected true when stopped (non-disabled) node exists")
	}
}

// --- removeHiddenNodes ---

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
	topo2, status2 := removeHiddenNodes(topo, status, []string{"tool"})
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
	topo2, _ := removeHiddenNodes(topo, status, []string{"cache"})
	for _, dep := range topo2["web"] {
		if dep == "cache" {
			t.Error("cache should be pruned from web's deps")
		}
	}
}

func TestRemoveHiddenNodes_EmptyHidden(t *testing.T) {
	topo := map[string][]string{"web": nil}
	status := map[string]ui.NodeStatus{"web": ui.NodeRunning}
	topo2, status2 := removeHiddenNodes(topo, status, nil)
	if len(topo2) != 1 || len(status2) != 1 {
		t.Errorf("empty hidden list should leave everything unchanged")
	}
}

// --- resolveProjectAndDocker ---

func TestResolveProjectAndDocker_WithDockerYML(t *testing.T) {
	dir := makeMinimalProject(t)
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "test", Prefix: "devbox"},
	}
	projectName, dockerCfg, err := resolveProjectAndDocker(filepath.Join(dir, "devbox.yml"), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if projectName == "" {
		t.Error("expected non-empty project name")
	}
	_ = dockerCfg
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
	projectName, dockerCfg, err := resolveProjectAndDocker(filepath.Join(dir, "devbox.yml"), cfg)
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

// --- renderIDETemplate additional edge cases ---

func TestRenderIDETemplate_InvalidTemplate(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.json")
	tplStr := `{{.Invalid`
	data := ideTemplateData{}
	err := renderIDETemplate(tplStr, "out.json", data, dest)
	if err == nil {
		t.Fatal("expected error for invalid template")
	}
}

// --- renderIDEConfigs with no IDE enabled ---

func TestRenderIDEConfigs_NoIDEEnabled(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "test"},
		IDE:     config.IDEConfig{},
	}
	svc := config.ServiceConfig{Dir: "./services/main"}
	w := render.NewWriter(&bytes.Buffer{})
	err := renderIDEConfigs(dir, "main", svc, cfg, w)
	if err != nil {
		t.Fatalf("renderIDEConfigs with no IDE: %v", err)
	}
}

func TestRenderIDEConfigs_JetBrainsEnabled(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "test"},
		IDE: config.IDEConfig{
			JetBrains: config.IDEEditorConfig{Enabled: true},
		},
	}
	svc := config.ServiceConfig{Dir: "./services/main"}
	w := render.NewWriter(&bytes.Buffer{})
	// JetBrains just emits a warning, no error.
	err := renderIDEConfigs(dir, "main", svc, cfg, w)
	if err != nil {
		t.Fatalf("renderIDEConfigs jetbrains: %v", err)
	}
}
