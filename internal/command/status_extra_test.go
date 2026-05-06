package command

import (
	"bytes"
	"path/filepath"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// hasRuntimeStatuses, removeHiddenNodes, resolveProjectAndDocker tests have been
// moved to internal/stack/topology_test.go and internal/stack/health_test.go.

// --- renderIDETemplate additional edge cases ---

func TestRenderIDETemplate_InvalidTemplate(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.json")
	tplStr := `{{.Invalid`
	data := ideTemplateData{}
	err := renderIDETemplate(tplStr, "out.json", data, dest, dir)
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
