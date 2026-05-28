package snapshot

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/usercommands/model"
)

func TestRemove_DeletesDirAndClearsCurrent(t *testing.T) {
	tmp := t.TempDir()
	snapCfg := &config.SnapshotConfig{}
	dir := SnapshotDir(tmp, snapCfg, "rm-me")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := SaveManifest(filepath.Join(dir, ManifestFileName), &Manifest{Name: "rm-me", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := WriteCurrent(tmp, "rm-me"); err != nil {
		t.Fatalf("write current: %v", err)
	}

	var out, errBuf bytes.Buffer
	res, err := Remove(context.Background(), RemoveParams{
		Cfg:         testCfg(),
		SnapCfg:     snapCfg,
		BaseDir:     tmp,
		Name:        "rm-me",
		SkipConfirm: true,
		Stdout:      &out,
		Stderr:      &errBuf,
	})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !res.ClearedCurrent {
		t.Errorf("expected ClearedCurrent=true")
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("snapshot dir should be gone, got stat err = %v", statErr)
	}
	cur, _ := ReadCurrent(tmp)
	if cur != "" {
		t.Errorf("current = %q want empty", cur)
	}
}

func TestRemove_RunsRemoveWorkflowBeforeDeletion(t *testing.T) {
	tmp := t.TempDir()
	snapCfg := &config.SnapshotConfig{
		Remove: &config.SnapshotWorkflow{
			Steps: []model.WorkflowStep{{Command: "rm.cleanup"}},
		},
	}
	marker := filepath.Join(tmp, "marker-removed")
	// The workflow runs from baseDir so a relative path resolves under tmp.
	reg := newRegistryWith(t, "rm.cleanup", `printf hello > marker-removed`)

	dir := SnapshotDir(tmp, snapCfg, "with-hook")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := SaveManifest(filepath.Join(dir, ManifestFileName), &Manifest{Name: "with-hook", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	var out, errBuf bytes.Buffer
	_, err := Remove(context.Background(), RemoveParams{
		Cfg:         testCfg(),
		SnapCfg:     snapCfg,
		Registry:    reg,
		BaseDir:     tmp,
		Name:        "with-hook",
		SkipConfirm: true,
		Stdout:      &out,
		Stderr:      &errBuf,
	})
	if err != nil {
		t.Fatalf("Remove: %v (stderr=%s)", err, errBuf.String())
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Errorf("expected remove workflow marker file, got: %v", statErr)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("snapshot dir should be gone, got: %v", statErr)
	}
}

func TestRemove_MissingSnapshotIsAnError(t *testing.T) {
	tmp := t.TempDir()
	_, err := Remove(context.Background(), RemoveParams{
		Cfg:         testCfg(),
		SnapCfg:     &config.SnapshotConfig{},
		BaseDir:     tmp,
		Name:        "nope",
		SkipConfirm: true,
	})
	if err == nil {
		t.Fatalf("expected error for missing snapshot")
	}
}

func TestRemove_RefusesWithoutConfirmCallback(t *testing.T) {
	tmp := t.TempDir()
	snapCfg := &config.SnapshotConfig{}
	dir := SnapshotDir(tmp, snapCfg, "nope")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := Remove(context.Background(), RemoveParams{
		Cfg:     testCfg(),
		SnapCfg: snapCfg,
		BaseDir: tmp,
		Name:    "nope",
		// SkipConfirm false, ConfirmRemove nil → cancelled.
	})
	if !errors.As(err, new(*RemoveCancelledError)) {
		t.Fatalf("expected RemoveCancelledError, got %v", err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("snapshot dir should still exist when cancelled, got: %v", statErr)
	}
}
