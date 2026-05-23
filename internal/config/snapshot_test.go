package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSnapshotYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write snapshot.yml: %v", err)
	}
	return path
}

func TestLoadSnapshotConfig_full(t *testing.T) {
	body := `
dir: ./snapshots
rollback_target: baseline
require_matching_config: true
pack:
  exclude: ["**/*.tmp", ".cache/**"]
create:
  description: Capture env
  steps:
    - command: db.dump
      with:
        out: "${snapshot.path}/db/main.sql.gz"
    - confirm: Continue?
    - parallel:
        steps:
          - command: opensearch.snapshot
          - command: redis.dump
  variants:
    db-only:
      steps:
        - command: db.dump
restore:
  steps:
    - command: db.restore
      when: "file-exists ${snapshot.path}/db/main.sql.gz"
      with:
        in: "${snapshot.path}/db/main.sql.gz"
      continue_on_error: true
remove:
  steps:
    - command: db.drop-snapshot-db
`
	path := writeSnapshotYAML(t, body)
	cfg, err := LoadSnapshotConfig(path)
	if err != nil {
		t.Fatalf("LoadSnapshotConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Dir != "./snapshots" {
		t.Errorf("Dir: got %q", cfg.Dir)
	}
	if cfg.RollbackTarget != "baseline" {
		t.Errorf("RollbackTarget: got %q", cfg.RollbackTarget)
	}
	if !cfg.RequireMatchingConfig {
		t.Errorf("RequireMatchingConfig: want true")
	}
	if len(cfg.Pack.Exclude) != 2 {
		t.Errorf("Pack.Exclude: len=%d", len(cfg.Pack.Exclude))
	}
	if cfg.Create == nil || len(cfg.Create.Steps) != 3 {
		t.Fatalf("Create.Steps: want 3 got %+v", cfg.Create)
	}
	if cfg.Create.Steps[0].Command != "db.dump" {
		t.Errorf("create step[0].Command: got %q", cfg.Create.Steps[0].Command)
	}
	if cfg.Create.Steps[1].Confirm != "Continue?" {
		t.Errorf("create step[1].Confirm: got %q", cfg.Create.Steps[1].Confirm)
	}
	if cfg.Create.Steps[2].Parallel == nil || len(cfg.Create.Steps[2].Parallel.Steps) != 2 {
		t.Errorf("create step[2].Parallel: %+v", cfg.Create.Steps[2].Parallel)
	}
	if v, ok := cfg.Create.Variants["db-only"]; !ok || len(v.Steps) != 1 {
		t.Errorf("Create.Variants[db-only]: %+v", cfg.Create.Variants)
	}
	if cfg.Restore == nil || len(cfg.Restore.Steps) != 1 {
		t.Fatalf("Restore.Steps: %+v", cfg.Restore)
	}
	if !cfg.Restore.Steps[0].ContinueOnError {
		t.Errorf("Restore.Steps[0].ContinueOnError: want true")
	}
	if cfg.Remove == nil || len(cfg.Remove.Steps) != 1 {
		t.Fatalf("Remove.Steps: %+v", cfg.Remove)
	}
}

func TestLoadSnapshotConfig_missingFile(t *testing.T) {
	cfg, err := LoadSnapshotConfig(filepath.Join(t.TempDir(), "snapshot.yml"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil cfg for missing file, got: %+v", cfg)
	}
}

func TestLoadSnapshotConfig_unknownTopLevelFieldRejected(t *testing.T) {
	body := `
dir: ./snapshots
mystery: 1
`
	path := writeSnapshotYAML(t, body)
	_, err := LoadSnapshotConfig(path)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "mystery") {
		t.Errorf("error should mention unknown field 'mystery': %v", err)
	}
}

func TestLoadSnapshotConfig_unknownNestedFieldRejected(t *testing.T) {
	body := `
create:
  steps:
    - command: db.dump
      bogus: 1
`
	path := writeSnapshotYAML(t, body)
	_, err := LoadSnapshotConfig(path)
	if err == nil {
		t.Fatal("expected error for unknown nested field, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention 'bogus': %v", err)
	}
}

func TestLoadSnapshotConfig_malformedYAML(t *testing.T) {
	path := writeSnapshotYAML(t, "dir: [unterminated\n")
	_, err := LoadSnapshotConfig(path)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestLoadSnapshotConfig_variantNaming(t *testing.T) {
	cases := []struct {
		name    string
		variant string
		wantErr bool
	}{
		{"plain", "db-only", false},
		{"alnum", "v1", false},
		{"dots-and-underscores", "a.b_c-1", false},
		{"uppercase-rejected", "DB", true},
		{"starts-with-dot", ".hidden", true},
		{"starts-with-dash", "-foo", true},
		{"empty", "", true},
		{"too-long", strings.Repeat("a", 32), true},
		{"space", "a b", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "create:\n  variants:\n    \"" + tc.variant + "\":\n      steps:\n        - command: db.dump\n"
			path := writeSnapshotYAML(t, body)
			_, err := LoadSnapshotConfig(path)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for variant %q, got nil", tc.variant)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("variant %q: unexpected error: %v", tc.variant, err)
			}
		})
	}
}

func TestLoadSnapshotConfig_stepValidationApplied(t *testing.T) {
	// Step with neither command, confirm, nor parallel set must be rejected
	// via model.WorkflowStep.Validate().
	body := `
create:
  steps:
    - {}
`
	path := writeSnapshotYAML(t, body)
	_, err := LoadSnapshotConfig(path)
	if err == nil {
		t.Fatal("expected step-validation error, got nil")
	}
	if !strings.Contains(err.Error(), "command, confirm, or parallel") {
		t.Errorf("error should reference workflow step validation: %v", err)
	}
}

func TestLoadSnapshotConfig_nestedParallelRejected(t *testing.T) {
	body := `
create:
  steps:
    - parallel:
        steps:
          - parallel:
              steps:
                - command: a
                - command: b
          - command: c
`
	path := writeSnapshotYAML(t, body)
	_, err := LoadSnapshotConfig(path)
	if err == nil {
		t.Fatal("expected nested-parallel rejection, got nil")
	}
	if !strings.Contains(err.Error(), "nested parallel") {
		t.Errorf("error should mention nested parallel: %v", err)
	}
}

func TestLoadSnapshotConfig_nestedVariantsRejected(t *testing.T) {
	body := `
create:
  variants:
    a:
      steps:
        - command: x
      variants:
        b:
          steps:
            - command: y
`
	path := writeSnapshotYAML(t, body)
	_, err := LoadSnapshotConfig(path)
	if err == nil {
		t.Fatal("expected nested-variants rejection, got nil")
	}
	if !strings.Contains(err.Error(), "nested variants") {
		t.Errorf("error should mention nested variants: %v", err)
	}
}

func TestLoadSnapshotConfig_emptyDocument(t *testing.T) {
	path := writeSnapshotYAML(t, "")
	cfg, err := LoadSnapshotConfig(path)
	if err != nil {
		t.Fatalf("empty doc: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil cfg for empty file")
	}
}

func TestSnapshotConfigPath(t *testing.T) {
	got := SnapshotConfigPath("/proj")
	want := filepath.Join("/proj", "devbox", "snapshot.yml")
	if got != want {
		t.Errorf("SnapshotConfigPath: got %q want %q", got, want)
	}
}
