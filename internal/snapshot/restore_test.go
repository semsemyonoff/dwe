package snapshot

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/usercommands/model"
)

// newSnapCfgWithRestore builds a snapshot config exposing a default `restore`
// block of the given steps and optional named variants.
func newSnapCfgWithRestore(steps []model.WorkflowStep, variants map[string]config.SnapshotVariant) *config.SnapshotConfig {
	return &config.SnapshotConfig{
		Create:  &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "noop"}}},
		Restore: &config.SnapshotWorkflow{Steps: steps, Variants: variants},
	}
}

// createBaselineSnap is a tiny helper: run Create with a single shell step
// that writes a marker file under ${snapshot.path}/marker, returning the
// resulting snapshot dir.
func createBaselineSnap(t *testing.T, baseDir, name, hash string) string {
	t.Helper()
	reg := newRegistryWith(t, "fake.marker",
		`mkdir -p "${snapshot.path}" && printf hello > "${snapshot.path}/marker"`)
	snapCfg := &config.SnapshotConfig{
		Create:  &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "fake.marker"}}},
		Restore: &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "fake.marker"}}},
	}

	// Seed deploy state so manifest.Project.ConfigHash is populated from baseDir.
	if hash != "" {
		stateDir := filepath.Join(baseDir, ".devbox", "deploy")
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatalf("mkdir state: %v", err)
		}
		if err := os.WriteFile(filepath.Join(stateDir, "state.yml"),
			[]byte("schema_version: \"1\"\nproject:\n  status: deployed\n  config_hash: "+hash+"\n"),
			0o644); err != nil {
			t.Fatalf("write state: %v", err)
		}
	}

	res, err := Create(context.Background(), CreateParams{
		Cfg:      testCfg(),
		SnapCfg:  snapCfg,
		Registry: reg,
		BaseDir:  baseDir,
		Name:     name,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Status != StatusOk {
		t.Fatalf("Create status = %q", res.Status)
	}
	return res.SnapshotDir
}

// writeDeployState writes a deploy state.yml with the given config_hash.
func writeDeployState(t *testing.T, baseDir, hash string) {
	t.Helper()
	stateDir := filepath.Join(baseDir, ".devbox", "deploy")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	body := "schema_version: \"1\"\nproject:\n  status: deployed\n  config_hash: " + hash + "\n"
	if err := os.WriteFile(filepath.Join(stateDir, "state.yml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

func TestRestore_RoundTripWritesBackupAndUpdatesCurrent(t *testing.T) {
	tmp := t.TempDir()

	// Seed the working-copy devbox files so the pre-restore backup has something
	// to capture.
	writeStringFile(t, filepath.Join(tmp, "devbox", "local.yml"), "live: local")
	writeStringFile(t, filepath.Join(tmp, journal.DefaultRelPath),
		"schema_version: \"1\"\nproject:\n  status: deployed\n  config_hash: live\n")

	createBaselineSnap(t, tmp, "snap1", "live")

	// Verify the snapshot captured the devbox files.
	if _, err := os.Stat(filepath.Join(tmp, "snapshots", "snap1", DevboxSubdir, "local.yml")); err != nil {
		t.Fatalf("snapshot did not capture devbox/local.yml: %v", err)
	}

	// Mutate working-copy devbox/local.yml after the snapshot.
	writeStringFile(t, filepath.Join(tmp, "devbox", "local.yml"), "mutated")

	// Restore: workflow simply re-writes the marker file.
	reg := newRegistryWith(t, "fake.marker",
		`mkdir -p "${snapshot.path}" && printf restored > "${snapshot.path}/restore-marker"`)
	snapCfg := newSnapCfgWithRestore(
		[]model.WorkflowStep{{Command: "fake.marker"}},
		nil,
	)

	var out, errBuf bytes.Buffer
	res, err := Restore(context.Background(), RestoreParams{
		Cfg:         testCfg(),
		SnapCfg:     snapCfg,
		Registry:    reg,
		BaseDir:     tmp,
		Name:        "snap1",
		SkipConfirm: true,
		Stdout:      &out,
		Stderr:      &errBuf,
	})
	if err != nil {
		t.Fatalf("Restore: %v (stderr=%s)", err, errBuf.String())
	}
	if res.Status != StatusOk {
		t.Fatalf("status = %q (stderr=%s)", res.Status, errBuf.String())
	}

	// devbox/local.yml restored from snapshot.
	body, err := os.ReadFile(filepath.Join(tmp, "devbox", "local.yml"))
	if err != nil {
		t.Fatalf("read local.yml: %v", err)
	}
	if string(body) != "live: local" {
		t.Errorf("local.yml = %q want %q", string(body), "live: local")
	}

	// Pre-restore backup captured the mutated content.
	backup, err := os.ReadFile(filepath.Join(res.BackupDir, "local.yml"))
	if err != nil {
		t.Fatalf("read backup local.yml: %v", err)
	}
	if string(backup) != "mutated" {
		t.Errorf("backup local.yml = %q want %q", string(backup), "mutated")
	}

	// Restore workflow ran (wrote the restore-marker).
	if _, err := os.Stat(filepath.Join(res.SnapshotDir, "restore-marker")); err != nil {
		t.Errorf("restore-marker missing: %v", err)
	}

	// Current pointer is updated.
	cur, err := ReadCurrent(tmp)
	if err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}
	if cur != "snap1" {
		t.Errorf("current = %q want snap1", cur)
	}

	// Manifest records the successful restore.
	m, err := LoadManifest(res.ManifestPath)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.LastRestore == nil || m.LastRestore.Status != StatusOk {
		t.Errorf("LastRestore = %+v", m.LastRestore)
	}
}

func TestRestore_PreservesLocalYMLPorts(t *testing.T) {
	tmp := t.TempDir()

	// Seed working-copy local.yml with both a port (preserved) and an
	// enabled flag (snapshotted).
	writeStringFile(t, filepath.Join(tmp, "devbox", "local.yml"),
		"services:\n  main:\n    ports:\n      - 9090\n    enabled: true\n")

	snapCfg := &config.SnapshotConfig{
		Create:  &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "noop"}}},
		Restore: &config.SnapshotWorkflow{Steps: []model.WorkflowStep{{Command: "noop"}}},
		LocalYML: config.LocalYMLPolicy{
			PreserveKeys: []string{"services.main.ports"},
		},
	}
	reg := newRegistryWith(t, "noop", "true")

	if _, err := Create(context.Background(), CreateParams{
		Cfg: testCfg(), SnapCfg: snapCfg, Registry: reg, BaseDir: tmp, Name: "preserved",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Mutate the working-copy local.yml: change the port (preserved) and
	// the enabled flag (snapshotted).
	writeStringFile(t, filepath.Join(tmp, "devbox", "local.yml"),
		"services:\n  main:\n    ports:\n      - 7777\n    enabled: false\n")

	var errBuf bytes.Buffer
	res, err := Restore(context.Background(), RestoreParams{
		Cfg: testCfg(), SnapCfg: snapCfg, Registry: reg, BaseDir: tmp,
		Name: "preserved", SkipConfirm: true, Stderr: &errBuf,
	})
	if err != nil {
		t.Fatalf("Restore: %v (stderr=%s)", err, errBuf.String())
	}
	if res.Status != StatusOk {
		t.Fatalf("status = %q", res.Status)
	}

	body, err := os.ReadFile(filepath.Join(tmp, "devbox", "local.yml"))
	if err != nil {
		t.Fatalf("read local.yml: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "7777") {
		t.Errorf("preserved port not spliced from working copy; body=%q", got)
	}
	if !strings.Contains(got, "enabled: true") {
		t.Errorf("snapshot value not restored for enabled; body=%q", got)
	}
}

func TestRestore_ConfigHashDivergedBlocksWhenRequired(t *testing.T) {
	tmp := t.TempDir()
	writeDeployState(t, tmp, "snap-hash")
	createBaselineSnap(t, tmp, "s", "snap-hash")

	// Change the live config_hash so it diverges.
	writeDeployState(t, tmp, "live-hash")
	// Reset current pointer to a sentinel so we can verify the blocked restore
	// did not touch it.
	if err := WriteCurrent(tmp, "sentinel"); err != nil {
		t.Fatalf("WriteCurrent: %v", err)
	}

	snapCfg := newSnapCfgWithRestore(
		[]model.WorkflowStep{{Command: "fake.marker"}},
		nil,
	)
	snapCfg.RequireMatchingConfig = true
	reg := newRegistryWith(t, "fake.marker", `true`)

	var errBuf bytes.Buffer
	_, err := Restore(context.Background(), RestoreParams{
		Cfg:         testCfg(),
		SnapCfg:     snapCfg,
		Registry:    reg,
		BaseDir:     tmp,
		Name:        "s",
		SkipConfirm: true,
		Stderr:      &errBuf,
	})
	var blocked *RestoreBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want RestoreBlockedError", err)
	}
	// Current pointer must NOT have been updated by the blocked restore.
	cur, _ := ReadCurrent(tmp)
	if cur != "sentinel" {
		t.Errorf("current = %q, want sentinel (must not change on blocked restore)", cur)
	}
	// Pre-restore backup must not have been created.
	if _, err := os.Stat(PreRestoreBackup(tmp)); err == nil {
		t.Errorf("backup dir created despite blocked restore")
	}
}

func TestRestore_EmptyManifestHashNeverBlocks(t *testing.T) {
	tmp := t.TempDir()
	// No deploy state: createBaselineSnap with empty hash → manifest hash empty.
	createBaselineSnap(t, tmp, "s", "")

	// Now write a live deploy state — divergence would otherwise trigger.
	writeDeployState(t, tmp, "live-hash")

	snapCfg := newSnapCfgWithRestore(
		[]model.WorkflowStep{{Command: "fake.marker"}},
		nil,
	)
	snapCfg.RequireMatchingConfig = true
	reg := newRegistryWith(t, "fake.marker", `true`)

	res, err := Restore(context.Background(), RestoreParams{
		Cfg:         testCfg(),
		SnapCfg:     snapCfg,
		Registry:    reg,
		BaseDir:     tmp,
		Name:        "s",
		SkipConfirm: true,
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if res.Status != StatusOk {
		t.Fatalf("status = %q", res.Status)
	}
}

func TestRestore_MissingVariantFallsBackToDefault(t *testing.T) {
	tmp := t.TempDir()
	createBaselineSnap(t, tmp, "v", "")

	// Mark the snapshot's manifest with a variant that the restore block
	// doesn't define.
	manifestPath := filepath.Join(tmp, "snapshots", "v", ManifestFileName)
	m, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m.Variant = "unknown-variant"
	if err := SaveManifest(manifestPath, m); err != nil {
		t.Fatalf("save: %v", err)
	}

	reg := newRegistryWith(t, "fake.marker",
		`mkdir -p "${snapshot.path}" && printf default > "${snapshot.path}/which"`)
	snapCfg := newSnapCfgWithRestore(
		[]model.WorkflowStep{{Command: "fake.marker"}},
		// Provide a different variant, NOT "unknown-variant", so the fallback
		// path is exercised.
		map[string]config.SnapshotVariant{
			"other": {Steps: []model.WorkflowStep{{Command: "fake.marker"}}},
		},
	)

	res, err := Restore(context.Background(), RestoreParams{
		Cfg:         testCfg(),
		SnapCfg:     snapCfg,
		Registry:    reg,
		BaseDir:     tmp,
		Name:        "v",
		SkipConfirm: true,
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if res.Status != StatusOk {
		t.Fatalf("status = %q", res.Status)
	}
	body, err := os.ReadFile(filepath.Join(res.SnapshotDir, "which"))
	if err != nil {
		t.Fatalf("read which: %v", err)
	}
	if string(body) != "default" {
		t.Errorf("ran wrong workflow body = %q", string(body))
	}
}

func TestRestore_InterruptedKeepsBackupAndCurrent(t *testing.T) {
	tmp := t.TempDir()

	// Capture a baseline and set the current pointer to something else so we
	// can verify it is NOT changed on cancellation.
	createBaselineSnap(t, tmp, "snap1", "")
	if err := WriteCurrent(tmp, "earlier"); err != nil {
		t.Fatalf("seed current: %v", err)
	}

	// Seed a local.yml so writePreRestoreBackup has something to capture.
	localDir := filepath.Join(tmp, "devbox")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatalf("mkdir devbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "local.yml"), []byte("before: restore"), 0o644); err != nil {
		t.Fatalf("seed local.yml: %v", err)
	}

	// A workflow step that blocks until ctx is cancelled.
	reg := newRegistryWith(t, "fake.sleep",
		`while true; do sleep 1; done`)
	snapCfg := newSnapCfgWithRestore(
		[]model.WorkflowStep{{Command: "fake.sleep"}},
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	var errBuf bytes.Buffer
	res, err := Restore(ctx, RestoreParams{
		Cfg:         testCfg(),
		SnapCfg:     snapCfg,
		Registry:    reg,
		BaseDir:     tmp,
		Name:        "snap1",
		SkipConfirm: true,
		Stderr:      &errBuf,
	})
	if err == nil {
		t.Fatalf("Restore: expected error from cancellation, got nil")
	}
	if res == nil {
		t.Fatalf("res is nil; expected partial result")
	}
	if res.Status != StatusInterrupted && res.Status != StatusFailed {
		t.Errorf("status = %q, want interrupted or failed", res.Status)
	}

	// Pre-restore backup must remain on disk for manual recovery.
	if _, err := os.Stat(res.BackupDir); err != nil {
		t.Errorf("backup dir missing after interrupt: %v", err)
	}
	// Current pointer must remain pointing at the earlier value.
	cur, _ := ReadCurrent(tmp)
	if cur != "earlier" {
		t.Errorf("current = %q, want earlier (must not change on cancel)", cur)
	}
	if !strings.Contains(errBuf.String(), "pre-restore") {
		t.Errorf("expected pre-restore hint on stderr: %s", errBuf.String())
	}
}

func TestRollback_DispatchesToRestoreTarget(t *testing.T) {
	tmp := t.TempDir()
	createBaselineSnap(t, tmp, "baseline", "")

	reg := newRegistryWith(t, "fake.marker",
		`mkdir -p "${snapshot.path}" && printf rollback > "${snapshot.path}/rb-marker"`)
	snapCfg := newSnapCfgWithRestore(
		[]model.WorkflowStep{{Command: "fake.marker"}},
		nil,
	)
	snapCfg.RollbackTarget = "baseline"

	res, err := Rollback(context.Background(), RestoreParams{
		Cfg:         testCfg(),
		SnapCfg:     snapCfg,
		Registry:    reg,
		BaseDir:     tmp,
		SkipConfirm: true,
	})
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if res.Status != StatusOk {
		t.Fatalf("status = %q", res.Status)
	}
	if _, err := os.Stat(filepath.Join(res.SnapshotDir, "rb-marker")); err != nil {
		t.Errorf("rollback marker missing: %v", err)
	}
}

// patchManifestServices rewrites the manifest at snapshots/<name>/manifest.yml
// to carry the given Services slice. Used by the services_mismatch tests
// because testCfg() has no services, so created snapshots otherwise carry an
// empty service set.
func patchManifestServices(t *testing.T, baseDir, name string, svcs []ServiceSnapshot) {
	t.Helper()
	mp := filepath.Join(baseDir, "snapshots", name, ManifestFileName)
	m, err := LoadManifest(mp)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	m.Project.Services = svcs
	if err := SaveManifest(mp, m); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
}

func TestRestore_ServicesMismatchPolicies(t *testing.T) {
	tests := []struct {
		name          string
		policy        string
		manifestSvcs  []ServiceSnapshot
		currentSvcs   map[string]config.ServiceConfig
		skipConfirm   bool
		wantBlocked   bool
		wantWarn      bool // expect warning on stderr (skip-confirm + non-empty diff)
		wantPromptCtx bool // expect ConfirmRestore to receive a non-empty diff
	}{
		{
			name:         "block_with_divergence_aborts",
			policy:       "block",
			manifestSvcs: []ServiceSnapshot{{Name: "db", Enabled: true}, {Name: "old", Enabled: true}},
			currentSvcs:  map[string]config.ServiceConfig{"db": {Enabled: true}, "new": {Enabled: true}},
			skipConfirm:  true,
			wantBlocked:  true,
		},
		{
			name:         "block_no_divergence_proceeds",
			policy:       "block",
			manifestSvcs: []ServiceSnapshot{{Name: "db", Enabled: true}},
			currentSvcs:  map[string]config.ServiceConfig{"db": {Enabled: true}},
			skipConfirm:  true,
		},
		{
			name:         "warn_skip_confirm_emits_warning",
			policy:       "warn",
			manifestSvcs: []ServiceSnapshot{{Name: "db", Enabled: true}},
			currentSvcs:  map[string]config.ServiceConfig{"db": {Enabled: false}},
			skipConfirm:  true,
			wantWarn:     true,
		},
		{
			name:          "warn_interactive_passes_diff_to_callback",
			policy:        "warn",
			manifestSvcs:  []ServiceSnapshot{{Name: "old", Enabled: true}},
			currentSvcs:   map[string]config.ServiceConfig{"new": {Enabled: true}},
			skipConfirm:   false,
			wantPromptCtx: true,
		},
		{
			name:         "ignore_skips_diff_entirely",
			policy:       "ignore",
			manifestSvcs: []ServiceSnapshot{{Name: "old", Enabled: true}},
			currentSvcs:  map[string]config.ServiceConfig{"new": {Enabled: true}},
			skipConfirm:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			createBaselineSnap(t, tmp, "s", "")
			patchManifestServices(t, tmp, "s", tc.manifestSvcs)

			// Seed a working-copy devbox/local.yml so we can detect any
			// unintended side effect on the blocked path.
			writeStringFile(t, filepath.Join(tmp, "devbox", "local.yml"), "untouched")

			reg := newRegistryWith(t, "fake.marker", `true`)
			snapCfg := newSnapCfgWithRestore(
				[]model.WorkflowStep{{Command: "fake.marker"}},
				nil,
			)
			snapCfg.ServicesMismatch.Policy = tc.policy

			cfg := testCfg()
			cfg.Services = tc.currentSvcs

			var gotCtx RestoreConfirmContext
			var callbackCalled bool

			var errBuf bytes.Buffer
			_, err := Restore(context.Background(), RestoreParams{
				Cfg:         cfg,
				SnapCfg:     snapCfg,
				Registry:    reg,
				BaseDir:     tmp,
				Name:        "s",
				SkipConfirm: tc.skipConfirm,
				Stderr:      &errBuf,
				ConfirmRestore: func(rc RestoreConfirmContext) (bool, error) {
					callbackCalled = true
					gotCtx = rc
					return true, nil
				},
			})

			if tc.wantBlocked {
				var sme *ServicesMismatchError
				if !errors.As(err, &sme) {
					t.Fatalf("err = %v, want ServicesMismatchError", err)
				}
				// No side effect on devbox/local.yml.
				body, _ := os.ReadFile(filepath.Join(tmp, "devbox", "local.yml"))
				if string(body) != "untouched" {
					t.Errorf("local.yml mutated despite block: %q", string(body))
				}
				// No pre-restore backup.
				if _, err := os.Stat(PreRestoreBackup(tmp)); err == nil {
					t.Errorf("backup dir created despite block")
				}
				// Callback must NOT be invoked when block fires.
				if callbackCalled {
					t.Errorf("ConfirmRestore invoked despite block")
				}
				return
			}
			if err != nil {
				t.Fatalf("Restore: %v (stderr=%s)", err, errBuf.String())
			}
			if tc.wantWarn {
				if !strings.Contains(errBuf.String(), "services diverge") {
					t.Errorf("expected services-diverge warning on stderr, got: %s", errBuf.String())
				}
			}
			if tc.wantPromptCtx {
				if !callbackCalled {
					t.Fatalf("ConfirmRestore was not invoked")
				}
				if gotCtx.ServicesDiff.IsEmpty() {
					t.Errorf("ConfirmRestore received empty ServicesDiff")
				}
			}
			if tc.policy == "ignore" {
				// ignore must produce no warning text even on a non-empty diff.
				if strings.Contains(errBuf.String(), "services diverge") {
					t.Errorf("ignore policy emitted warning: %s", errBuf.String())
				}
			}
		})
	}
}

func TestRestore_RejectedConfirmDoesNotTouchLocalYml(t *testing.T) {
	tmp := t.TempDir()
	createBaselineSnap(t, tmp, "s", "")
	patchManifestServices(t, tmp, "s", []ServiceSnapshot{{Name: "db", Enabled: true}})

	writeStringFile(t, filepath.Join(tmp, "devbox", "local.yml"), "untouched")

	snapCfg := newSnapCfgWithRestore(
		[]model.WorkflowStep{{Command: "fake.marker"}},
		nil,
	)
	snapCfg.ServicesMismatch.Policy = "warn"
	reg := newRegistryWith(t, "fake.marker", `true`)

	cfg := testCfg()
	cfg.Services = map[string]config.ServiceConfig{"db": {Enabled: false}}

	_, err := Restore(context.Background(), RestoreParams{
		Cfg:      cfg,
		SnapCfg:  snapCfg,
		Registry: reg,
		BaseDir:  tmp,
		Name:     "s",
		Stderr:   &bytes.Buffer{},
		ConfirmRestore: func(RestoreConfirmContext) (bool, error) {
			return false, nil
		},
	})
	var cancelled *RestoreCancelledError
	if !errors.As(err, &cancelled) {
		t.Fatalf("err = %v, want RestoreCancelledError", err)
	}
	body, _ := os.ReadFile(filepath.Join(tmp, "devbox", "local.yml"))
	if string(body) != "untouched" {
		t.Errorf("local.yml mutated on cancelled restore: %q", string(body))
	}
}

func TestRollback_NoTargetReturnsError(t *testing.T) {
	tmp := t.TempDir()
	snapCfg := newSnapCfgWithRestore(
		[]model.WorkflowStep{{Command: "fake.marker"}},
		nil,
	)
	// RollbackTarget is empty.

	_, err := Rollback(context.Background(), RestoreParams{
		Cfg:      testCfg(),
		SnapCfg:  snapCfg,
		Registry: newRegistryWith(t, "fake.marker", `true`),
		BaseDir:  tmp,
	})
	if err == nil || !strings.Contains(err.Error(), "rollback_target") {
		t.Errorf("err = %v, want rollback_target error", err)
	}
}
