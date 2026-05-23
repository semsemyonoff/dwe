package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestConstructor_New(t *testing.T) {
	fixed := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	c := NewManifestConstructor(func() time.Time { return fixed })
	m := c.New("snap")
	if m.Name != "snap" {
		t.Fatalf("name: %q", m.Name)
	}
	if !m.CreatedAt.Equal(fixed) {
		t.Fatalf("createdAt: got %v want %v", m.CreatedAt, fixed)
	}
}

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yml")
	fixed := time.Date(2026, 5, 24, 11, 2, 0, 0, time.UTC)
	c := NewManifestConstructor(func() time.Time { return fixed })
	m := c.New("feature-x")
	m.Description = "WIP"
	m.Project = ProjectInfo{Name: "tbm-next", ConfigHash: "abc123"}
	m.DevboxVersion = "0.42.0"
	m.Variant = "db-only"
	m.Artifacts = []ArtifactInfo{
		{Path: "db/main.sql.gz", Size: 1234, Sha256: "deadbeef"},
	}
	m.DevboxFiles = DevboxFiles{
		LocalYML:    "devbox/local.yml",
		DeployState: "devbox/deploy-state.yml",
	}
	m.LastCreate = &LastCreate{At: fixed, Status: StatusOk}

	if err := SaveManifest(path, m); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Name != m.Name || got.Description != m.Description {
		t.Errorf("name/description mismatch: %+v", got)
	}
	if got.Project != m.Project {
		t.Errorf("project mismatch: got %+v want %+v", got.Project, m.Project)
	}
	if got.Variant != "db-only" || got.DevboxVersion != "0.42.0" {
		t.Errorf("variant/version mismatch: %+v", got)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].Sha256 != "deadbeef" || got.Artifacts[0].Size != 1234 {
		t.Errorf("artifacts mismatch: %+v", got.Artifacts)
	}
	if got.LastCreate == nil || got.LastCreate.Status != StatusOk || !got.LastCreate.At.Equal(fixed) {
		t.Errorf("last_create mismatch: %+v", got.LastCreate)
	}
}

func TestSaveManifest_atomicNoLeftoverOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yml")
	c := NewManifestConstructor(func() time.Time { return time.Unix(0, 0).UTC() })
	if err := SaveManifest(path, c.New("snap")); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "manifest.yml" {
			continue
		}
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %q", e.Name())
		}
	}
}

func TestSaveManifest_nil(t *testing.T) {
	dir := t.TempDir()
	if err := SaveManifest(filepath.Join(dir, "manifest.yml"), nil); err == nil {
		t.Fatal("expected error for nil manifest")
	}
}

func TestSaveManifest_createsParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested/sub/manifest.yml")
	c := NewManifestConstructor(func() time.Time { return time.Unix(0, 0).UTC() })
	if err := SaveManifest(path, c.New("snap")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	}
}

func TestLoadManifest_missingFile(t *testing.T) {
	_, err := LoadManifest(filepath.Join(t.TempDir(), "missing.yml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
