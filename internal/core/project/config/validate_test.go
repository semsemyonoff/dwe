package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/validate/diag"
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

func TestLoadValidateConfig_unknownStageEmitsWarning(t *testing.T) {
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
	if w.Severity != diag.SeverityWarning {
		t.Errorf("warning severity = %v, want SeverityWarning", w.Severity)
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
	if !strings.Contains(w.Hint, "Known stages") {
		t.Errorf("hint %q should list known stages", w.Hint)
	}
}

func TestLoadValidateConfig_typoStageSuggestion(t *testing.T) {
	// Create a test file with a typo in stage name.
	content := `checks:
  - id: typo-check
    description: check with typo stage
    stages: [deplooy]
    type: builtin
    cmd: shell
    with:
      cmd: 'true'
`
	tmpfile, err := os.CreateTemp("", "validate-*.yml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	_ = tmpfile.Close()

	_, warnings, err := LoadValidateConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %+v", len(warnings), warnings)
	}
	w := warnings[0]
	if !strings.Contains(w.Hint, "deploy") {
		t.Errorf("hint should suggest 'deploy' for typo 'deplooy': %q", w.Hint)
	}
}

func TestLoadValidateConfig_restartStageNote(t *testing.T) {
	// Create a test file with "restart" stage (composite, not a preflight stage).
	content := `checks:
  - id: restart-check
    description: check with restart stage
    stages: [restart]
    type: builtin
    cmd: shell
    with:
      cmd: 'true'
`
	tmpfile, err := os.CreateTemp("", "validate-*.yml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	_ = tmpfile.Close()

	_, warnings, err := LoadValidateConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	w := warnings[0]
	if !strings.Contains(w.Hint, "composite") {
		t.Errorf("hint should mention that restart is composite: %q", w.Hint)
	}
}

func TestLoadValidateConfig_resetStageNote(t *testing.T) {
	// Create a test file with "reset" stage (uses stop stage for preflight).
	content := `checks:
  - id: reset-check
    description: check with reset stage
    stages: [reset]
    type: builtin
    cmd: shell
    with:
      cmd: 'true'
`
	tmpfile, err := os.CreateTemp("", "validate-*.yml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	_ = tmpfile.Close()

	_, warnings, err := LoadValidateConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	w := warnings[0]
	if !strings.Contains(w.Hint, "stop") {
		t.Errorf("hint should mention that reset uses stop stage: %q", w.Hint)
	}
}

func TestLoadValidateConfig_validStagesNoWarning(t *testing.T) {
	// Create a test file with valid stages only.
	content := `checks:
  - id: valid-check
    description: check with valid stages
    stages: [deploy, run, stop, command]
    type: builtin
    cmd: shell
    with:
      cmd: 'true'
`
	tmpfile, err := os.CreateTemp("", "validate-*.yml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	_ = tmpfile.Close()

	_, warnings, err := LoadValidateConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for valid stages, got %d: %+v", len(warnings), warnings)
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

func TestLoadValidateConfig_lintersHappy(t *testing.T) {
	cfg, warnings, err := LoadValidateConfig("testdata/validate/linters_happy.yml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings: %+v", warnings)
	}
	if got, want := len(cfg.Linters), 4; got != want {
		t.Fatalf("linters: got %d, want %d", got, want)
	}

	byID := map[string]LinterEntry{}
	for _, l := range cfg.Linters {
		byID[l.ID] = l
	}

	sc, ok := byID["shellcheck"]
	if !ok {
		t.Fatalf("shellcheck missing")
	}
	if sc.Type != "builtin" {
		t.Errorf("shellcheck.Type = %q, want builtin", sc.Type)
	}
	if sc.Enabled == nil || !*sc.Enabled {
		t.Errorf("shellcheck.Enabled = %v, want *true", sc.Enabled)
	}
	if sc.Bin != "shellcheck" {
		t.Errorf("shellcheck.Bin = %q", sc.Bin)
	}
	if sc.Severity == nil || *sc.Severity != diag.SeverityWarning {
		t.Errorf("shellcheck.Severity = %v, want *SeverityWarning", sc.Severity)
	}
	if sc.SourceLine == 0 {
		t.Errorf("shellcheck.SourceLine = 0")
	}

	had := byID["hadolint"]
	if had.Type != "builtin" {
		t.Errorf("hadolint.Type = %q, want builtin (default)", had.Type)
	}
	if had.Enabled != nil {
		t.Errorf("hadolint.Enabled = %v, want nil", had.Enabled)
	}
	if had.Severity != nil {
		t.Errorf("hadolint.Severity = %v, want nil", had.Severity)
	}
	if len(had.Filenames) != 1 || had.Filenames[0] != "Dockerfile" {
		t.Errorf("hadolint.Filenames = %v", had.Filenames)
	}
	if len(had.Paths) != 1 || had.Paths[0] != "." {
		t.Errorf("hadolint.Paths = %v, want [.]", had.Paths)
	}

	gen := byID["yamllint"]
	if gen.Type != "generic" {
		t.Errorf("yamllint.Type = %q, want generic", gen.Type)
	}

	dis := byID["disabled-thing"]
	if dis.Enabled == nil || *dis.Enabled {
		t.Errorf("disabled-thing.Enabled = %v, want *false", dis.Enabled)
	}
}

func TestLoadValidateConfig_lintersSeverityRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		check   func(t *testing.T, e LinterEntry)
		wantErr string
	}{
		{
			name: "absent",
			body: "linters:\n  l: {}\n",
			check: func(t *testing.T, e LinterEntry) {
				if e.Severity != nil {
					t.Fatalf("Severity = %v, want nil", e.Severity)
				}
			},
		},
		{
			name: "warning",
			body: "linters:\n  l: { severity: warning }\n",
			check: func(t *testing.T, e LinterEntry) {
				if e.Severity == nil || *e.Severity != diag.SeverityWarning {
					t.Fatalf("Severity = %v, want SeverityWarning", e.Severity)
				}
			},
		},
		{
			name: "error",
			body: "linters:\n  l: { severity: error }\n",
			check: func(t *testing.T, e LinterEntry) {
				if e.Severity == nil || *e.Severity != diag.SeverityError {
					t.Fatalf("Severity = %v, want SeverityError", e.Severity)
				}
			},
		},
		{
			name: "info",
			body: "linters:\n  l: { severity: info }\n",
			check: func(t *testing.T, e LinterEntry) {
				if e.Severity == nil || *e.Severity != diag.SeverityInfo {
					t.Fatalf("Severity = %v, want SeverityInfo", e.Severity)
				}
			},
		},
		{
			name:    "empty",
			body:    `{linters: {l: {severity: ""}}}` + "\n",
			wantErr: "not empty",
		},
		{
			name:    "ok rejected",
			body:    "linters:\n  l: { severity: ok }\n",
			wantErr: "not allowed",
		},
		{
			name:    "bogus rejected",
			body:    "linters:\n  l: { severity: bogus }\n",
			wantErr: "unknown severity",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "validate.yml")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, _, err := LoadValidateConfig(path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(cfg.Linters) != 1 {
				t.Fatalf("linters: %d", len(cfg.Linters))
			}
			tc.check(t, cfg.Linters[0])
		})
	}
}

func TestLoadValidateConfig_lintersValidationErrors(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		substring string
	}{
		{"unknownType", "linters:\n  l: { type: bogus }\n", "unknown type"},
		{"unknownField", "linters:\n  l: { wat: yes }\n", "field wat not found"},
		{"binWithSlash", "linters:\n  l: { bin: /usr/bin/shellcheck }\n", "bare command name"},
		{"binWithRelPath", "linters:\n  l: { bin: ./tool }\n", "bare command name"},
		{"pathTraversal", "linters:\n  l: { paths: [../escape] }\n", "traverse outside"},
		{"absolutePath", "linters:\n  l: { paths: [/etc] }\n", "must be relative"},
		{"emptyPath", `{linters: {l: {paths: [""]}}}` + "\n", "must not be empty"},
		{"extWithoutDot", "linters:\n  l: { extensions: [sh] }\n", "must start with"},
		{"extWithSlash", "linters:\n  l: { extensions: [\".s/h\"] }\n", "path separators"},
		{"filenameWithSlash", "linters:\n  l: { filenames: [dir/file] }\n", "path separators"},
		{"emptyFilename", `{linters: {l: {filenames: [""]}}}` + "\n", "must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "validate.yml")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := LoadValidateConfig(path)
			if err == nil || !strings.Contains(err.Error(), tc.substring) {
				t.Fatalf("err = %v, want substring %q", err, tc.substring)
			}
		})
	}
}

func TestLoadValidateConfig_lintersPathsDotAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validate.yml")
	if err := os.WriteFile(path, []byte("linters:\n  hadolint:\n    paths: [\".\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadValidateConfig(path)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.Linters[0].Paths[0] != "." {
		t.Fatalf("Paths = %v, want [.]", cfg.Linters[0].Paths)
	}
}

func TestLoadValidateConfig_lintersMissingBinAllowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validate.yml")
	if err := os.WriteFile(path, []byte("linters:\n  shellcheck: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadValidateConfig(path)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.Linters[0].Bin != "" {
		t.Fatalf("Bin = %q, want empty (default takes over at runtime)", cfg.Linters[0].Bin)
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
