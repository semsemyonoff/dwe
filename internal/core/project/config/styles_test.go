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
    - "DWE Next"
  font: doom
  tagline: "Local dev, batteries included"

colors:
  accent: "#2EC3EB"
  success: "#22C55E"
  warning: "#F59E0B"
  danger: "#EF4444"
  muted: "#9AA3BB"
  border: "#334155"
  text: "#E2E8F0"

separator: "—"
`

const partialStylesYML = `
colors:
  accent: "#2EC3EB"
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
	if cfg.Header.Lines[1] != "DWE Next" {
		t.Errorf("header.lines[1] = %q", cfg.Header.Lines[1])
	}
	if cfg.Header.Font != "doom" {
		t.Errorf("header.font = %q, want doom", cfg.Header.Font)
	}
	if cfg.Header.Tagline != "Local dev, batteries included" {
		t.Errorf("header.tagline = %q", cfg.Header.Tagline)
	}

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"accent", cfg.Colors.Accent, "#2EC3EB"},
		{"success", cfg.Colors.Success, "#22C55E"},
		{"warning", cfg.Colors.Warning, "#F59E0B"},
		{"danger", cfg.Colors.Danger, "#EF4444"},
		{"muted", cfg.Colors.Muted, "#9AA3BB"},
		{"border", cfg.Colors.Border, "#334155"},
		{"text", cfg.Colors.Text, "#E2E8F0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("colors.%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}

	if cfg.Separator != "—" {
		t.Errorf("separator = %q, want —", cfg.Separator)
	}
}

func TestLoadStylesConfig_missingFile(t *testing.T) {
	cfg, err := LoadStylesConfig("/tmp/dwe-nonexistent-styles.yml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config for missing file")
	}
	if cfg.Colors.Accent != "" {
		t.Errorf("expected empty accent for missing file, got %q", cfg.Colors.Accent)
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

	if cfg.Colors.Accent != "#2EC3EB" {
		t.Errorf("colors.accent = %q, want #2EC3EB", cfg.Colors.Accent)
	}
	// Every other token zero-values to empty string; ApplyStyles is responsible
	// for resolving these to the light/dark hex defaults (covered in Task 2).
	zeroCases := []struct {
		name string
		got  string
	}{
		{"success", cfg.Colors.Success},
		{"warning", cfg.Colors.Warning},
		{"danger", cfg.Colors.Danger},
		{"muted", cfg.Colors.Muted},
		{"border", cfg.Colors.Border},
		{"text", cfg.Colors.Text},
	}
	for _, tc := range zeroCases {
		t.Run(tc.name+"_empty", func(t *testing.T) {
			if tc.got != "" {
				t.Errorf("colors.%s should be empty, got %q", tc.name, tc.got)
			}
		})
	}
	if cfg.Header.Font != "" {
		t.Errorf("header.font should be empty, got %q", cfg.Header.Font)
	}
	if cfg.Header.Tagline != "" {
		t.Errorf("header.tagline should be empty, got %q", cfg.Header.Tagline)
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
