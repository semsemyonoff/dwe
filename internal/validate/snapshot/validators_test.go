package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devbox-cli/internal/config"
	coresnap "devbox-cli/internal/snapshot"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/validate"
)

// findFirst returns the first diagnostic whose Target equals target (exactly
// or as a prefix), or the zero value if none matches.
func findFirst(diags []validate.Diagnostic, target string) (validate.Diagnostic, bool) {
	for _, d := range diags {
		if d.Target == target || strings.HasPrefix(d.Target, target) {
			return d, true
		}
	}
	return validate.Diagnostic{}, false
}

func TestConfigLoadableValidator(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantDiag bool
	}{
		{"no error", nil, false},
		{"absent file", os.ErrNotExist, false},
		{"wrapped absent", fmt.Errorf("read: %w", os.ErrNotExist), false},
		{"parse error", errors.New("yaml: unknown field"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := &configLoadableValidator{err: tc.err}
			diags := v.Run(validate.Context{})
			if tc.wantDiag {
				if len(diags) != 1 || diags[0].Severity != validate.SeverityError {
					t.Fatalf("want one error diagnostic, got %+v", diags)
				}
			} else if len(diags) != 0 {
				t.Fatalf("want no diagnostics, got %+v", diags)
			}
		})
	}
}

func TestCreateRestoreDefined(t *testing.T) {
	cfg := &config.SnapshotConfig{}
	if got := (&createDefinedValidator{cfg: cfg}).Run(validate.Context{}); len(got) != 1 || got[0].Severity != validate.SeverityInfo {
		t.Fatalf("create missing: want info, got %+v", got)
	}
	if got := (&restoreDefinedValidator{cfg: cfg}).Run(validate.Context{}); len(got) != 1 || got[0].Severity != validate.SeverityInfo {
		t.Fatalf("restore missing: want info, got %+v", got)
	}

	cfg.Create = &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "x"}}}
	cfg.Restore = &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "y"}}}
	if got := (&createDefinedValidator{cfg: cfg}).Run(validate.Context{}); len(got) != 0 {
		t.Fatalf("create defined: want no diag, got %+v", got)
	}
	if got := (&restoreDefinedValidator{cfg: cfg}).Run(validate.Context{}); len(got) != 0 {
		t.Fatalf("restore defined: want no diag, got %+v", got)
	}

	// nil cfg → silent
	if got := (&createDefinedValidator{cfg: nil}).Run(validate.Context{}); len(got) != 0 {
		t.Fatalf("nil cfg: want no diag, got %+v", got)
	}
}

func TestVariantPairingValidator(t *testing.T) {
	cfg := &config.SnapshotConfig{
		Create: &config.SnapshotWorkflow{
			Steps: []model.WorkflowStep{{Command: "x"}},
			Variants: map[string]config.SnapshotWorkflow{
				"db-only": {Steps: []model.WorkflowStep{{Command: "y"}}},
			},
		},
	}
	// No restore block at all → warn.
	got := (&variantPairingValidator{cfg: cfg}).Run(validate.Context{})
	if len(got) != 1 || got[0].Severity != validate.SeverityWarning {
		t.Fatalf("no restore: want one warning, got %+v", got)
	}

	// Restore has default steps → variant covered by fallback.
	cfg.Restore = &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "z"}}}
	if got := (&variantPairingValidator{cfg: cfg}).Run(validate.Context{}); len(got) != 0 {
		t.Fatalf("default restore: want no diag, got %+v", got)
	}

	// Restore exists but only with mismatched variants and no default steps → warn.
	cfg.Restore = &config.SnapshotWorkflow{
		Variants: map[string]config.SnapshotWorkflow{
			"other": {Steps: []model.WorkflowStep{{Command: "z"}}},
		},
	}
	if got := (&variantPairingValidator{cfg: cfg}).Run(validate.Context{}); len(got) != 1 || got[0].Severity != validate.SeverityWarning {
		t.Fatalf("mismatched variant: want one warning, got %+v", got)
	}

	// Restore has matching variant → no diag.
	cfg.Restore.Variants["db-only"] = config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "z"}}}
	if got := (&variantPairingValidator{cfg: cfg}).Run(validate.Context{}); len(got) != 0 {
		t.Fatalf("matched variant: want no diag, got %+v", got)
	}
}

func TestRollbackTargetExistsValidator(t *testing.T) {
	root := t.TempDir()
	cfg := &config.SnapshotConfig{RollbackTarget: "baseline"}
	v := &rollbackTargetExistsValidator{cfg: cfg, baseDir: root}

	// Target missing → warn.
	got := v.Run(validate.Context{})
	if len(got) != 1 || got[0].Severity != validate.SeverityWarning {
		t.Fatalf("missing target: want one warning, got %+v", got)
	}

	// Create the target dir → no diag.
	if err := os.MkdirAll(filepath.Join(root, "snapshots", "baseline"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := v.Run(validate.Context{}); len(got) != 0 {
		t.Fatalf("target present: want no diag, got %+v", got)
	}

	// Empty rollback_target → silent.
	cfg.RollbackTarget = ""
	if got := v.Run(validate.Context{}); len(got) != 0 {
		t.Fatalf("empty target: want no diag, got %+v", got)
	}
}

func TestTemplateScopeValidator_RejectsCreatedAtInCreate(t *testing.T) {
	cfg := &config.SnapshotConfig{
		Create: &config.SnapshotWorkflow{
			Steps: []model.WorkflowStep{
				{Command: "x", With: map[string]string{"out": "${snapshot.created_at}"}},
			},
		},
	}
	got := (&templateScopeValidator{cfg: cfg}).Run(validate.Context{})
	if d, ok := findFirst(got, "template_scope.create.steps[0].with.out"); !ok || d.Severity != validate.SeverityError {
		t.Fatalf("want error for created_at in create, got %+v", got)
	}
}

func TestTemplateScopeValidator_AllowsCreatedAtInRestore(t *testing.T) {
	cfg := &config.SnapshotConfig{
		Restore: &config.SnapshotWorkflow{
			Steps: []model.WorkflowStep{
				{Command: "x", When: "file-exists ${snapshot.path}/db", With: map[string]string{"at": "${snapshot.created_at}"}},
			},
		},
	}
	if got := (&templateScopeValidator{cfg: cfg}).Run(validate.Context{}); len(got) != 0 {
		t.Fatalf("restore allows created_at + path: want no diag, got %+v", got)
	}
}

func TestTemplateScopeValidator_WalksParallelAndVariants(t *testing.T) {
	cfg := &config.SnapshotConfig{
		Create: &config.SnapshotWorkflow{
			Variants: map[string]config.SnapshotWorkflow{
				"v": {Steps: []model.WorkflowStep{
					{Parallel: &model.WorkflowParallel{Steps: []model.WorkflowStep{
						{Command: "x", With: map[string]string{"k": "${snapshot.created_at}"}},
					}}},
				}},
			},
		},
	}
	got := (&templateScopeValidator{cfg: cfg}).Run(validate.Context{})
	if len(got) == 0 {
		t.Fatalf("want error for nested created_at in create variant parallel, got none")
	}
}

func writeManifest(t *testing.T, dir string, m *coresnap.Manifest) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := coresnap.SaveManifest(filepath.Join(dir, coresnap.ManifestFileName), m); err != nil {
		t.Fatal(err)
	}
}

func TestPerSnapshotValidator_ManifestMissing(t *testing.T) {
	root := t.TempDir()
	snapDir := filepath.Join(root, "snapshots", "broken")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	all := All(nil, nil, nil, root, nil, false)
	diags := runAll(all)
	if d, ok := findFirst(diags, "broken.manifest_valid"); !ok || d.Severity != validate.SeverityError {
		t.Fatalf("want manifest_valid error, got %+v", diags)
	}
}

func TestPerSnapshotValidator_ArtifactMissing(t *testing.T) {
	root := t.TempDir()
	snapDir := filepath.Join(root, "snapshots", "snap1")
	writeManifest(t, snapDir, &coresnap.Manifest{
		Name:      "snap1",
		CreatedAt: time.Now().UTC(),
		Artifacts: []coresnap.ArtifactInfo{{Path: "db/main.sql", Size: 10, Sha256: "deadbeef"}},
	})

	all := All(nil, nil, nil, root, nil, false)
	diags := runAll(all)
	if d, ok := findFirst(diags, "snap1.artifacts_exist"); !ok || d.Severity != validate.SeverityError {
		t.Fatalf("want artifacts_exist error, got %+v", diags)
	}
}

func TestPerSnapshotValidator_LastCreateFailedInfo(t *testing.T) {
	root := t.TempDir()
	snapDir := filepath.Join(root, "snapshots", "snap2")
	writeManifest(t, snapDir, &coresnap.Manifest{
		Name:       "snap2",
		CreatedAt:  time.Now().UTC(),
		LastCreate: &coresnap.LastCreate{Status: coresnap.StatusFailed, FailedStep: "db.dump"},
	})
	all := All(nil, nil, nil, root, nil, false)
	diags := runAll(all)
	if d, ok := findFirst(diags, "snap2.last_create_failed"); !ok || d.Severity != validate.SeverityInfo {
		t.Fatalf("want last_create_failed info, got %+v", diags)
	}
}

func TestPerSnapshotValidator_ChecksumsVerifyDetectsTampering(t *testing.T) {
	root := t.TempDir()
	snapDir := filepath.Join(root, "snapshots", "snap3")
	// Write a real artifact and a manifest with the correct sha256.
	if err := os.MkdirAll(filepath.Join(snapDir, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(snapDir, "db", "main.sql")
	if err := os.WriteFile(artifactPath, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Compute the real sha256 by calling ScanArtifacts.
	scanned, err := coresnap.ScanArtifacts(snapDir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	writeManifest(t, snapDir, &coresnap.Manifest{
		Name:      "snap3",
		CreatedAt: time.Now().UTC(),
		Artifacts: scanned,
	})
	// First, without --verify, no checksum diagnostic.
	all := All(nil, nil, nil, root, nil, false)
	if d, ok := findFirst(runAll(all), "snap3.checksums"); ok {
		t.Fatalf("verify off: want no checksums diagnostic, got %+v", d)
	}
	// With --verify and unchanged content, expect OK.
	if d, ok := findFirst(runAll(All(nil, nil, nil, root, nil, true)), "snap3.checksums"); !ok || d.Severity != validate.SeverityOK {
		t.Fatalf("verify on, unchanged: want OK checksums, got %+v", d)
	}
	// Tamper with the file → mismatch warning.
	if err := os.WriteFile(artifactPath, []byte("tampered!!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d, ok := findFirst(runAll(All(nil, nil, nil, root, nil, true)), "snap3.checksums"); !ok || d.Severity != validate.SeverityWarning {
		t.Fatalf("verify on, tampered: want warning, got %+v", d)
	}
}

func TestAll_NilCfg_NoPanic(t *testing.T) {
	root := t.TempDir()
	got := All(nil, nil, nil, root, nil, false)
	if len(got) < 6 {
		t.Fatalf("expected at least 6 base validators, got %d", len(got))
	}
	// Running them all should not panic.
	_ = runAll(got)
}

// runAll runs every validator and concatenates diagnostics.
func runAll(vs []validate.Validator) []validate.Diagnostic {
	var out []validate.Diagnostic
	for _, v := range vs {
		out = append(out, v.Run(validate.Context{})...)
	}
	return out
}
