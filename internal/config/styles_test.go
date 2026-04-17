package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleStylesYML = `
header:
  lines:
    - "Welcome to"
    - "Devbox Next"
  font: doom
  color: red

colors:
  label: "203"
  section_title: "167"
  subheader: "209"
  muted: "245"
  warning: "214"
  info: "210"
  enabled: "2"
  disabled: "245"
  mandatory: "203"
  partial: "214"
  table_border: "167"
  table_header: "203"

separator: "—"
`

const partialStylesYML = `
colors:
  label: "203"
`

func TestLoadStylesConfig(t *testing.T) {
	path := writeTempYML(t, sampleStylesYML)
	cfg, err := LoadStylesConfig(path)
	if err != nil {
		t.Fatalf("LoadStylesConfig: %v", err)
	}

	if len(cfg.Header.Lines) != 2 {
		t.Errorf("header.lines count = %d, want 2", len(cfg.Header.Lines))
	}
	if cfg.Header.Lines[0] != "Welcome to" {
		t.Errorf("header.lines[0] = %q", cfg.Header.Lines[0])
	}
	if cfg.Header.Lines[1] != "Devbox Next" {
		t.Errorf("header.lines[1] = %q", cfg.Header.Lines[1])
	}
	if cfg.Header.Font != "doom" {
		t.Errorf("header.font = %q, want doom", cfg.Header.Font)
	}
	if cfg.Header.Color != "red" {
		t.Errorf("header.color = %q, want red", cfg.Header.Color)
	}

	if cfg.Colors.Label != "203" {
		t.Errorf("colors.label = %q, want 203", cfg.Colors.Label)
	}
	if cfg.Colors.SectionTitle != "167" {
		t.Errorf("colors.section_title = %q, want 167", cfg.Colors.SectionTitle)
	}
	if cfg.Colors.SubHeader != "209" {
		t.Errorf("colors.subheader = %q, want 209", cfg.Colors.SubHeader)
	}
	if cfg.Colors.Muted != "245" {
		t.Errorf("colors.muted = %q, want 245", cfg.Colors.Muted)
	}
	if cfg.Colors.Warning != "214" {
		t.Errorf("colors.warning = %q, want 214", cfg.Colors.Warning)
	}
	if cfg.Colors.Info != "210" {
		t.Errorf("colors.info = %q, want 210", cfg.Colors.Info)
	}
	if cfg.Colors.Enabled != "2" {
		t.Errorf("colors.enabled = %q, want 2", cfg.Colors.Enabled)
	}
	if cfg.Colors.Disabled != "245" {
		t.Errorf("colors.disabled = %q, want 245", cfg.Colors.Disabled)
	}
	if cfg.Colors.Mandatory != "203" {
		t.Errorf("colors.mandatory = %q, want 203", cfg.Colors.Mandatory)
	}
	if cfg.Colors.Partial != "214" {
		t.Errorf("colors.partial = %q, want 214", cfg.Colors.Partial)
	}
	if cfg.Colors.TableBorder != "167" {
		t.Errorf("colors.table_border = %q, want 167", cfg.Colors.TableBorder)
	}
	if cfg.Colors.TableHeader != "203" {
		t.Errorf("colors.table_header = %q, want 203", cfg.Colors.TableHeader)
	}
	if cfg.Separator != "—" {
		t.Errorf("separator = %q, want —", cfg.Separator)
	}
}

func TestLoadStylesConfig_missingFile(t *testing.T) {
	cfg, err := LoadStylesConfig("/tmp/devbox-nonexistent-styles.yml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config for missing file")
	}
	// All fields should be zero values
	if cfg.Colors.Label != "" {
		t.Errorf("expected empty label for missing file, got %q", cfg.Colors.Label)
	}
	if cfg.Separator != "" {
		t.Errorf("expected empty separator for missing file, got %q", cfg.Separator)
	}
}

func TestLoadStylesConfig_partialFields(t *testing.T) {
	path := writeTempYML(t, partialStylesYML)
	cfg, err := LoadStylesConfig(path)
	if err != nil {
		t.Fatalf("LoadStylesConfig: %v", err)
	}

	// Explicitly set field
	if cfg.Colors.Label != "203" {
		t.Errorf("colors.label = %q, want 203", cfg.Colors.Label)
	}
	// Omitted fields should be empty strings
	if cfg.Colors.SectionTitle != "" {
		t.Errorf("colors.section_title should be empty, got %q", cfg.Colors.SectionTitle)
	}
	if cfg.Header.Font != "" {
		t.Errorf("header.font should be empty, got %q", cfg.Header.Font)
	}
	if cfg.Separator != "" {
		t.Errorf("separator should be empty, got %q", cfg.Separator)
	}
}

func TestLoadStylesConfig_invalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "styles.yml")
	if err := os.WriteFile(path, []byte("{ invalid yaml ]["), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadStylesConfig(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
	// Even on parse error, a non-nil fallback config must be returned so callers
	// can still apply defaults without a nil check.
	if cfg == nil {
		t.Error("expected non-nil fallback config even on parse error")
	}
}
