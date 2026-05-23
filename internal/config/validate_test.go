package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/validate/diag"
)

func TestLoadValidateConfig_happy(t *testing.T) {
	cfg, warnings, err := LoadValidateConfig("testdata/validate/happy.yml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d: %+v", len(warnings), warnings)
	}
	if got, want := len(cfg.Checks), 2; got != want {
		t.Fatalf("checks: got %d, want %d", got, want)
	}
	c0 := cfg.Checks[0]
	if c0.ID != "ghcr-login" {
		t.Errorf("c0.ID = %q", c0.ID)
	}
	if c0.Severity != diag.SeverityError {
		t.Errorf("c0.Severity = %v, want SeverityError", c0.Severity)
	}
	if c0.Type != "builtin" || c0.Cmd != "shell" {
		t.Errorf("c0 type/cmd = %q/%q", c0.Type, c0.Cmd)
	}
	if c0.SourceLine == 0 {
		t.Errorf("c0.SourceLine should be > 0")
	}
	if cfg.Checks[1].SourceLine <= c0.SourceLine {
		t.Errorf("c1 line should follow c0; got %d after %d", cfg.Checks[1].SourceLine, c0.SourceLine)
	}
}

func TestLoadValidateConfig_defaultSeverityIsError(t *testing.T) {
	cfg, _, err := LoadValidateConfig("testdata/validate/default_severity.yml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Checks[0].Severity != diag.SeverityError {
		t.Fatalf("default severity = %v, want SeverityError", cfg.Checks[0].Severity)
	}
}

func TestLoadValidateConfig_missingFile(t *testing.T) {
	_, _, err := LoadValidateConfig(filepath.Join(t.TempDir(), "validate.yml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestLoadValidateConfig_unknownStageEmitsInfoWarning(t *testing.T) {
	cfg, warnings, err := LoadValidateConfig("testdata/validate/unknown_stage.yml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Checks) != 1 {
		t.Fatalf("checks = %d, want 1", len(cfg.Checks))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1: %+v", len(warnings), warnings)
	}
	w := warnings[0]
	if w.Severity != diag.SeverityInfo {
		t.Errorf("warning severity = %v, want SeverityInfo", w.Severity)
	}
	if !strings.Contains(w.Message, `"preview"`) {
		t.Errorf("warning message %q should name the unknown stage", w.Message)
	}
	if w.File != "devbox/validate.yml" {
		t.Errorf("warning file = %q", w.File)
	}
	if w.Line <= 0 {
		t.Errorf("warning line = %d, want > 0", w.Line)
	}
}

func TestLoadValidateConfig_errors(t *testing.T) {
	cases := []struct {
		name      string
		fixture   string
		substring string
	}{
		{"unknownField", "unknown_field.yml", "field bogus not found"},
		{"missingID", "missing_id.yml", "id is required"},
		{"missingDescription", "missing_description.yml", "description is required"},
		{"missingStages", "missing_stages.yml", "stages is required"},
		{"missingType", "missing_type.yml", "type is required"},
		{"missingCmd", "missing_cmd.yml", "cmd is required"},
		{"duplicateID", "duplicate_id.yml", "duplicate id"},
		{"unknownSeverity", "unknown_severity.yml", "unknown severity"},
		{"unknownType", "unknown_type.yml", "unknown type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := LoadValidateConfig(filepath.Join("testdata", "validate", tc.fixture))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.substring) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.substring)
			}
		})
	}
}

func TestValidateConfigPath(t *testing.T) {
	got := ValidateConfigPath("/tmp/proj")
	want := filepath.Join("/tmp/proj", "devbox", "validate.yml")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseSeverity(t *testing.T) {
	cases := []struct {
		in      string
		want    diag.Severity
		wantErr bool
	}{
		{"", diag.SeverityError, false},
		{"error", diag.SeverityError, false},
		{"warning", diag.SeverityWarning, false},
		{"info", diag.SeverityInfo, false},
		{"bogus", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseSeverity(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
