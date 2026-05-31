package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
)

func TestWrite_createsFile(t *testing.T) {
	dir := t.TempDir()
	cfg := makeEnvCfg(nil, map[string]any{})
	outputPath := filepath.Join(dir, ".env")

	if err := Write(cfg, outputPath); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "PROJECT=laravel") {
		t.Errorf("expected PROJECT=laravel in output, got:\n%s", data)
	}
}

func TestWrite_createsParentDirs(t *testing.T) {
	dir := t.TempDir()
	cfg := makeEnvCfg(nil, map[string]any{})
	outputPath := filepath.Join(dir, "sub", "dir", ".env")

	if err := Write(cfg, outputPath); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	if _, err := os.Stat(outputPath); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

func TestRegenerate_writesEnvNextToConfig(t *testing.T) {
	dir := t.TempDir()
	// Write a minimal devbox.yml so LoadConfig can parse it.
	configContent := `
project:
  name: testproject
  prefix: devbox
`
	configPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile devbox.yml: %v", err)
	}

	envPath, err := Regenerate(configPath)
	if err != nil {
		t.Fatalf("Regenerate returned error: %v", err)
	}

	expected := filepath.Join(dir, ".env")
	if envPath != expected {
		t.Errorf("expected envPath=%q, got %q", expected, envPath)
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("ReadFile .env: %v", err)
	}
	if !strings.Contains(string(data), "PROJECT=testproject") {
		t.Errorf("expected PROJECT=testproject, got:\n%s", data)
	}
}

func TestWrite_withExportRules(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myapp", Prefix: "devbox"},
		Exports: config.ExportsConfig{Env: []config.ExportRule{
			{Name: "APP_ENV", From: "state"},
		}},
		Raw: map[string]any{"state": "production"},
	}
	outputPath := filepath.Join(dir, ".env")

	if err := Write(cfg, outputPath); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "APP_ENV=production") {
		t.Errorf("expected APP_ENV=production, got:\n%s", data)
	}
}
